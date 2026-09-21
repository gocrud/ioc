package ioc

import "reflect"

// decorator wraps an already-constructed instance with extra behavior. fn's first parameter is
// always the inner instance; any remaining parameters are additional auto-wired dependencies,
// resolved into paramIndices during ServiceCollection.Build like a constructor's parameters.
type decorator struct {
	fn           reflect.Value
	hasErr       bool
	paramTypes   []reflect.Type
	paramIndices []int
}

// descriptor describes a single registration of a service type. Every descriptor is a Singleton:
// its dependency graph is resolved, cycle-checked and constructed exactly once, eagerly, during
// ServiceCollection.Build. ServiceProvider only ever reads singletonVal afterwards.
type descriptor struct {
	serviceType reflect.Type
	index       int // stable position in ServiceProvider.entries, assigned during Build

	isInstance bool
	instance   any

	ctor         reflect.Value // valid when !isInstance
	ctorHasErr   bool
	paramTypes   []reflect.Type // constructor dependency types, resolved into paramIndices during Build
	paramIndices []int

	decorators []decorator

	singletonVal any
}
