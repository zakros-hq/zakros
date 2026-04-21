// Package jwt handles pod-to-broker authentication.
//
// Phase 1 uses a bearer token minted at pod spawn and verified by a
// shared-secret check on the receiving broker — sufficient because every
// broker and pod runs inside Crete on a trusted Proxmox virtual bridge
// (architecture.md §6 MCP Broker Authentication, Phase 1 posture).
//
// Phase 2 switches to Minos-signed Ed25519 JWTs with per-scope claims; the
// Claims type defined here is already the Phase 2 shape so Phase 1 bearer
// tokens can carry it as an opaque payload and Phase 2 swap is a signature
// change, not a claim-shape change.
package jwt

import "time"

// Claims is the Phase 2 JWT body shape, usable in Phase 1 as an opaque
// per-pod fact Minos stamps and brokers consult on arrival.
type Claims struct {
	Subject  string    `json:"sub"`
	Issuer   string    `json:"iss"`
	Audience []string  `json:"aud"`
	IssuedAt time.Time `json:"iat"`
	Expires  time.Time `json:"exp"`
	JTI      string    `json:"jti"`
	// McpScopes maps broker name to the allowed operation strings for this
	// pod on that broker. Keys match audience entries.
	McpScopes map[string][]string `json:"mcp_scopes"`
}

// HasScope reports whether the caller is permitted to invoke op on broker.
// Used by broker-side middleware before dispatching to the handler.
func (c *Claims) HasScope(broker, op string) bool {
	if c == nil {
		return false
	}
	for _, s := range c.McpScopes[broker] {
		if s == op {
			return true
		}
	}
	return false
}
