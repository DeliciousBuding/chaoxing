// Package authz carries OAuth scopes from the transport middleware into tool
// adapters without exposing token state or making adapters depend on OAuth.
package authz

import "context"

type scopeKey struct{}
type principalKey struct{}

// WithScopes returns a request context carrying a space-separated OAuth scope
// string. An empty value means no scope is granted.
func WithScopes(ctx context.Context, scopes string) context.Context {
	return context.WithValue(ctx, scopeKey{}, scopes)
}

// Has reports whether the requested scope is present in the request context.
func Has(ctx context.Context, scope string) bool {
	if scope == "" {
		return true
	}
	scopes, _ := ctx.Value(scopeKey{}).(string)
	for _, item := range splitScopes(scopes) {
		if item == scope {
			return true
		}
	}
	return false
}

// WithPrincipal records the authenticated transport identity for audit
// attribution. It is not an authorization decision and carries no secret.
func WithPrincipal(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// Principal returns the transport identity recorded by the authentication
// middleware, or an empty string when the caller is unauthenticated.
func Principal(ctx context.Context) string {
	value, _ := ctx.Value(principalKey{}).(string)
	return value
}

func splitScopes(scopes string) []string {
	var out []string
	start := -1
	for i, r := range scopes {
		if r == ' ' {
			if start >= 0 {
				out = append(out, scopes[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, scopes[start:])
	}
	return out
}
