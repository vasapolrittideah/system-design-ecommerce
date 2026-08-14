package errorx_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
)

func TestToGRPCCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{
			name: "not found",
			err:  errorx.New(errorx.KindNotFound, "order not found"),
			want: codes.NotFound,
		},
		{
			name: "invalid input",
			err:  errorx.New(errorx.KindInvalidInput, "items must not be empty"),
			want: codes.InvalidArgument,
		},
		{
			name: "conflict",
			err:  &domainError{kind: "conflict", msg: "PENDING -> PAID"},
			want: codes.FailedPrecondition,
		},
		{
			name: "unauthorized",
			err:  errorx.New(errorx.KindUnauthorized, "not your order"),
			want: codes.PermissionDenied,
		},
		{
			name: "unclassified",
			err:  errors.New("connection reset by peer"),
			want: codes.Internal,
		},
		{
			// A gateway holds the downstream's status. Flattening it would hide
			// an unhealthy dependency from the caller's circuit breaker, which
			// only counts codes that mean the target itself is in trouble.
			name: "downstream status keeps its code",
			err:  status.Error(codes.Unavailable, "inventory is down"),
			want: codes.Unavailable,
		},
		{
			name: "downstream status keeps its code through wrapping",
			err:  fmt.Errorf("reserve stock: %w", status.Error(codes.ResourceExhausted, "throttled")),
			want: codes.ResourceExhausted,
		},
		{
			// A caller that hung up is not this service being broken, and
			// reporting it as Internal would put client behaviour into the
			// error rate and into every breaker on the path.
			name: "cancelled context",
			err:  fmt.Errorf("query orders: %w", context.Canceled),
			want: codes.Canceled,
		},
		{
			name: "expired context",
			err:  fmt.Errorf("query orders: %w", context.DeadlineExceeded),
			want: codes.DeadlineExceeded,
		},
		{
			// A declared kind is checked before anything else, so a use case
			// that decided a downstream NotFound means InvalidInput to its own
			// caller gets the answer it asked for.
			name: "declared kind beats an inner status",
			err:  errorx.Wrap(status.Error(codes.NotFound, "sku unknown"), errorx.KindInvalidInput, "unknown sku in cart"),
			want: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(errorx.ToGRPC(tt.err)); got != tt.want {
				t.Errorf("status.Code(ToGRPC()) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToGRPCNilIsNil(t *testing.T) {
	if err := errorx.ToGRPC(nil); err != nil {
		t.Errorf("ToGRPC(nil) = %v, want nil", err)
	}
}

func TestToGRPCKeepsClientActionableMessages(t *testing.T) {
	err := errorx.ToGRPC(errorx.New(errorx.KindNotFound, "order o-1 not found"))

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("ToGRPC() did not produce a status: %v", err)
	}
	if got := st.Message(); got != "order o-1 not found" {
		t.Errorf("Message() = %q, want %q", got, "order o-1 not found")
	}
}

func TestToGRPCScrubsInternalOnTheWireOnly(t *testing.T) {
	cause := errors.New("pgx: password authentication failed for user \"orders\"")
	err := errorx.ToGRPC(fmt.Errorf("save order: %w", cause))

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("ToGRPC() did not produce a status: %v", err)
	}
	if got := st.Message(); got != "internal error" {
		t.Errorf("Message() = %q, want %q — internals must not reach the client", got, "internal error")
	}

	// The access line in pkg/grpcx/server logs the error the handler returned,
	// so scrubbing the wire message must not also erase the only record of what
	// broke.
	if got := err.Error(); got != "save order: "+cause.Error() {
		t.Errorf("Error() = %q, want the full chain for the log", got)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true")
	}
}

func TestToGRPCAttachesErrorInfo(t *testing.T) {
	err := errorx.ToGRPC(
		errorx.New(errorx.KindConflict, "sku SKU-1 is sold out").
			WithReason("OUT_OF_STOCK").
			WithMetadata(map[string]string{"sku": "SKU-1"}),
	)

	info := errorInfoOf(t, err)
	if got := info.GetReason(); got != "OUT_OF_STOCK" {
		t.Errorf("Reason = %q, want %q", got, "OUT_OF_STOCK")
	}
	if got := info.GetMetadata()["sku"]; got != "SKU-1" {
		t.Errorf("Metadata[sku] = %q, want %q", got, "SKU-1")
	}
}

func TestToGRPCAttachesDefaultReason(t *testing.T) {
	// Every error leaving a service carries something a client can branch on,
	// so nobody has to pattern-match the message.
	err := errorx.ToGRPC(&domainError{kind: "not_found", msg: "order not found"})

	if got := errorInfoOf(t, err).GetReason(); got != "NOT_FOUND" {
		t.Errorf("Reason = %q, want %q", got, "NOT_FOUND")
	}
}

func TestReasonAndMetadataReadBackFromTheWire(t *testing.T) {
	// What the Composition API actually holds: the status a service returned,
	// received over the wire and re-parsed.
	sent := errorx.ToGRPC(
		errorx.New(errorx.KindConflict, "sold out").
			WithReason("OUT_OF_STOCK").
			WithMetadata(map[string]string{"sku": "SKU-1"}),
	)

	st, ok := status.FromError(sent)
	if !ok {
		t.Fatalf("ToGRPC() did not produce a status: %v", sent)
	}
	received := status.ErrorProto(st.Proto())

	if got := errorx.Reason(received); got != "OUT_OF_STOCK" {
		t.Errorf("Reason() = %q, want %q", got, "OUT_OF_STOCK")
	}
	if got := errorx.Metadata(received)["sku"]; got != "SKU-1" {
		t.Errorf("Metadata()[sku] = %q, want %q", got, "SKU-1")
	}
}

func TestReasonFromStatusWithoutErrorInfo(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			// A tripped breaker answers Unavailable with no detail attached.
			// Naming that INTERNAL would have the BFF answer 503 while
			// reporting an internal error.
			name: "unavailable",
			err:  status.Error(codes.Unavailable, "circuit breaker open for inventory"),
			want: "UNAVAILABLE",
		},
		{
			name: "camel case code becomes snake case reason",
			err:  status.Error(codes.DeadlineExceeded, "too slow"),
			want: "DEADLINE_EXCEEDED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorx.Reason(tt.err); got != tt.want {
				t.Errorf("Reason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReasonFollowsTheOutermostClassification(t *testing.T) {
	// A use case that decided a downstream NotFound means invalid input to its
	// own caller must not keep announcing NOT_FOUND alongside a 400.
	inner := errorx.ToGRPC(errorx.New(errorx.KindNotFound, "sku unknown"))
	err := errorx.Wrap(inner, errorx.KindInvalidInput, "unknown sku in cart")

	if got := errorx.Reason(err); got != "INVALID_INPUT" {
		t.Errorf("Reason() = %q, want %q", got, "INVALID_INPUT")
	}
	if got := errorInfoOf(t, errorx.ToGRPC(err)).GetReason(); got != "INVALID_INPUT" {
		t.Errorf("ErrorInfo reason = %q, want %q", got, "INVALID_INPUT")
	}
}

func TestHTTPStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "nil",
			err:  nil,
			want: http.StatusOK,
		},
		{
			name: "kind is mapped without a round trip",
			err:  errorx.New(errorx.KindNotFound, "order not found"),
			want: http.StatusNotFound,
		},
		{
			name: "conflict",
			err:  &domainError{kind: "conflict", msg: "sold out"},
			want: http.StatusConflict,
		},
		{
			name: "unauthorized",
			err:  errorx.New(errorx.KindUnauthorized, "not your order"),
			want: http.StatusForbidden,
		},
		{
			name: "downstream status",
			err:  status.Error(codes.NotFound, "order not found"),
			want: http.StatusNotFound,
		},
		{
			// The BFF answers Kong, and a dependency that is down is a 503,
			// not a bug in the BFF.
			name: "unavailable downstream",
			err:  status.Error(codes.Unavailable, "inventory is down"),
			want: http.StatusServiceUnavailable,
		},
		{
			name: "timed out downstream",
			err:  status.Error(codes.DeadlineExceeded, "too slow"),
			want: http.StatusGatewayTimeout,
		},
		{
			name: "edge rejected the token",
			err:  status.Error(codes.Unauthenticated, "invalid token"),
			want: http.StatusUnauthorized,
		},
		{
			name: "unclassified",
			err:  errors.New("boom"),
			want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorx.HTTPStatus(tt.err); got != tt.want {
				t.Errorf("HTTPStatus() = %d, want %d", got, tt.want)
			}
		})
	}
}

func errorInfoOf(t *testing.T, err error) *errdetails.ErrorInfo {
	t.Helper()

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("ToGRPC() did not produce a status: %v", err)
	}

	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info
		}
	}

	t.Fatalf("status carries no ErrorInfo detail: %v", st.Details())

	return nil
}
