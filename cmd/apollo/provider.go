package main

import (
	"context"
	"net/http"
	"strings"
)

// Provider is the in-process plugin interface every upstream LLM
// provider implements. Phase 2 H2a registers the Anthropic provider
// at startup; future providers (OpenAI, Google, OCP-fronted Claude)
// register the same way. The §2 D4 subprocess split lifts this
// interface across an HTTP/RPC boundary without changing call sites.
type Provider interface {
	// Name is the canonical short name used in JWT scopes
	// (`apollo.<name>.<model>`) and audit fields.
	Name() string

	// Models reports which model identifiers this provider claims.
	// Apollo's registry resolves a request's model field through this
	// list; the first provider claiming a match wins.
	Models() []string

	// Forward sends the (already-validated) request body to the
	// upstream API and returns the response. Implementations SHOULD
	// pass the upstream's response headers through unmodified so
	// callers get the same observability the bare API provides.
	Forward(ctx context.Context, body []byte) (*ProviderResponse, error)
}

// ProviderResponse is the value type Forward returns. Status,
// Headers, and Body mirror http.Response; TokensIn/TokensOut are
// extracted by the provider so the per-call audit can record them
// without re-parsing the body in server.go.
type ProviderResponse struct {
	Status    int
	Headers   http.Header
	Body      []byte
	TokensIn  int
	TokensOut int
}

// providerRegistry resolves model-name → Provider. Order of
// registration matters only when two providers claim the same model
// (which the H2a Anthropic-only deploy avoids by construction).
type providerRegistry struct {
	all []Provider
	// idx is a fast-path map from model name to provider; rebuilt
	// after every register call.
	idx map[string]Provider
}

func newProviderRegistry() *providerRegistry {
	return &providerRegistry{idx: map[string]Provider{}}
}

func (r *providerRegistry) register(p Provider) {
	r.all = append(r.all, p)
	for _, m := range p.Models() {
		r.idx[strings.ToLower(m)] = p
	}
}

func (r *providerRegistry) providerFor(model string) Provider {
	return r.idx[strings.ToLower(model)]
}
