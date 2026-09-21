package ioc

// ServiceCollectionExtension is the convention for building reusable, chainable groups of
// registrations, mirroring .NET's IServiceCollection extension methods (e.g. AddHttpClient()).
// Third-party packages implement plain functions of this shape using only ServiceCollection's
// public methods; no framework-internal hooks are required.
type ServiceCollectionExtension func(sc *ServiceCollection) *ServiceCollection

// Extend applies ext to sc and returns the (possibly same) *ServiceCollection, enabling
// sc.AddSingleton[...](...).Extend(mypkg.AddHttpClient).AddSingleton[...](...) style chaining.
func (sc *ServiceCollection) Extend(ext ServiceCollectionExtension) *ServiceCollection {
	return ext(sc)
}
