package argus

import "context"

// Evaluate runs one pass of the rules engine without waiting for the
// ticker. Exposed for tests.
func (a *Argus) Evaluate(ctx context.Context) { a.evaluate(ctx) }
