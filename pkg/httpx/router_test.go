package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()

	r := httpx.MustNewRouter(httpx.MustNewValidator())
	r.Get("/api/v1/carts/{cartId}", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusOK, map[string]string{"cartId": "c-1"})
	})
	r.Get("/boom", func(_ http.ResponseWriter, _ *http.Request) {
		panic("handler exploded")
	})

	return r
}

// TestRouterAnswersEveryFailureInOneShape is the promise the package makes: a
// client writes its error handling once. chi's defaults answer text/plain for
// an unrouted path, the wrong method, and a panic, which would be three
// exceptions to that rule on day one.
func TestRouterAnswersEveryFailureInOneShape(t *testing.T) {
	router := newTestRouter(t)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unrouted path",
			method:     http.MethodGet,
			path:       "/api/v1/nope",
			wantStatus: http.StatusNotFound,
			wantCode:   "ROUTE_NOT_FOUND",
		},
		{
			name:       "wrong method",
			method:     http.MethodDelete,
			path:       "/api/v1/carts/c-1",
			wantStatus: http.StatusMethodNotAllowed,
			wantCode:   "METHOD_NOT_ALLOWED",
		},
		{
			name:       "panicking handler",
			method:     http.MethodGet,
			path:       "/boom",
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if got := w.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}

			var body httpx.ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode body %q: %v", w.Body.String(), err)
			}
			if body.Error.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}
		})
	}
}

func TestRouterNeverLeaksAStack(t *testing.T) {
	// chi's recoverer prints the stack into the response, which hands out the
	// source layout to anyone who can make a handler panic.
	w := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if strings.Contains(w.Body.String(), "goroutine") || strings.Contains(w.Body.String(), "handler exploded") {
		t.Errorf("panic details reached the client: %s", w.Body.String())
	}
}

func TestRouterCorrelatesEveryResponse(t *testing.T) {
	// Including the ones no handler produced, so a user can report a 404 or a
	// panic by quoting one ID.
	for _, path := range []string{"/api/v1/carts/c-1", "/api/v1/nope", "/boom"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			newTestRouter(t).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

			if got := w.Header().Get("x-correlation-id"); got == "" {
				t.Errorf("no correlation ID on the response")
			}
		})
	}
}
