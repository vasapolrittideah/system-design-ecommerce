package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

// addCartItemRequest is a BFF request DTO shaped the way this package expects
// them: camelCase json tags, rules in validate tags, no proto in sight.
type addCartItemRequest struct {
	SKU       string    `json:"sku"       validate:"required"`
	Quantity  int       `json:"quantity"  validate:"required,gte=1,lte=99"`
	UnitPrice float64   `json:"unitPrice" validate:"required,gt=0"`
	Options   []option  `json:"options"   validate:"dive"`
	Gift      *giftNote `json:"gift"`
}

type option struct {
	Name string `json:"name" validate:"required"`
}

type giftNote struct {
	Message string `json:"message" validate:"required,max=10"`
}

func newRequest(t *testing.T, body string) *http.Request {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/api/v1/carts/c-1/items", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	return r
}

func TestBindValid(t *testing.T) {
	v := httpx.MustNewValidator()

	var req addCartItemRequest
	if err := v.Bind(newRequest(t, `{"sku":"SKU-1","quantity":2,"unitPrice":9.5}`), &req); err != nil {
		t.Fatalf("Bind returned an error: %v", err)
	}

	if req.SKU != "SKU-1" || req.Quantity != 2 || req.UnitPrice != 9.5 {
		t.Errorf("decoded %+v, want SKU-1/2/9.5", req)
	}
}

func TestBindReportsEveryFailingField(t *testing.T) {
	// A form that surfaces one error at a time costs a round trip per field.
	v := httpx.MustNewValidator()

	var req addCartItemRequest
	err := v.Bind(newRequest(t, `{"sku":"","quantity":0,"unitPrice":0}`), &req)

	invalid, ok := errors.AsType[*httpx.ValidationError](err)
	if !ok {
		t.Fatalf("Bind returned %v, want a *ValidationError", err)
	}

	got := make(map[string]string, len(invalid.Fields))
	for _, field := range invalid.Fields {
		got[field.Field] = field.Code
	}

	for _, field := range []string{"sku", "quantity", "unitPrice"} {
		if _, ok := got[field]; !ok {
			t.Errorf("no failure reported for %q, got %v", field, got)
		}
	}
}

func TestBindReportsFieldsAsTheClientSpelledThem(t *testing.T) {
	// The client sent unitPrice; being told UnitPrice is wrong would leave the
	// frontend translating Go field names back to its own payload.
	v := httpx.MustNewValidator()

	var req addCartItemRequest
	err := v.Bind(newRequest(t, `{"sku":"SKU-1","quantity":1,"unitPrice":-1}`), &req)

	invalid, ok := errors.AsType[*httpx.ValidationError](err)
	if !ok {
		t.Fatalf("Bind returned %v, want a *ValidationError", err)
	}
	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != "unitPrice" {
		t.Errorf("fields = %+v, want one failure on unitPrice", invalid.Fields)
	}
}

func TestBindReportsNestedFieldPaths(t *testing.T) {
	// A client rendering errors against a list needs to know which entry was
	// rejected, not just that something in options was.
	v := httpx.MustNewValidator()

	var req addCartItemRequest
	err := v.Bind(newRequest(t, `{"sku":"SKU-1","quantity":1,"unitPrice":1,"options":[{"name":"red"},{"name":""}]}`), &req)

	invalid, ok := errors.AsType[*httpx.ValidationError](err)
	if !ok {
		t.Fatalf("Bind returned %v, want a *ValidationError", err)
	}
	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != "options[1].name" {
		t.Errorf("fields = %+v, want one failure on options[1].name", invalid.Fields)
	}
}

func TestValidationErrorIsInvalidInput(t *testing.T) {
	// The kind is declared structurally, so errorx maps it without knowing this
	// package exists.
	err := error(&httpx.ValidationError{Fields: []httpx.FieldError{{Field: "sku", Code: "required"}}})

	if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
	}
	if got := errorx.HTTPStatus(err); got != http.StatusBadRequest {
		t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusBadRequest)
	}
}

func TestBindTranslatesToTheNegotiatedLanguage(t *testing.T) {
	v := httpx.MustNewValidator()

	tests := []struct {
		name           string
		acceptLanguage string
		wantThai       bool
	}{
		{
			name:           "no header falls back to english",
			acceptLanguage: "",
		},
		{
			name:           "thai",
			acceptLanguage: "th",
			wantThai:       true,
		},
		{
			// A regional variant has no catalogue of its own and must be
			// served by the base language rather than dropped to the default.
			name:           "regional variant matches its base language",
			acceptLanguage: "th-TH",
			wantThai:       true,
		},
		{
			// Ranked with weights, not a single value: this caller prefers
			// Thai and will accept English.
			name:           "quality values are honoured",
			acceptLanguage: "th;q=0.9,en;q=0.8",
			wantThai:       true,
		},
		{
			name:           "unsupported language falls back to english",
			acceptLanguage: "fr-CA,fr;q=0.9",
		},
		{
			name:           "unparseable header falls back to english",
			acceptLanguage: "!!!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := translateOneFailure(t, v, tt.acceptLanguage)

			// The catalogues share no alphabet, which is the only assertion
			// that stays true when either one is reworded upstream.
			if isThai := strings.ContainsFunc(message, func(r rune) bool { return r >= 0x0E00 && r <= 0x0E7F }); isThai != tt.wantThai {
				t.Errorf("message = %q, thai = %v, want thai = %v", message, isThai, tt.wantThai)
			}
		})
	}
}

// translateOneFailure runs one failing request through Localize and Bind and
// returns the translated message, which is the only way to reach the
// negotiated language from outside the package.
func translateOneFailure(t *testing.T, v *httpx.Validator, acceptLanguage string) string {
	t.Helper()

	var invalid *httpx.ValidationError

	handler := v.Localize(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var req addCartItemRequest

		failure, ok := errors.AsType[*httpx.ValidationError](v.Bind(r, &req))
		if !ok {
			t.Errorf("Bind did not return a *ValidationError")
		}
		invalid = failure
	}))

	r := newRequest(t, `{"sku":"","quantity":1,"unitPrice":1}`)
	if acceptLanguage != "" {
		r.Header.Set("Accept-Language", acceptLanguage)
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if invalid == nil || len(invalid.Fields) == 0 {
		t.Fatalf("no validation failure produced")
	}

	return invalid.Fields[0].Message
}

func TestLocalizeAnnouncesTheLanguageItChose(t *testing.T) {
	v := httpx.MustNewValidator()

	tests := []struct {
		name           string
		acceptLanguage string
		want           string
	}{
		{name: "default", want: "en"},
		{name: "thai", acceptLanguage: "th-TH", want: "th"},
		{name: "unsupported", acceptLanguage: "de", want: "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string

			handler := v.Localize(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = httpx.LanguageFrom(r.Context())
			}))

			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.acceptLanguage != "" {
				r.Header.Set("Accept-Language", tt.acceptLanguage)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			if got != tt.want {
				t.Errorf("LanguageFrom() = %q, want %q", got, tt.want)
			}
			// A caller that asked for Thai and got English should be able to
			// see that rather than assume.
			if header := w.Header().Get("Content-Language"); header != tt.want {
				t.Errorf("Content-Language = %q, want %q", header, tt.want)
			}
		})
	}
}

func TestLanguageFromWithoutLocalize(t *testing.T) {
	if got := httpx.LanguageFrom(context.Background()); got != httpx.DefaultLanguage {
		t.Errorf("LanguageFrom() = %q, want %q", got, httpx.DefaultLanguage)
	}
}

func TestStructRejectsANonStruct(t *testing.T) {
	// Passing something unvalidatable is a bug in the handler. Reporting it as
	// invalid input would tell the client to fix input that was never read.
	v := httpx.MustNewValidator()

	notAStruct := "hello"
	err := v.Struct(context.Background(), notAStruct)

	if got := errorx.KindOf(err); got != errorx.KindInternal {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindInternal)
	}
}

func TestBindRejectsUnreadableBodies(t *testing.T) {
	v := httpx.MustNewValidator()

	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    string
	}{
		{
			name:        "malformed json",
			contentType: "application/json",
			body:        `{"sku": }`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "MALFORMED_JSON",
		},
		{
			name:        "truncated body",
			contentType: "application/json",
			body:        `{"sku": "SKU-1"`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "MALFORMED_JSON",
		},
		{
			name:        "empty body",
			contentType: "application/json",
			body:        ``,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "EMPTY_BODY",
		},
		{
			name:        "wrong field type",
			contentType: "application/json",
			body:        `{"sku":"SKU-1","quantity":"two","unitPrice":1}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "INVALID_FIELD_TYPE",
		},
		{
			// A typo in a field name silently does nothing if it is accepted,
			// and the client never learns the value was dropped.
			name:        "unknown field",
			contentType: "application/json",
			body:        `{"sku":"SKU-1","quantity":1,"unitPrice":1,"quantiy":9}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "UNKNOWN_FIELD",
		},
		{
			name:        "trailing content",
			contentType: "application/json",
			body:        `{"sku":"SKU-1","quantity":1,"unitPrice":1} {"sku":"SKU-2"}`,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "MALFORMED_JSON",
		},
		{
			name:        "not json",
			contentType: "text/plain",
			body:        `sku=SKU-1`,
			wantStatus:  http.StatusUnsupportedMediaType,
			wantCode:    "UNSUPPORTED_MEDIA_TYPE",
		},
		{
			name:        "oversized body",
			contentType: "application/json",
			body:        `{"sku":"` + strings.Repeat("x", int(httpx.MaxBodyBytes)+1) + `"}`,
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantCode:    "BODY_TOO_LARGE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)

			var req addCartItemRequest
			err := v.Bind(r, &req)
			if err == nil {
				t.Fatalf("Bind accepted %q", tt.body)
			}

			// Asserted through WriteError rather than errorx.HTTPStatus,
			// because 413 and 415 have no gRPC code to be mapped from and
			// resolve through Statuser on the way out.
			status, body := writeAndParse(t, err)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if body.Error.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}

			// Decode failures are client bugs, so they carry a code and no
			// per-field prose meant for a person.
			if _, ok := errors.AsType[*httpx.ValidationError](err); ok {
				t.Errorf("decode failure came back as a *ValidationError")
			}
		})
	}
}

// TestUnknownFieldWordingUnchanged pins the standard library error string that
// decodeError has to prefix-match, because encoding/json offers no typed error
// for it. If this fails, the wording changed and UNKNOWN_FIELD silently
// degraded to MALFORMED_JSON.
func TestUnknownFieldWordingUnchanged(t *testing.T) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(`{"nope":1}`)))
	decoder.DisallowUnknownFields()

	var dst struct{}
	err := decoder.Decode(&dst)
	if err == nil {
		t.Fatalf("decoder accepted an unknown field")
	}

	if !strings.HasPrefix(err.Error(), "json: unknown field ") {
		t.Errorf("encoding/json now reports unknown fields as %q", err.Error())
	}
}
