package ioc

import (
	"fmt"
	"io"
	"reflect"
)

// builder accumulates the frozen, indexed view of a ServiceCollection while Build constructs it.
type builder struct {
	entries []*descriptor

	typeIndex   map[reflect.Type]int   // last unkeyed registration per type
	typeIndices map[reflect.Type][]int // every unkeyed registration per type, in registration order

	keyedIndex   map[reflect.Type]map[any]int   // last keyed registration per (type, key)
	keyedIndices map[reflect.Type]map[any][]int // every keyed registration per (type, key)
}

func (b *builder) appendEntry(d *descriptor) int {
	idx := len(b.entries)
	d.index = idx
	b.entries = append(b.entries, d)
	return idx
}

// resolveParamIndices resolves each of paramTypes to the index of its last unkeyed registration,
// building the static dependency edges used for cycle detection and construction ordering.
func (b *builder) resolveParamIndices(forType reflect.Type, paramTypes []reflect.Type) ([]int, error) {
	indices := make([]int, len(paramTypes))
	for i, pt := range paramTypes {
		idx, ok := b.typeIndex[pt]
		if !ok {
			return nil, fmt.Errorf("di: resolving dependency %s for %s: %w", pt, forType, ErrServiceNotRegistered)
		}
		indices[i] = idx
	}
	return indices, nil
}

// topoSort computes a construction order for b.entries (dependencies before dependents) via DFS,
// returning a CircularDependencyError if the dependency graph (constructor params + decorator
// params) contains a cycle.
func (b *builder) topoSort() ([]int, error) {
	const (
		white = iota
		gray
		black
	)
	color := make([]int, len(b.entries))
	order := make([]int, 0, len(b.entries))
	var path []reflect.Type

	var visit func(idx int) error
	visit = func(idx int) error {
		switch color[idx] {
		case black:
			return nil
		case gray:
			chain := append(append([]reflect.Type{}, path...), b.entries[idx].serviceType)
			return &CircularDependencyError{Chain: chain}
		}
		color[idx] = gray
		path = append(path, b.entries[idx].serviceType)

		d := b.entries[idx]
		deps := append([]int{}, d.paramIndices...)
		for _, dec := range d.decorators {
			deps = append(deps, dec.paramIndices...)
		}
		for _, depIdx := range deps {
			if err := visit(depIdx); err != nil {
				return err
			}
		}

		path = path[:len(path)-1]
		color[idx] = black
		order = append(order, idx)
		return nil
	}

	for idx := range b.entries {
		if err := visit(idx); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Build resolves every registration's dependency graph, checks for missing and circular
// dependencies, and eagerly constructs every service in dependency order (dependencies before
// dependents). The returned ServiceProvider only ever reads pre-built values: Resolve/ResolveAll/
// ResolveKeyed/ResolveAllKeyed are plain map/slice lookups with no further construction, reflection
// or locking.
func (sc *ServiceCollection) Build() (*ServiceProvider, error) {
	b := &builder{
		typeIndex:    make(map[reflect.Type]int),
		typeIndices:  make(map[reflect.Type][]int),
		keyedIndex:   make(map[reflect.Type]map[any]int),
		keyedIndices: make(map[reflect.Type]map[any][]int),
	}

	for t, descs := range sc.descriptors {
		for _, d := range descs {
			idx := b.appendEntry(d)
			b.typeIndex[t] = idx
			b.typeIndices[t] = append(b.typeIndices[t], idx)
		}
	}
	for t, byKey := range sc.keyedDescriptors {
		for key, descs := range byKey {
			idxs := make([]int, 0, len(descs))
			for _, d := range descs {
				idxs = append(idxs, b.appendEntry(d))
			}
			if b.keyedIndex[t] == nil {
				b.keyedIndex[t] = make(map[any]int)
				b.keyedIndices[t] = make(map[any][]int)
			}
			b.keyedIndex[t][key] = idxs[len(idxs)-1]
			b.keyedIndices[t][key] = idxs
		}
	}

	for _, d := range b.entries {
		if d.isInstance {
			continue
		}
		idxs, err := b.resolveParamIndices(d.serviceType, d.paramTypes)
		if err != nil {
			return nil, err
		}
		d.paramIndices = idxs
		for i := range d.decorators {
			decIdxs, err := b.resolveParamIndices(d.serviceType, d.decorators[i].paramTypes)
			if err != nil {
				return nil, err
			}
			d.decorators[i].paramIndices = decIdxs
		}
	}

	order, err := b.topoSort()
	if err != nil {
		return nil, err
	}

	var closers []io.Closer
	for _, idx := range order {
		d := b.entries[idx]

		if d.isInstance {
			d.singletonVal = d.instance
		} else {
			args := make([]reflect.Value, len(d.paramIndices))
			for i, pidx := range d.paramIndices {
				args[i] = reflect.ValueOf(b.entries[pidx].singletonVal)
			}
			v, err := finishCall(d.ctor.Call(args), d.ctorHasErr)
			if err != nil {
				return nil, fmt.Errorf("di: constructing %s: %w", d.serviceType, err)
			}
			d.singletonVal = v
		}

		for _, dec := range d.decorators {
			args := make([]reflect.Value, len(dec.paramIndices)+1)
			args[0] = reflect.ValueOf(d.singletonVal)
			for i, pidx := range dec.paramIndices {
				args[i+1] = reflect.ValueOf(b.entries[pidx].singletonVal)
			}
			v, err := finishCall(dec.fn.Call(args), dec.hasErr)
			if err != nil {
				return nil, fmt.Errorf("di: decorating %s: %w", d.serviceType, err)
			}
			d.singletonVal = v
		}

		if c, ok := d.singletonVal.(io.Closer); ok {
			closers = append(closers, c)
		}
	}

	return &ServiceProvider{
		entries:      b.entries,
		typeIndex:    b.typeIndex,
		typeIndices:  b.typeIndices,
		keyedIndex:   b.keyedIndex,
		keyedIndices: b.keyedIndices,
		closers:      closers,
	}, nil
}
