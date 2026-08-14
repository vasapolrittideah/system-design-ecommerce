package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// writeAndParse runs err through WriteError and reads back what a client would
// actually receive.
func writeAndParse(t *testing.T, err error) (int, httpx.ErrorResponse) {
	t.Helper()

	w := httptest.NewRecorder()
	httpx.WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), err)

	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var body httpx.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}

	return w.Code, body
}

func TestWriteErrorStatusAndCode(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "local not found",
			err:        errorx.New(errorx.KindNotFound, "cart not found"),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
		{
			// The shape the whole design exists for: a service attaches a
			// reason on one side of the wire, the frontend branches on it here.
			name: "downstream conflict keeps its reason",
			err: errorx.ToGRPC(errorx.New(errorx.KindConflict, "sku SKU-1 is sold out").
				WithReason("OUT_OF_STOCK").
				WithMetadata(map[string]string{"sku": "SKU-1"})),
			wantStatus: http.StatusConflict,
			wantCode:   "OUT_OF_STOCK",
		},
		{
			name:       "downstream unavailable",
			err:        status.Error(codes.Unavailable, "inventory is down"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UNAVAILABLE",
		},
		{
			name:       "validation failure",
			err:        &httpx.ValidationError{Fields: []httpx.FieldError{{Field: "sku", Code: "required", Message: "sku is a required field"}}},
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_INPUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, body := writeAndParse(t, tt.err)

			if gotStatus != tt.wantStatus {
				t.Errorf("status = %d, want %d", gotStatus, tt.wantStatus)
			}
			if body.Error.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}
		})
	}
}

func TestWriteErrorCarriesMetadata(t *testing.T) {
	_, body := writeAndParse(t, errorx.New(errorx.KindConflict, "sold out").
		WithReason("OUT_OF_STOCK").
		WithMetadata(map[string]string{"sku": "SKU-1"}))

	if got := body.Error.Metadata["sku"]; got != "SKU-1" {
		t.Errorf("metadata[sku] = %q, want SKU-1", got)
	}
}

func TestWriteErrorNeverLeaksInternals(t *testing.T) {
	secret := `pgx: password authentication failed for user "orders"`

	status, body := writeAndParse(t, errorx.New(errorx.KindInternal, "%s", secret))

	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if strings.Contains(body.Error.Message, "password") {
		t.Errorf("message leaked internals: %q", body.Error.Message)
	}
}

func TestWriteErrorStripsGRPCWrapping(t *testing.T) {
	// A gRPC error's Error() reads "rpc error: code = NotFound desc = ...",
	// which is a Go string about transport, not something to show a client.
	_, body := writeAndParse(t, status.Error(codes.NotFound, "cart not found"))

	if body.Error.Message != "cart not found" {
		t.Errorf("message = %q, want %q", body.Error.Message, "cart not found")
	}
}

func TestWriteErrorPassesThroughProtovalidateFieldViolations(t *testing.T) {
	// protovalidate rejects a request at the service, and the field it names is
	// worth more to the frontend than a bare 400 — even though it was written
	// against a proto and cannot be translated.
	st, err := status.New(codes.InvalidArgument, "validation failed").WithDetails(&errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{
			{Field: "items[0].quantity", Description: "must be greater than 0"},
		},
	})
	if err != nil {
		t.Fatalf("build status: %v", err)
	}

	_, body := writeAndParse(t, st.Err())

	if len(body.Error.Fields) != 1 {
		t.Fatalf("fields = %+v, want one", body.Error.Fields)
	}
	if body.Error.Fields[0].Field != "items[0].quantity" {
		t.Errorf("field = %q, want items[0].quantity", body.Error.Fields[0].Field)
	}
}

func TestWriteErrorCarriesTheCorrelationID(t *testing.T) {
	// The one string a user can quote in a bug report, so it has to survive
	// from the request header into the error body.
	var body httpx.ErrorResponse

	handler := httpx.CorrelationID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, errorx.New(errorx.KindNotFound, "cart not found"))
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("x-correlation-id", "corr-1")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if body.Error.CorrelationID != "corr-1" {
		t.Errorf("correlationId = %q, want corr-1", body.Error.CorrelationID)
	}
	if got := w.Header().Get("x-correlation-id"); got != "corr-1" {
		t.Errorf("echoed header = %q, want corr-1", got)
	}
}

func TestCorrelationIDMintsOneWhenAbsent(t *testing.T) {
	// A request that arrives without one — a health check, a caller that
	// bypassed Kong — still has to be traceable, and the ID has to reach both
	// the context pkg/grpcx/client forwards from and the response.
	var inContext string

	handler := httpx.CorrelationID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inContext = logger.CorrelationID(r.Context())
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	minted := w.Header().Get("x-correlation-id")
	if minted == "" {
		t.Fatalf("no correlation ID was minted")
	}
	if inContext != minted {
		t.Errorf("context ID = %q, echoed ID = %q, want the same", inContext, minted)
	}
}

// TestErrorBodyIsCamelCase pins the wire contract: renaming a key here breaks
// every client, so it is asserted against literal JSON rather than a struct.
func TestErrorBodyIsCamelCase(t *testing.T) {
	w := httptest.NewRecorder()
	httpx.WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil),
		&httpx.ValidationError{Fields: []httpx.FieldError{{Field: "unitPrice", Code: "gt", Message: "must be greater than 0"}}})

	raw := w.Body.String()
	for _, key := range []string{`"error"`, `"code"`, `"message"`, `"fields"`, `"field"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("body %s is missing %s", raw, key)
		}
	}
	if strings.Contains(raw, `"Field"`) || strings.Contains(raw, `"Message"`) {
		t.Errorf("body %s has PascalCase keys", raw)
	}
}
