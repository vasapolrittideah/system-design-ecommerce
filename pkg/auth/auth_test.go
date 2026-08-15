package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/auth"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

const (
	testIssuer   = "identity.test"
	testAudience = "ecommerce.test"
)

func TestSignAndVerify(t *testing.T) {
	_, signer, verifier := testKeys(t)

	before := time.Now()
	token, err := signer.Sign("u-1", []string{"customer", "admin"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	claims, err := verifier.Verify(token.Value)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	if claims.Subject != "u-1" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "u-1")
	}
	if !slices.Equal(claims.Roles, []string{"customer", "admin"}) {
		t.Errorf("Roles = %v, want [customer admin]", claims.Roles)
	}

	identity := claims.Identity()
	if identity.UserID != "u-1" || !slices.Equal(identity.Roles, []string{"customer", "admin"}) {
		t.Errorf("Identity() = %+v, want u-1 with both roles", identity)
	}

	// ExpiresAt is what a login response reports as expires_in, so it has to
	// describe the token actually issued rather than the moment it was read.
	if wantMin, wantMax := before.Add(time.Minute), time.Now().Add(time.Hour); token.ExpiresAt.Before(wantMin) ||
		token.ExpiresAt.After(wantMax) {
		t.Errorf("ExpiresAt = %v, want roughly 15m out", token.ExpiresAt)
	}
}

func TestSignRejectsEmptySubject(t *testing.T) {
	_, signer, _ := testKeys(t)

	if _, err := signer.Sign("", nil); err == nil {
		t.Fatal("Sign() with no subject succeeded, want error")
	}
}

func TestVerifyRejections(t *testing.T) {
	key, _, verifier := testKeys(t)

	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	valid := func() auth.Claims {
		now := time.Now()

		return auth.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   "u-1",
				Issuer:    testIssuer,
				Audience:  jwt.ClaimStrings{testAudience},
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			},
		}
	}

	expired := valid()
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))

	wrongIssuer := valid()
	wrongIssuer.Issuer = "identity.staging"

	wrongAudience := valid()
	wrongAudience.Audience = jwt.ClaimStrings{"someone.else"}

	noExpiry := valid()
	noExpiry.ExpiresAt = nil

	noSubject := valid()
	noSubject.Subject = ""

	tests := []struct {
		name       string
		token      string
		wantReason string
	}{
		{
			name:       "no token at all",
			token:      "",
			wantReason: "TOKEN_MISSING",
		},
		{
			name:       "not a jwt",
			token:      "hello",
			wantReason: "TOKEN_INVALID",
		},
		{
			name:       "expired",
			token:      sign(t, jwt.SigningMethodES256, key, expired),
			wantReason: "TOKEN_EXPIRED",
		},
		{
			// Same key pair, different deployment. Without an issuer check a
			// staging token would open a production session.
			name:       "issued by another deployment",
			token:      sign(t, jwt.SigningMethodES256, key, wrongIssuer),
			wantReason: "TOKEN_INVALID",
		},
		{
			name:       "meant for another audience",
			token:      sign(t, jwt.SigningMethodES256, key, wrongAudience),
			wantReason: "TOKEN_INVALID",
		},
		{
			name:       "signed by a key we do not trust",
			token:      sign(t, jwt.SigningMethodES256, otherKey, valid()),
			wantReason: "TOKEN_INVALID",
		},
		{
			// The alg-confusion attack: the attacker replays the public key,
			// which is not secret, as an HMAC shared secret. A parser that
			// believes the alg header accepts this.
			name:       "hs256 signed with the public key as secret",
			token:      sign(t, jwt.SigningMethodHS256, publicKeyBytes(t, key), valid()),
			wantReason: "TOKEN_INVALID",
		},
		{
			name:       "no expiry claim",
			token:      sign(t, jwt.SigningMethodES256, key, noExpiry),
			wantReason: "TOKEN_INVALID",
		},
		{
			// Verifies perfectly and names nobody; downstream would read it as
			// a call with no user behind it.
			name:       "no subject",
			token:      sign(t, jwt.SigningMethodES256, key, noSubject),
			wantReason: "TOKEN_INVALID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verifier.Verify(tt.token)
			if err == nil {
				t.Fatal("Verify() succeeded, want rejection")
			}

			if got := errorx.Reason(err); got != tt.wantReason {
				t.Errorf("Reason() = %q, want %q", got, tt.wantReason)
			}
			// Unauthenticated, not Unauthorized: the caller has not been
			// identified, which is a 401 and a prompt to re-authenticate.
			if got := errorx.KindOf(err); got != errorx.KindUnauthenticated {
				t.Errorf("KindOf() = %q, want %q", got, errorx.KindUnauthenticated)
			}
			if got := errorx.HTTPStatus(err); got != http.StatusUnauthorized {
				t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusUnauthorized)
			}
		})
	}
}

// TestVerifyToleratesClockDrift covers the reason leeway exists: signer and
// verifier are different pods, and a token whose iat is a few seconds ahead of
// the verifier's clock would otherwise fail once and work on retry.
func TestVerifyToleratesClockDrift(t *testing.T) {
	key, _, verifier := testKeys(t)

	ahead := time.Now().Add(5 * time.Second)
	claims := auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "u-1",
			Issuer:    testIssuer,
			Audience:  jwt.ClaimStrings{testAudience},
			IssuedAt:  jwt.NewNumericDate(ahead),
			NotBefore: jwt.NewNumericDate(ahead),
			ExpiresAt: jwt.NewNumericDate(ahead.Add(time.Minute)),
		},
	}

	if _, err := verifier.Verify(sign(t, jwt.SigningMethodES256, key, claims)); err != nil {
		t.Fatalf("Verify() rejected a token 5s ahead of us: %v", err)
	}
}

// TestKeyEncodings covers both shapes a PEM key survives an environment
// variable in: verbatim, as a Kubernetes Secret delivers it, and base64, as a
// .env file requires.
func TestKeyEncodings(t *testing.T) {
	_, _, publicPEM := generateKeyPair(t)

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{name: "verbatim pem", key: publicPEM},
		{name: "base64 encoded pem", key: base64.StdEncoding.EncodeToString([]byte(publicPEM))},
		{name: "empty", key: "", wantErr: true},
		{name: "neither", key: "not-a-key", wantErr: true},
		{name: "base64 of something else", key: base64.StdEncoding.EncodeToString([]byte("nope")), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := auth.NewVerifier(auth.VerifierConfig{
				PublicKey: tt.key,
				Issuer:    testIssuer,
				Audience:  testAudience,
			})
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("NewVerifier() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuthenticate(t *testing.T) {
	key, signer, verifier := testKeys(t)

	token, err := signer.Sign("u-1", []string{"customer"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	expired := auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "u-1",
			Issuer:    testIssuer,
			Audience:  jwt.ClaimStrings{testAudience},
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}

	tests := []struct {
		name       string
		header     string
		wantStatus int
		wantCode   string
		wantUser   string
	}{
		{
			name:       "valid token",
			header:     "Bearer " + token.Value,
			wantStatus: http.StatusOK,
			wantUser:   "u-1",
		},
		{
			// RFC 7235 makes the scheme case-insensitive and clients spell it
			// every way there is.
			name:       "lowercase scheme",
			header:     "bearer " + token.Value,
			wantStatus: http.StatusOK,
			wantUser:   "u-1",
		},
		{
			name:       "no header",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "TOKEN_MISSING",
		},
		{
			name:       "wrong scheme",
			header:     "Basic " + token.Value,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "TOKEN_MISSING",
		},
		{
			// The one rejection a client acts on differently: refresh rather
			// than log in again.
			name:       "expired token",
			header:     "Bearer " + sign(t, jwt.SigningMethodES256, key, expired),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "TOKEN_EXPIRED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotIdentity grpcx.Identity
			var gotFound bool
			var gotLoggedUser string

			handler := auth.Authenticate(verifier)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				gotIdentity, gotFound = grpcx.IdentityFrom(r.Context())
				gotLoggedUser = logger.UserID(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/orders", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantStatus != http.StatusOK {
				if gotFound {
					t.Error("handler ran with an identity on a rejected request")
				}

				var body httpx.ErrorResponse
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatalf("decode error body: %v", err)
				}
				if body.Error.Code != tt.wantCode {
					t.Errorf("error.code = %q, want %q", body.Error.Code, tt.wantCode)
				}

				return
			}

			if !gotFound {
				t.Fatal("handler ran without an identity on the context")
			}
			if gotIdentity.UserID != tt.wantUser {
				t.Errorf("UserID = %q, want %q", gotIdentity.UserID, tt.wantUser)
			}
			// pkg/grpcx/client reads the identity off the context to forward
			// it, and pkg/logger reads the user ID to stamp every log line.
			if gotLoggedUser != tt.wantUser {
				t.Errorf("logger user_id = %q, want %q", gotLoggedUser, tt.wantUser)
			}
		})
	}
}

// TestAuthenticateIgnoresForwardedHeaders is the zero-trust rule as a test: the
// identity comes from the signature this middleware checked, never from what the
// request claims about itself.
func TestAuthenticateIgnoresForwardedHeaders(t *testing.T) {
	_, signer, verifier := testKeys(t)

	token, err := signer.Sign("u-1", []string{"customer"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	var got grpcx.Identity
	handler := auth.Authenticate(verifier)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = grpcx.IdentityFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	req.Header.Set("Authorization", "Bearer "+token.Value)
	req.Header.Set(grpcx.MetadataUserID, "u-victim")
	req.Header.Set(grpcx.MetadataUserRoles, "admin")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got.UserID != "u-1" {
		t.Errorf("UserID = %q, want u-1 — headers overrode the verified token", got.UserID)
	}
	if slices.Contains(got.Roles, "admin") {
		t.Errorf("Roles = %v, want the token's roles only", got.Roles)
	}
}

// testKeys returns a matched signer and verifier over one freshly generated key
// pair, plus the private key for tests that forge their own tokens.
func testKeys(t *testing.T) (*ecdsa.PrivateKey, *auth.Signer, *auth.Verifier) {
	t.Helper()

	key, privatePEM, publicPEM := generateKeyPair(t)

	signer, err := auth.NewSigner(auth.SignerConfig{
		PrivateKey: config.Secret(privatePEM),
		KeyID:      "test-key",
		Issuer:     testIssuer,
		Audience:   testAudience,
		TTL:        15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}

	verifier, err := auth.NewVerifier(auth.VerifierConfig{
		PublicKey: publicPEM,
		Issuer:    testIssuer,
		Audience:  testAudience,
		Leeway:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}

	return key, signer, verifier
}

func generateKeyPair(t *testing.T) (key *ecdsa.PrivateKey, privatePEM, publicPEM string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}

	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}

	return key,
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
}

func sign(t *testing.T, method jwt.SigningMethod, key any, claims auth.Claims) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}

	return signed
}

func publicKeyBytes(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()

	encoded, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}

	return encoded
}
