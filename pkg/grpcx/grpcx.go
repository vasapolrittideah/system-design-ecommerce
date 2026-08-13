// Package grpcx holds the gRPC wiring shared by every service in this repo.
//
// The name carries an "x" because a package called "grpc" would collide with
// google.golang.org/grpc in every file that needs both — which is every adapter
// there is.
//
// This root package owns only what both ends of a call have to agree on: the
// metadata keys identity travels under, and the context type it is read back
// out of. The two sub-packages own a side each:
//
//   - grpcx/server builds the *grpc.Server, with the interceptor chain, keepalive
//     policy, health service, and graceful shutdown already attached.
//   - grpcx/client builds the *grpc.ClientConn, with deadlines, retries, a
//     circuit breaker, and round-robin balancing already attached.
//
// Neither knows anything about orders, payments, or stock. Business rules live
// in a service's domain package; this is transport.
package grpcx

import "context"

// Metadata keys for the caller identity that travels with an east-west call.
//
// Kong verifies the JWT at the edge and forwards its claims as X-User-ID and
// X-User-Roles. From there the Composition API — which re-verifies the token
// itself rather than trusting those headers — passes the identity down over
// gRPC using these same keys, so a service several hops in can still tell whose
// request it is serving.
//
// gRPC lowercases metadata keys on the wire, so these are written lowercase to
// match what metadata.MD.Get expects.
const (
	MetadataCorrelationID = "x-correlation-id"
	MetadataUserID        = "x-user-id"
	MetadataUserRoles     = "x-user-roles"
)

// Identity is the caller a request is being served on behalf of.
//
// It answers "who is asking", never "are they allowed". Authorization is a
// business rule and belongs to the service that owns the data being touched —
// the gateway does not do it, and neither does this package.
type Identity struct {
	// UserID is the authenticated subject, empty for an unauthenticated or
	// service-to-service call made outside any user request.
	UserID string

	// Roles are the caller's roles as the edge saw them.
	Roles []string
}

type contextKey int

const identityKey contextKey = iota

// IdentityInto stores id in ctx. The server's auth interceptor calls this once
// per request; handlers and use cases read it back with IdentityFrom.
func IdentityInto(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// IdentityFrom returns the identity in ctx and whether one was set.
//
// The false case is a real state, not an error: outbox relays, Kafka consumers,
// and cron jobs all run without a user behind them. Code that requires a user
// must check, rather than assume the zero value means "anonymous but allowed".
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey).(Identity)

	return id, ok
}
