package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

// The whole point of ReadBody: a webhook's signature is computed over exactly
// the bytes that arrived, so a hop that decoded and re-encoded the JSON would
// forward a body that no longer verifies. Whitespace and key order are what
// such a hop normalises away, which is why they are in this fixture.
func TestReadBodyReturnsTheBytesThatArrived(t *testing.T) {
	body := `{"status":"succeeded",  "id":"chrg_1"}`

	got, err := httpx.ReadBody(newRequest(t, body))
	if err != nil {
		t.Fatalf("ReadBody() error = %v", err)
	}

	if string(got) != body {
		t.Errorf("ReadBody() = %q, want %q", got, body)
	}
}

func TestReadBodyRefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string

		wantReason string
		wantStatus int
	}{
		{
			name:        "a body this API cannot parse",
			body:        "id=chrg_1",
			contentType: "text/plain",
			wantReason:  "UNSUPPORTED_MEDIA_TYPE",
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:       "no body at all",
			wantReason: "EMPTY_BODY",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "a body over the limit",
			body:       `{"id":"` + strings.Repeat("x", int(httpx.MaxBodyBytes)) + `"}`,
			wantReason: "BODY_TOO_LARGE",
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRequest(t, tt.body)
			if tt.contentType != "" {
				r.Header.Set("Content-Type", tt.contentType)
			}

			_, err := httpx.ReadBody(r)
			if err == nil {
				t.Fatal("ReadBody() returned no error")
			}

			if got := errorx.Reason(err); got != tt.wantReason {
				t.Errorf("reason = %q, want %q", got, tt.wantReason)
			}

			res := httptest.NewRecorder()
			httpx.WriteError(res, r, err)

			if res.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", res.Code, tt.wantStatus, res.Body)
			}
		})
	}
}

// A body is never decoded here, so nothing about its shape is this function's
// business: what it means belongs to whoever holds the signing secret.
func TestReadBodyDoesNotParseWhatItReads(t *testing.T) {
	body := `{"id": "chrg_1",`

	got, err := httpx.ReadBody(newRequest(t, body))
	if err != nil {
		t.Fatalf("ReadBody() error = %v", err)
	}

	if string(got) != body {
		t.Errorf("ReadBody() = %q, want the malformed body passed through", got)
	}
}
