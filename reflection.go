package ioc

import (
	"fmt"
	"reflect"
)

var errType = reflect.TypeFor[error]()

// buildDescriptor inspects provider and returns a descriptor for service type t. provider may be:
//   - a non-func value: used as a pre-built instance
//   - func(deps...) TImpl or func(deps...) (TImpl, error): an auto-wired constructor, where every
//     parameter type must itself be a registered service (resolved during ServiceCollection.Build)
//
// Invalid shapes panic, since they represent a programming/configuration mistake.
func buildDescriptor[T any](t reflect.Type, provider any) *descriptor {
	if provider == nil {
		panic(fmt.Sprintf("di: nil provider registered for service %s", t))
	}

	pv := reflect.ValueOf(provider)
	if pv.Kind() != reflect.Func {
		pt := reflect.TypeOf(provider)
		if !pt.AssignableTo(t) {
			panic(fmt.Sprintf("di: instance of type %s is not assignable to service type %s", pt, t))
		}
		return &descriptor{serviceType: t, isInstance: true, instance: provider}
	}

	ft := pv.Type()
	hasErr := validateReturn(t, ft, "provider function")

	paramTypes := make([]reflect.Type, ft.NumIn())
	for i := range paramTypes {
		paramTypes[i] = ft.In(i)
	}
	return &descriptor{
		serviceType: t,
		ctor:        pv,
		ctorHasErr:  hasErr,
		paramTypes:  paramTypes,
	}
}

// buildDecorator inspects decoratorFn and returns a decorator for service type t. decoratorFn must
// be func(inner T, deps...) T or func(inner T, deps...) (T, error), where deps are additional
// auto-wired dependencies resolved like a constructor's parameters.
func buildDecorator[T any](t reflect.Type, decoratorFn any) decorator {
	pv := reflect.ValueOf(decoratorFn)
	if pv.Kind() != reflect.Func {
		panic(fmt.Sprintf("di: decorator for %s must be a function", t))
	}
	ft := pv.Type()
	if ft.NumIn() < 1 || ft.In(0) != t {
		panic(fmt.Sprintf("di: decorator for %s must take %s as its first parameter", t, t))
	}
	hasErr := validateReturn(t, ft, "decorator function")

	paramTypes := make([]reflect.Type, ft.NumIn()-1)
	for i := range paramTypes {
		paramTypes[i] = ft.In(i + 1)
	}
	return decorator{fn: pv, hasErr: hasErr, paramTypes: paramTypes}
}

// validateReturn checks that ft returns (T) or (T, error) for the given service type, panicking
// with a description of what if it doesn't.
func validateReturn(serviceType, ft reflect.Type, what string) (hasErr bool) {
	if ft.NumOut() != 1 && ft.NumOut() != 2 {
		panic(fmt.Sprintf("di: %s for %s must return (T) or (T, error), got %s", what, serviceType, ft))
	}
	if !ft.Out(0).AssignableTo(serviceType) {
		panic(fmt.Sprintf("di: %s return type %s is not assignable to service type %s", what, ft.Out(0), serviceType))
	}
	hasErr = ft.NumOut() == 2
	if hasErr && !ft.Out(1).Implements(errType) {
		panic(fmt.Sprintf("di: %s for %s second return value must be error, got %s", what, serviceType, ft.Out(1)))
	}
	return hasErr
}

func finishCall(out []reflect.Value, hasErr bool) (any, error) {
	if hasErr && !out[1].IsNil() {
		return nil, out[1].Interface().(error)
	}
	return out[0].Interface(), nil
}
