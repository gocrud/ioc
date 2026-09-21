package ioc

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
)

// ServiceProvider resolves services that were registered in a ServiceCollection and eagerly built
// by Build. Every service is a Singleton constructed once, in dependency order, during Build, so
// Resolve/MustResolve/ResolveAll/ResolveKeyed/MustResolveKeyed/ResolveAllKeyed never construct
// anything themselves: they only read the values Build already constructed.
type ServiceProvider struct {
	entries []*descriptor

	typeIndex   map[reflect.Type]int
	typeIndices map[reflect.Type][]int

	keyedIndex   map[reflect.Type]map[any]int
	keyedIndices map[reflect.Type]map[any][]int

	closers   []io.Closer // construction order; Close disposes them in reverse
	closeOnce sync.Once
	closeErr  error
}

// Close disposes every constructed singleton that implements io.Closer, in the reverse of their
// construction order. Safe to call multiple times; only the first call actually closes anything.
func (sp *ServiceProvider) Close() error {
	sp.closeOnce.Do(func() {
		var errs []error
		for i := len(sp.closers) - 1; i >= 0; i-- {
			if err := sp.closers[i].Close(); err != nil {
				errs = append(errs, err)
			}
		}
		sp.closeErr = errors.Join(errs...)
	})
	return sp.closeErr
}

// Resolve resolves the last registration of T. Returns ErrServiceNotRegistered if T was never registered.
func (sp *ServiceProvider) Resolve[T any]() (T, error) {
	var zero T
	t := reflect.TypeFor[T]()
	idx, ok := sp.typeIndex[t]
	if !ok {
		return zero, fmt.Errorf("%w: %s", ErrServiceNotRegistered, t)
	}
	return sp.entries[idx].singletonVal.(T), nil
}

// MustResolve resolves T like Resolve, but panics instead of returning an error.
func (sp *ServiceProvider) MustResolve[T any]() T {
	v, err := sp.Resolve[T]()
	if err != nil {
		panic(err)
	}
	return v
}

// ResolveAll resolves every registration of T, in registration order.
func (sp *ServiceProvider) ResolveAll[T any]() ([]T, error) {
	t := reflect.TypeFor[T]()
	idxs := sp.typeIndices[t]
	result := make([]T, len(idxs))
	for i, idx := range idxs {
		result[i] = sp.entries[idx].singletonVal.(T)
	}
	return result, nil
}

// ResolveKeyed resolves the last registration of T made under the given key via AddKeyedSingleton.
// Returns ErrServiceNotRegistered if no registration exists for (T, key).
func (sp *ServiceProvider) ResolveKeyed[T any](key any) (T, error) {
	var zero T
	t := reflect.TypeFor[T]()
	idx, ok := sp.keyedIndex[t][key]
	if !ok {
		return zero, fmt.Errorf("%w: %s (key=%v)", ErrServiceNotRegistered, t, key)
	}
	return sp.entries[idx].singletonVal.(T), nil
}

// MustResolveKeyed resolves T under key like ResolveKeyed, but panics instead of returning an error.
func (sp *ServiceProvider) MustResolveKeyed[T any](key any) T {
	v, err := sp.ResolveKeyed[T](key)
	if err != nil {
		panic(err)
	}
	return v
}

// ResolveAllKeyed resolves every registration of T made under the given key, in registration order.
func (sp *ServiceProvider) ResolveAllKeyed[T any](key any) ([]T, error) {
	t := reflect.TypeFor[T]()
	idxs := sp.keyedIndices[t][key]
	result := make([]T, len(idxs))
	for i, idx := range idxs {
		result[i] = sp.entries[idx].singletonVal.(T)
	}
	return result, nil
}
