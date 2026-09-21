package ioc

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Sentinel errors returned (possibly wrapped) by ServiceCollection.Build / ServiceProvider resolution.
var (
	ErrServiceNotRegistered = errors.New("di: service not registered")
	ErrCircularDependency   = errors.New("di: circular dependency detected")
)

// CircularDependencyError carries the full dependency chain that formed a cycle.
type CircularDependencyError struct {
	Chain []reflect.Type
}

func (e *CircularDependencyError) Error() string {
	names := make([]string, len(e.Chain))
	for i, t := range e.Chain {
		names[i] = t.String()
	}
	return fmt.Sprintf("%s: %s", ErrCircularDependency, strings.Join(names, " -> "))
}

func (e *CircularDependencyError) Unwrap() error {
	return ErrCircularDependency
}
