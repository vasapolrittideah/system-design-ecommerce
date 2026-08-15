package auth

import (
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// bearerScheme is the authorization scheme, matched case-insensitively because
// RFC 7235 says the scheme token is case-insensitive and clients disagree about
// how to spell it.
const bearerScheme = "bearer "

// Authenticate verifies the bearer token and puts the caller on the context.
//
// It rejects a request without a valid token, so it is mounted on the route
// groups that require a user and left off the ones that cannot have one yet —
// login, register, refresh, health. There is deliberately no "optional" mode: a
// middleware that sometimes authenticates moves the decision into the handlers,
// one at a time.
//
// It ignores X-User-ID and X-User-Roles entirely. The identity it installs comes
// from the signature it verified itself, which is the whole content of the
// zero-trust rule — a client setting those headers by hand is a non-event.
//
// The identity goes on the context because that is where pkg/grpcx/client looks
// for it, and from there it reaches every service the request fans out to.
func Authenticate(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := v.Verify(bearerToken(r))
			if err != nil {
				// Info, not Error: a rejected token is a user with a stale
				// session, not a broken service, and logging it at error puts
				// ordinary logins into the rate an alert fires on.
				logger.From(r.Context()).Info("rejected unauthenticated request", zap.Error(err))
				httpx.WriteError(w, r, err)

				return
			}

			identity := claims.Identity()
			ctx := grpcx.IdentityInto(r.Context(), identity)
			ctx = logger.WithUserID(ctx, identity.UserID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken returns the token from the Authorization header, or "" when there
// is nothing there to verify — which the verifier reports as TOKEN_MISSING
// rather than as a malformed token, so a caller that simply forgot the header
// gets told so.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) < len(bearerScheme) || !strings.EqualFold(header[:len(bearerScheme)], bearerScheme) {
		return ""
	}

	return strings.TrimSpace(header[len(bearerScheme):])
}
