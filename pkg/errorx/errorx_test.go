package errorx_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
)

// domainError stands in for the error type a service declares in its own
// internal/domain package. It imports nothing, which is the whole point of the
// Kinder contract: a domain package classifies its failures without depending
// on this one.
type domainError struct {
	kind string
	msg  string
}

func (e *domainError) Error() string     { return e.msg }
func (e *domainError) ErrorKind() string { return e.kind }

func TestKindOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want errorx.Kind
	}{
		{
			name: "nil has no kind",
			err:  nil,
			want: "",
		},
		{
			name: "error built here",
			err:  errorx.New(errorx.KindNotFound, "order not found"),
			want: errorx.KindNotFound,
		},
		{
			name: "domain error declaring its kind structurally",
			err:  &domainError{kind: "conflict", msg: "out of stock"},
			want: errorx.KindConflict,
		},
		{
			name: "kind survives fmt wrapping",
			err:  fmt.Errorf("reserve stock: %w", &domainError{kind: "conflict", msg: "out of stock"}),
			want: errorx.KindConflict,
		},
		{
			// A repository wrapping a driver failure is the common shape, and
			// the classification has to reach through it.
			name: "kind survives Wrap",
			err:  errorx.Wrap(errors.New("no rows in result set"), errorx.KindNotFound, "find order"),
			want: errorx.KindNotFound,
		},
		{
			name: "unclassified error is internal",
			err:  errors.New("connection reset by peer"),
			want: errorx.KindInternal,
		},
		{
			// Fail closed: a typo in a domain package must not become a 200-ish
			// answer, it must become the bug it is.
			name: "unrecognised kind string is internal",
			err:  &domainError{kind: "not-found", msg: "typo"},
			want: errorx.KindInternal,
		},
		{
			// The outermost declaration wins, so a use case can reclassify what
			// a lower layer said without the lower layer's kind leaking past it.
			name: "outermost declaration wins",
			err:  errorx.Wrap(errorx.New(errorx.KindNotFound, "inner"), errorx.KindInvalidInput, "outer"),
			want: errorx.KindInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorx.KindOf(tt.err); got != tt.want {
				t.Errorf("KindOf() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorsIsMatchesSentinelsByKind(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		sentinel error
		want     bool
	}{
		{
			name:     "same kind",
			err:      errorx.New(errorx.KindNotFound, "order not found"),
			sentinel: errorx.ErrNotFound,
			want:     true,
		},
		{
			name:     "different kind",
			err:      errorx.New(errorx.KindConflict, "already paid"),
			sentinel: errorx.ErrNotFound,
			want:     false,
		},
		{
			name:     "wrapped",
			err:      fmt.Errorf("load order: %w", errorx.New(errorx.KindUnauthorized, "not yours")),
			sentinel: errorx.ErrUnauthorized,
			want:     true,
		},
		{
			// The two refusals are near-identical in name and adjacent in every
			// table in this package, which is exactly how one ends up mapped to
			// the other's code.
			name:     "unauthenticated is not unauthorized",
			err:      errorx.New(errorx.KindUnauthenticated, "token expired"),
			sentinel: errorx.ErrUnauthorized,
			want:     false,
		},
		{
			// A domain error is matched by kind through KindOf, not by errors.Is
			// against a sentinel it has never heard of.
			name:     "domain error does not match a sentinel it cannot reference",
			err:      &domainError{kind: "not_found", msg: "order not found"},
			sentinel: errorx.ErrNotFound,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Is(tt.err, tt.sentinel); got != tt.want {
				t.Errorf("errors.Is() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWrapKeepsCauseReachable(t *testing.T) {
	cause := errors.New("no rows in result set")
	err := errorx.Wrap(cause, errorx.KindNotFound, "find order %s", "o-1")

	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true")
	}

	const want = "find order o-1: no rows in result set"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestWrapNilIsNil(t *testing.T) {
	// Call sites hand Wrap the result of the operation they are describing, so
	// returning a non-nil *Error here would turn every success into a failure
	// the moment it is assigned to an error variable.
	if err := errorx.Wrap(nil, errorx.KindInternal, "save order"); err != nil {
		t.Errorf("Wrap(nil, ...) = %v, want nil", err)
	}
}

func TestReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil",
			err:  nil,
			want: "",
		},
		{
			name: "explicit reason",
			err:  errorx.New(errorx.KindConflict, "sold out").WithReason("OUT_OF_STOCK"),
			want: "OUT_OF_STOCK",
		},
		{
			name: "defaults to the kind",
			err:  errorx.New(errorx.KindConflict, "sold out"),
			want: "CONFLICT",
		},
		{
			name: "unclassified error defaults to internal",
			err:  errors.New("boom"),
			want: "INTERNAL",
		},
		{
			name: "domain error defaults to its kind",
			err:  &domainError{kind: "not_found", msg: "order not found"},
			want: "NOT_FOUND",
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

func TestWithMetadataMerges(t *testing.T) {
	err := errorx.New(errorx.KindConflict, "sold out").
		WithMetadata(map[string]string{"sku": "SKU-1"}).
		WithMetadata(map[string]string{"requested": "5"})

	want := map[string]string{"sku": "SKU-1", "requested": "5"}

	got := errorx.Metadata(err)
	if len(got) != len(want) {
		t.Fatalf("Metadata() = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("Metadata()[%q] = %q, want %q", key, got[key], value)
		}
	}
}

func TestWithersDoNotMutateTheReceiver(t *testing.T) {
	// The sentinels are package-level and shared by every service in the
	// process. If a wither wrote through, the first caller to attach an
	// OUT_OF_STOCK reason would attach it to everyone else's conflicts too.
	tagged := errorx.ErrConflict.
		WithReason("OUT_OF_STOCK").
		WithMetadata(map[string]string{"sku": "SKU-1"})

	if got := errorx.Reason(errorx.ErrConflict); got != "CONFLICT" {
		t.Errorf("sentinel reason = %q, want %q", got, "CONFLICT")
	}
	if got := errorx.Metadata(errorx.ErrConflict); got != nil {
		t.Errorf("sentinel metadata = %v, want nil", got)
	}
	if got := errorx.Reason(tagged); got != "OUT_OF_STOCK" {
		t.Errorf("copy reason = %q, want %q", got, "OUT_OF_STOCK")
	}
}

func TestMetadataIsNotAliased(t *testing.T) {
	err := errorx.New(errorx.KindConflict, "sold out").
		WithMetadata(map[string]string{"sku": "SKU-1"})

	errorx.Metadata(err)["sku"] = "TAMPERED"

	if got := errorx.Metadata(err)["sku"]; got != "SKU-1" {
		t.Errorf("metadata was mutated through the returned map: got %q", got)
	}
}
