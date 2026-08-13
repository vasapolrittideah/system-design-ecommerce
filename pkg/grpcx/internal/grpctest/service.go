// Package grpctest provides a gRPC service the grpcx tests can register real
// handlers on.
//
// The interceptor chains in grpcx/server and grpcx/client only behave the way
// they are supposed to when a real call travels through a real connection —
// metadata, status codes, deadlines, and retries all live below the level a
// hand-called interceptor can reach. This package exists so those tests can
// make that call without waiting for a service to define the first .proto.
//
// Method names are fixed rather than free-form because the client's retry
// policy reads them: a Get* method is retried and a Do* method is not, and
// tests need both to exist.
package grpctest

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// ServiceName is the fully qualified name the test service registers under.
const ServiceName = "grpcx.test.v1.TestService"

// The methods this service exposes, as the full paths that appear in
// grpc.UnaryServerInfo, metric labels, and retry decisions.
const (
	MethodGetEcho  = "/" + ServiceName + "/GetEcho"
	MethodGetFlaky = "/" + ServiceName + "/GetFlaky"
	MethodDoWork   = "/" + ServiceName + "/DoWork"
	MethodDoPanic  = "/" + ServiceName + "/DoPanic"
)

var methodNames = []string{"GetEcho", "GetFlaky", "DoWork", "DoPanic"}

// Handler implements one method. A string in and a string out is enough for
// every assertion these tests make, and it avoids needing generated code.
type Handler func(ctx context.Context, in string) (string, error)

// Service is the registrable implementation, backed by whatever handlers the
// test supplies. Keys are bare method names, e.g. "GetEcho".
type Service struct {
	handlers map[string]Handler
}

// New builds a service exposing handlers. A method with no handler answers
// Unimplemented, exactly as an unregistered method would.
func New(handlers map[string]Handler) *Service {
	return &Service{handlers: handlers}
}

// Register attaches the service to a server.
func (s *Service) Register(r grpc.ServiceRegistrar) {
	r.RegisterService(serviceDesc(), s)
}

func (s *Service) call(ctx context.Context, name, in string) (string, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return "", status.Errorf(codes.Unimplemented, "grpctest: no handler for %s", name)
	}

	return handler(ctx, in)
}

// Call invokes method over conn, going through the client interceptor chain the
// same way a generated stub would.
func Call(ctx context.Context, conn *grpc.ClientConn, method, in string, opts ...grpc.CallOption) (string, error) {
	out := new(wrapperspb.StringValue)
	if err := conn.Invoke(ctx, method, wrapperspb.String(in), out, opts...); err != nil {
		return "", err
	}

	return out.GetValue(), nil
}

func serviceDesc() *grpc.ServiceDesc {
	methods := make([]grpc.MethodDesc, 0, len(methodNames))
	for _, name := range methodNames {
		methods = append(methods, grpc.MethodDesc{MethodName: name, Handler: methodHandler(name)})
	}

	return &grpc.ServiceDesc{
		ServiceName: ServiceName,
		// An empty interface accepts any implementation, which is all the
		// registration check needs here.
		HandlerType: (*any)(nil),
		Methods:     methods,
		Metadata:    "grpcx/internal/grpctest",
	}
}

// methodHandler mirrors the shape protoc-gen-go-grpc emits, so the interceptor
// under test sees the same call it would see in a real service.
func methodHandler(name string) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
	return func(
		srv any,
		ctx context.Context,
		dec func(any) error,
		interceptor grpc.UnaryServerInterceptor,
	) (any, error) {
		in := new(wrapperspb.StringValue)
		if err := dec(in); err != nil {
			return nil, err
		}

		svc := srv.(*Service)
		invoke := func(ctx context.Context, req any) (any, error) {
			out, err := svc.call(ctx, name, req.(*wrapperspb.StringValue).GetValue())
			if err != nil {
				return nil, err
			}

			return wrapperspb.String(out), nil
		}

		if interceptor == nil {
			return invoke(ctx, in)
		}

		return interceptor(ctx, in, &grpc.UnaryServerInfo{
			Server:     srv,
			FullMethod: "/" + ServiceName + "/" + name,
		}, invoke)
	}
}
