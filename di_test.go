package ioc

import (
	"errors"
	"fmt"
	"testing"
)

type Logger interface {
	Log(msg string)
}

type consoleLogger struct{ prefix string }

func (c *consoleLogger) Log(msg string) {}

func NewConsoleLogger() *consoleLogger { return &consoleLogger{prefix: "console"} }

type Repository struct {
	Logger Logger
}

func NewRepository(logger Logger) *Repository { return &Repository{Logger: logger} }

type Validator interface{ Validate() string }
type emailValidator struct{}

func (emailValidator) Validate() string { return "email" }

type phoneValidator struct{}

func (phoneValidator) Validate() string { return "phone" }

func TestAutoWiring(t *testing.T) {
	sc := NewServiceCollection()
	sc.AddSingleton[Logger](NewConsoleLogger).
		AddSingleton[*Repository](NewRepository)

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}

	l1 := provider.MustResolve[Logger]()
	l2 := provider.MustResolve[Logger]()
	if l1 != l2 {
		t.Fatal("expected singleton logger to be the same instance")
	}

	r1 := provider.MustResolve[*Repository]()
	r2 := provider.MustResolve[*Repository]()
	if r1 != r2 {
		t.Fatal("expected singleton repository to be the same instance")
	}
	if r1.Logger != l1 {
		t.Fatal("expected auto-wired ctor to receive the singleton logger")
	}
}

func TestResolveAll(t *testing.T) {
	sc := NewServiceCollection()
	sc.AddSingleton[Validator](func() Validator { return emailValidator{} })
	sc.AddSingleton[Validator](func() Validator { return phoneValidator{} })

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}
	all, err := provider.ResolveAll[Validator]()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Validate() != "email" || all[1].Validate() != "phone" {
		t.Fatalf("unexpected ResolveAll result: %#v", all)
	}

	last := provider.MustResolve[Validator]()
	if last.Validate() != "phone" {
		t.Fatalf("expected Resolve to return the last registration, got %s", last.Validate())
	}
}

func TestTryAdd(t *testing.T) {
	sc := NewServiceCollection()
	sc.AddSingleton[Logger](NewConsoleLogger)
	sc.TryAddSingleton[Logger](func() Logger { return &consoleLogger{prefix: "should-not-register"} })

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}
	logger := provider.MustResolve[Logger]().(*consoleLogger)
	if logger.prefix != "console" {
		t.Fatalf("expected TryAdd to skip, got prefix %q", logger.prefix)
	}
}

type AppConfig struct {
	Port int
}

func TestOptions(t *testing.T) {
	sc := NewServiceCollection()
	sc.Configure(func(c *AppConfig) { c.Port = 8080 })
	sc.Configure(func(c *AppConfig) { c.Port += 1 })

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}
	opts := provider.MustResolve[*Options[AppConfig]]()
	if opts.Value.Port != 8081 {
		t.Fatalf("expected merged config port 8081, got %d", opts.Value.Port)
	}
}

func TestOptionsMonitor(t *testing.T) {
	sc := NewServiceCollection()
	sc.ConfigureMonitor(func(c *AppConfig) { c.Port = 8080 })

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}
	monitor := provider.MustResolve[*OptionsMonitor[AppConfig]]()
	if monitor.CurrentValue().Port != 8080 {
		t.Fatalf("expected initial port 8080, got %d", monitor.CurrentValue().Port)
	}

	var got AppConfig
	unsubscribe := monitor.OnChange(func(c AppConfig) { got = c })

	monitor.Set(AppConfig{Port: 9090})
	if monitor.CurrentValue().Port != 9090 {
		t.Fatalf("expected current value updated to 9090, got %d", monitor.CurrentValue().Port)
	}
	if got.Port != 9090 {
		t.Fatalf("expected listener to observe new value 9090, got %d", got.Port)
	}

	unsubscribe()
	monitor.Set(AppConfig{Port: 1234})
	if got.Port != 9090 {
		t.Fatalf("expected unsubscribed listener not to be notified, still got %d", got.Port)
	}
	if monitor.CurrentValue().Port != 1234 {
		t.Fatalf("expected current value updated to 1234, got %d", monitor.CurrentValue().Port)
	}
}

type a struct{ B *b }
type b struct{ A *a }

func TestCircularDependency(t *testing.T) {
	sc := NewServiceCollection()
	sc.AddSingleton[*a](func(bb *b) *a { return &a{B: bb} })
	sc.AddSingleton[*b](func(aa *a) *b { return &b{A: aa} })

	_, err := sc.Build()
	var cycleErr *CircularDependencyError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected CircularDependencyError from Build, got %v", err)
	}
}

func TestMissingDependency(t *testing.T) {
	sc := NewServiceCollection()
	sc.AddSingleton[*Repository](NewRepository) // Logger not registered

	if _, err := sc.Build(); !errors.Is(err, ErrServiceNotRegistered) {
		t.Fatalf("expected Build to surface missing dependency error, got %v", err)
	}
}

func TestDecorate(t *testing.T) {
	sc := NewServiceCollection()
	sc.AddSingleton[Logger](NewConsoleLogger)
	sc.Decorate[Logger](func(inner Logger) (Logger, error) {
		return &consoleLogger{prefix: "decorated-" + inner.(*consoleLogger).prefix}, nil
	})

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}
	logger := provider.MustResolve[Logger]().(*consoleLogger)
	if logger.prefix != "decorated-console" {
		t.Fatalf("expected decorated prefix, got %q", logger.prefix)
	}
}

func TestDecorateWithExtraDependency(t *testing.T) {
	sc := NewServiceCollection()
	sc.AddSingleton[Logger](NewConsoleLogger)
	sc.AddSingleton[*AppConfig](func() *AppConfig { return &AppConfig{Port: 9090} })
	sc.Decorate[Logger](func(inner Logger, cfg *AppConfig) (Logger, error) {
		return &consoleLogger{prefix: fmt.Sprintf("%s-%d", inner.(*consoleLogger).prefix, cfg.Port)}, nil
	})

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}
	logger := provider.MustResolve[Logger]().(*consoleLogger)
	if logger.prefix != "console-9090" {
		t.Fatalf("expected decorated prefix using extra dependency, got %q", logger.prefix)
	}
}

func addHttpLoggerExtension(sc *ServiceCollection) *ServiceCollection {
	sc.TryAddSingleton[Logger](NewConsoleLogger)
	return sc
}

func TestExtend(t *testing.T) {
	sc := NewServiceCollection()
	sc.Extend(addHttpLoggerExtension).
		AddSingleton[*Repository](NewRepository)

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}
	if provider.MustResolve[Logger]() == nil {
		t.Fatal("expected logger registered via extension")
	}
}

// closeRecorder appends its name to a shared slice when closed, so tests can assert close order.
type closeRecorder struct {
	name  string
	order *[]string
}

func (c *closeRecorder) Close() error {
	*c.order = append(*c.order, c.name)
	return nil
}

type closerB struct{ *closeRecorder }
type closerA struct {
	*closeRecorder
	B *closerB
}

func TestProviderClose(t *testing.T) {
	var order []string
	sc := NewServiceCollection()
	sc.AddSingleton[*closerB](func() *closerB {
		return &closerB{closeRecorder: &closeRecorder{name: "B", order: &order}}
	})
	sc.AddSingleton[*closerA](func(bb *closerB) *closerA {
		return &closerA{closeRecorder: &closeRecorder{name: "A", order: &order}, B: bb}
	})

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}

	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "A" || order[1] != "B" {
		t.Fatalf("expected close order [A B] (reverse of construction order), got %v", order)
	}

	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 {
		t.Fatalf("expected second Close call to be a no-op, got %v", order)
	}
}

func TestKeyedServices(t *testing.T) {
	sc := NewServiceCollection()
	sc.AddKeyedSingleton[Validator]("email", func() Validator { return emailValidator{} })
	sc.AddKeyedSingleton[Validator]("phone", func() Validator { return phoneValidator{} })
	sc.TryAddKeyedSingleton[Validator]("email", func() Validator { return phoneValidator{} }) // should be skipped

	provider, err := sc.Build()
	if err != nil {
		t.Fatal(err)
	}

	email := provider.MustResolveKeyed[Validator]("email")
	if email.Validate() != "email" {
		t.Fatalf("expected keyed 'email' validator, got %q", email.Validate())
	}
	phone, err := provider.ResolveKeyed[Validator]("phone")
	if err != nil || phone.Validate() != "phone" {
		t.Fatalf("expected keyed 'phone' validator, got %v, %v", phone, err)
	}

	if _, err := provider.ResolveKeyed[Validator]("missing"); !errors.Is(err, ErrServiceNotRegistered) {
		t.Fatalf("expected ErrServiceNotRegistered for unknown key, got %v", err)
	}

	// Keyed registrations must not leak into the unkeyed resolution space.
	if _, err := provider.Resolve[Validator](); !errors.Is(err, ErrServiceNotRegistered) {
		t.Fatalf("expected keyed registrations to be invisible to unkeyed Resolve, got %v", err)
	}
}
