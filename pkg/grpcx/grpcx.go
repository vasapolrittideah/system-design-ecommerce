// Package grpcx holds the gRPC wiring shared by every service in this repo.
//
// This root package owns only what both ends of a call have to agree on: the
// metadata keys, and the identity type read back off the context. Each side is
// built by a sub-package:
//
//   - grpcx/server builds the *grpc.Server, with the interceptor chain, keepalive
//     policy, health service, and graceful shutdown already attached.
//   - grpcx/client builds the *grpc.ClientConn, with deadlines, retries, a
//     circuit breaker, and round-robin balancing already attached.
package grpcx

import "context"

// The metadata an east-west call carries besides its request. Both ends read
// these names, so they live here rather than in either sub-package.
//
// Lowercase because gRPC lowercases metadata keys on the wire, which is what
// metadata.MD.Get expects.
const (
	// MetadataCorrelationID ties one user-visible operation together across
	// every hop and every event it causes. pkg/httpx mints it at the edge.
	MetadataCorrelationID = "x-correlation-id"

	// MetadataUserID and MetadataUserRoles carry the identity settled at the
	// edge, so a service several hops in can tell whose request it is serving.
	// Roles travel comma-separated.
	MetadataUserID    = "x-user-id"
	MetadataUserRoles = "x-user-roles"
)

// Identity is the caller a request is being served on behalf of.
//
// It answers "who is asking", never "are they allowed": authorization belongs to
// the service that owns the data being touched.
type Identity struct {
	// UserID is the authenticated subject, empty for an unauthenticated or
	// service-to-service call made outside any user request.
	UserID string

	// Roles are the caller's roles as the edge saw them.
	Roles []string
}

type contextKey int

const identityKey contextKey = iota

// IdentityInto stores id in ctx, which pkg/auth does at the edge after verifying
// a token and pkg/grpcx/server does on each hop after reading the forwarded
// metadata. Handlers and use cases read it back with IdentityFrom.
func IdentityInto(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// IdentityFrom returns the identity in ctx and whether one was set.
//
// The false case is a real state, not an error: outbox relays, Kafka consumers,
// and cron jobs run without a user behind them. Code that requires a user must
// check, rather than read the zero value as "anonymous but allowed".
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey).(Identity)

	return id, ok
}
