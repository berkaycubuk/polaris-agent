package session

import "context"

type ctxKey struct{}

// WithID returns ctx tagged with the active session ID. Tools read this
// when they need to know which session originated a tool call (e.g.
// manage_schedule captures the origin from here).
func WithID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, id)
}

// IDFrom returns the session ID stored in ctx, or "" if absent.
func IDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}
