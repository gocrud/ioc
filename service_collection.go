package ioc

import (
	"fmt"
	"reflect"
)

// ServiceCollection collects service registrations before they are built into a ServiceProvider.
type ServiceCollection struct {
	descriptors      map[reflect.Type][]*descriptor
	keyedDescriptors map[reflect.Type]map[any][]*descriptor
	optionConfigs    map[reflect.Type][]func(any)
	monitorConfigs   map[reflect.Type][]func(any)
}

// NewServiceCollection creates an empty ServiceCollection.
func NewServiceCollection() *ServiceCollection {
	return &ServiceCollection{
		descriptors:      make(map[reflect.Type][]*descriptor),
		keyedDescriptors: make(map[reflect.Type]map[any][]*descriptor),
		optionConfigs:    make(map[reflect.Type][]func(any)),
		monitorConfigs:   make(map[reflect.Type][]func(any)),
	}
}

func (sc *ServiceCollection) isRegistered(t reflect.Type) bool {
	return len(sc.descriptors[t]) > 0
}

func (sc *ServiceCollection) isKeyedRegistered(t reflect.Type, key any) bool {
	return len(sc.keyedDescriptors[t][key]) > 0
}

// AddSingleton registers TService. provider may be a pre-built instance or an auto-wired
// constructor func(deps...) TImpl / func(deps...) (TImpl, error), where every parameter type must
// itself be registered. See buildDescriptor for the accepted provider shapes.
func (sc *ServiceCollection) AddSingleton[TService any](provider any) *ServiceCollection {
	t := reflect.TypeFor[TService]()
	sc.descriptors[t] = append(sc.descriptors[t], buildDescriptor[TService](t, provider))
	return sc
}

// TryAddSingleton registers TService only if it has no existing registration.
func (sc *ServiceCollection) TryAddSingleton[TService any](provider any) *ServiceCollection {
	t := reflect.TypeFor[TService]()
	if sc.isRegistered(t) {
		return sc
	}
	return sc.AddSingleton[TService](provider)
}

// AddKeyedSingleton registers TService under the given key. The service is only resolvable via
// ResolveKeyed/MustResolveKeyed/ResolveAllKeyed with a matching key, never via the unkeyed
// Resolve/MustResolve/ResolveAll. key must be a comparable value (e.g. string, int, or an enum),
// since it is used as a map key.
func (sc *ServiceCollection) AddKeyedSingleton[TService any](key any, provider any) *ServiceCollection {
	t := reflect.TypeFor[TService]()
	if sc.keyedDescriptors[t] == nil {
		sc.keyedDescriptors[t] = make(map[any][]*descriptor)
	}
	sc.keyedDescriptors[t][key] = append(sc.keyedDescriptors[t][key], buildDescriptor[TService](t, provider))
	return sc
}

// TryAddKeyedSingleton registers TService/key only if that exact (TService, key) pair has no
// existing registration.
func (sc *ServiceCollection) TryAddKeyedSingleton[TService any](key any, provider any) *ServiceCollection {
	t := reflect.TypeFor[TService]()
	if sc.isKeyedRegistered(t, key) {
		return sc
	}
	return sc.AddKeyedSingleton[TService](key, provider)
}

// Decorate wraps the last registration of TService. decoratorFn's first parameter receives the
// originally constructed instance; any additional parameters are auto-wired dependencies resolved
// like a constructor's. TService must already be registered. Multiple Decorate calls for the same
// TService stack in registration order.
func (sc *ServiceCollection) Decorate[TService any](decoratorFn any) *ServiceCollection {
	t := reflect.TypeFor[TService]()
	descs := sc.descriptors[t]
	if len(descs) == 0 {
		panic(fmt.Sprintf("di: cannot decorate unregistered service %s", t))
	}
	d := descs[len(descs)-1]
	d.decorators = append(d.decorators, buildDecorator[TService](t, decoratorFn))
	return sc
}
