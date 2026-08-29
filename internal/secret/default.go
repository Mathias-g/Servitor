package secret

// DefaultRegistry returns the providers compiled into this build: the plain
// environment fallback (env), the varlock pull source, and the recommended
// push-based on-box ciphertext store (onbox). Adding more providers is additive
// here, one per actual need (ADR-0002).
func DefaultRegistry() *Registry {
	reg := NewRegistry()
	reg.Register(envSource, EnvProvider{})
	reg.Register(varlockSource, &VarlockProvider{})
	reg.Register(onboxSource, NewOnBoxProvider(""))
	return reg
}
