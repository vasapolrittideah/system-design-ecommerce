package server

import (
	"context"
	"errors"

	"buf.build/go/protovalidate"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// validateUnary enforces the rules declared on the request message itself.
//
// Validation is a property of the contract, so it is written in the .proto with
// protovalidate options and enforced here for every method at once. Hand-written
// checks in handlers are the thing this replaces: they drift from the proto,
// they differ between two handlers that accept the same message, and they push
// the contract out of the file that is supposed to be the source of truth.
func validateUnary(v protovalidate.Validator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := validate(v, req); err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// validateStream validates each message as it is received, since a stream has
// no single request to check up front.
func validateStream(v protovalidate.Validator) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, &validatingStream{ServerStream: stream, validator: v})
	}
}

type validatingStream struct {
	grpc.ServerStream
	validator protovalidate.Validator
}

func (s *validatingStream) RecvMsg(msg any) error {
	if err := s.ServerStream.RecvMsg(msg); err != nil {
		return err
	}

	return validate(s.validator, msg)
}

func validate(v protovalidate.Validator, req any) error {
	msg, ok := req.(proto.Message)
	if !ok {
		return nil
	}

	err := v.Validate(msg)
	if err == nil {
		return nil
	}

	// A CompilationError or RuntimeError means the rules in our own proto are
	// wrong or uncompilable. That is our bug, not the caller's, so it must not
	// come back as InvalidArgument and send them off debugging a request that
	// was fine.
	var invalid *protovalidate.ValidationError
	if !errors.As(err, &invalid) {
		return status.Error(codes.Internal, "internal error")
	}

	return violationStatus(invalid).Err()
}

// violationStatus renders the violations as a BadRequest detail, the standard
// Google shape for field-level errors, so a client can point at the offending
// field instead of parsing a sentence.
func violationStatus(invalid *protovalidate.ValidationError) *status.Status {
	st := status.New(codes.InvalidArgument, "invalid request")

	violations := make([]*errdetails.BadRequest_FieldViolation, 0, len(invalid.Violations))
	for _, v := range invalid.Violations {
		violations = append(violations, &errdetails.BadRequest_FieldViolation{
			Field:       protovalidate.FieldPathString(v.Proto.GetField()),
			Description: v.Proto.GetMessage(),
			Reason:      v.Proto.GetRuleId(),
		})
	}

	// Attaching details can only fail if they do not marshal, and these always
	// do. The plain status is still a correct answer, so a failure here is not
	// worth turning a bad request into an internal error.
	detailed, err := st.WithDetails(&errdetails.BadRequest{FieldViolations: violations})
	if err != nil {
		return st
	}

	return detailed
}
