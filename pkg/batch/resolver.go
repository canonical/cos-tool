package batch

import "strings"

// Transformer is the only capability batch mode needs from an expression
// implementation: rewrite one expression, injecting the given matchers.
//
// Implementations are not required to be safe for concurrent use. The existing
// pkg/tool implementations keep per-call state on the receiver, so if batch
// mode ever gains concurrency each worker must hold its own instance.
type Transformer interface {
	Transform(expr string, matchers *map[string]string) (string, error)
}

// Resolver maps a format name to the transformer that implements it.
type Resolver interface {
	// Transformer returns the transformer for a format name, or ok=false for
	// an unrecognised name. Names are matched case-insensitively.
	Transformer(format string) (Transformer, bool)
}

// MapResolver resolves formats from a fixed set of named transformers.
//
// The design called for lazy construction, which is unnecessary: the existing
// implementations are zero-value structs, so holding both costs nothing and
// avoids a mutex. Callers inject the map, which keeps this package independent
// of pkg/tool.
type MapResolver map[string]Transformer

// NewMapResolver returns a resolver over the given transformers, keyed by
// format name.
func NewMapResolver(transformers map[string]Transformer) MapResolver {
	normalized := make(MapResolver, len(transformers))
	for format, transformer := range transformers {
		normalized[strings.ToLower(format)] = transformer
	}
	return normalized
}

// Transformer implements Resolver.
func (m MapResolver) Transformer(format string) (Transformer, bool) {
	transformer, ok := m[strings.ToLower(format)]
	return transformer, ok
}

// Formats returns the recognised format names, for use in diagnostics.
func (m MapResolver) Formats() []string {
	formats := make([]string, 0, len(m))
	for format := range m {
		formats = append(formats, format)
	}
	return formats
}
