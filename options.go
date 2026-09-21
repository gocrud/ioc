package ioc

import (
	"reflect"
	"sync"
	"sync/atomic"
)

// Options wraps a configured value of type T, analogous to .NET's IOptions[T].
type Options[T any] struct {
	Value T
}

// Configure registers a configuration callback for T. Multiple Configure calls for the same T
// are applied in registration order against a zero-value T, then exposed as a Singleton
// *Options[T]. Configure must be called before Build.
func (sc *ServiceCollection) Configure[T any](configure func(*T)) *ServiceCollection {
	t := reflect.TypeFor[T]()
	sc.optionConfigs[t] = append(sc.optionConfigs[t], func(v any) { configure(v.(*T)) })

	sc.TryAddSingleton[*Options[T]](func() (*Options[T], error) {
		var value T
		for _, fn := range sc.optionConfigs[t] {
			fn(&value)
		}
		return &Options[T]{Value: value}, nil
	})
	return sc
}

// OptionsMonitor holds a live value of T that can be updated at runtime via Set, analogous to
// .NET's IOptionsMonitor[T]. Unlike Options[T], the framework does not know how or when the
// value should change: callers must invoke Set themselves (e.g. from a config-file watcher).
type OptionsMonitor[T any] struct {
	value     atomic.Pointer[T]
	mu        sync.Mutex // guards listeners only, never held while invoking them
	listeners map[int]func(T)
	nextID    int
}

// CurrentValue returns the most recently set value. Lock-free read.
func (m *OptionsMonitor[T]) CurrentValue() T {
	return *m.value.Load()
}

// OnChange registers a listener invoked after every Set call. The returned func unsubscribes it.
func (m *OptionsMonitor[T]) OnChange(listener func(T)) (unsubscribe func()) {
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.listeners[id] = listener
	m.mu.Unlock()

	return func() {
		m.mu.Lock()
		delete(m.listeners, id)
		m.mu.Unlock()
	}
}

// Set stores a new value and notifies all current listeners with it.
func (m *OptionsMonitor[T]) Set(newValue T) {
	m.value.Store(&newValue)

	m.mu.Lock()
	snapshot := make([]func(T), 0, len(m.listeners))
	for _, l := range m.listeners {
		snapshot = append(snapshot, l)
	}
	m.mu.Unlock()

	for _, l := range snapshot {
		l(newValue)
	}
}

// ConfigureMonitor registers a configuration callback for T, exposed as a Singleton
// *OptionsMonitor[T]. Multiple ConfigureMonitor calls for the same T are applied in registration
// order against a zero-value T to compute the initial value. ConfigureMonitor must be called
// before Build. Runtime updates must be pushed via (*OptionsMonitor[T]).Set.
func (sc *ServiceCollection) ConfigureMonitor[T any](configure func(*T)) *ServiceCollection {
	t := reflect.TypeFor[T]()
	sc.monitorConfigs[t] = append(sc.monitorConfigs[t], func(v any) { configure(v.(*T)) })

	sc.TryAddSingleton[*OptionsMonitor[T]](func() (*OptionsMonitor[T], error) {
		var value T
		for _, fn := range sc.monitorConfigs[t] {
			fn(&value)
		}
		m := &OptionsMonitor[T]{listeners: make(map[int]func(T))}
		m.value.Store(&value)
		return m, nil
	})
	return sc
}
