package server

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// Authenticator establishes who is calling and returns a context carrying them.
//
// Returning an error rejects the call, so the error must already be a gRPC
// status — Unauthenticated for "we do not know who you are", PermissionDenied
// for "we do, and no". Deciding whether this caller may touch this particular
// order is not this function's job; that rule depends on the aggregate and
// lives in the service that owns it.
type Authenticator func(ctx context.Context, fullMethod string) (context.Context, error)

// MetadataIdentity is the default authenticator: it reads the identity Kong
// verified at the edge and the Composition API forwarded on.
//
// It never rejects a call. Inside the cluster there is no anonymous caller to
// defend against at this layer — every path in reaches through Kong, which
// verifies the JWT, and through the BFF, which verifies it again rather than
// trusting the headers Kong set. A service also serves plenty of legitimately
// user-less traffic: outbox relays, saga timeout workers, and the sync gRPC
// calls one service makes to another on its own behalf. Rejecting those here
// would break the system while protecting nothing.
func MetadataIdentity(ctx context.Context, _ string) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, nil
	}

	id := grpcx.Identity{
		UserID: first(md.Get(grpcx.MetadataUserID)),
		Roles:  roles(md.Get(grpcx.MetadataUserRoles)),
	}
	if id.UserID == "" && len(id.Roles) == 0 {
		return ctx, nil
	}

	return logger.WithUserID(grpcx.IdentityInto(ctx, id), id.UserID), nil
}

func authUnary(fn Authenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := fn(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

func authStream(fn Authenticator) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := fn(stream.Context(), info.FullMethod)
		if err != nil {
			return err
		}

		return handler(srv, withContext(stream, ctx))
	}
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

// roles flattens the two shapes the roles header arrives in. Kong writes one
// comma-separated header; a caller building metadata by hand tends to append a
// key per role. Both are accepted rather than guessing which one is in use.
func roles(values []string) []string {
	var out []string
	for _, value := range values {
		for _, role := range strings.Split(value, ",") {
			if role = strings.TrimSpace(role); role != "" {
				out = append(out, role)
			}
		}
	}

	return out
}
