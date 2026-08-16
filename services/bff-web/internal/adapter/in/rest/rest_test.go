package rest_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/auth"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/bff-web/internal/adapter/in/rest"
)

const (
	testIssuer   = "http://identity.test"
	testAudience = "ecommerce-test"
	testUserID   = "6f1b3c9e-6c1e-4f5a-9f2a-1d2c3b4a5e6f"
)

// stubIdentityClient records what the handler asked for and answers with what
// the test wants.
//
// Hand-written rather than generated: .mockery.yml lists driven ports only, and
// what these tests assert on is the mapping either side of this boundary.
type stubIdentityClient struct {
	registerReq *identityv1.RegisterRequest
	loginReq    *identityv1.LoginRequest
	refreshReq  *identityv1.RefreshTokenRequest
	logoutReq   *identityv1.LogoutRequest
	getUserReq  *identityv1.GetUserRequest

	user *identityv1.User
	err  error
}

func (s *stubIdentityClient) Register(
	_ context.Context, in *identityv1.RegisterRequest, _ ...grpc.CallOption,
) (*identityv1.RegisterResponse, error) {
	s.registerReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &identityv1.RegisterResponse{User: s.user}, nil
}

func (s *stubIdentityClient) Login(
	_ context.Context, in *identityv1.LoginRequest, _ ...grpc.CallOption,
) (*identityv1.LoginResponse, error) {
	s.loginReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &identityv1.LoginResponse{Tokens: testTokenPair(), User: s.user}, nil
}

func (s *stubIdentityClient) RefreshToken(
	_ context.Context, in *identityv1.RefreshTokenRequest, _ ...grpc.CallOption,
) (*identityv1.RefreshTokenResponse, error) {
	s.refreshReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &identityv1.RefreshTokenResponse{Tokens: testTokenPair()}, nil
}

func (s *stubIdentityClient) Logout(
	_ context.Context, in *identityv1.LogoutRequest, _ ...grpc.CallOption,
) (*identityv1.LogoutResponse, error) {
	s.logoutReq = in

	return &identityv1.LogoutResponse{}, s.err
}

func (s *stubIdentityClient) GetUser(
	_ context.Context, in *identityv1.GetUserRequest, _ ...grpc.CallOption,
) (*identityv1.GetUserResponse, error) {
	s.getUserReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &identityv1.GetUserResponse{User: s.user}, nil
}

func (s *stubIdentityClient) GetUsersByIDs(
	_ context.Context, _ *identityv1.GetUsersByIDsRequest, _ ...grpc.CallOption,
) (*identityv1.GetUsersByIDsResponse, error) {
	return &identityv1.GetUsersByIDsResponse{}, s.err
}

func TestRegisterReturnsCreatedWithNoTokens(t *testing.T) {
	stub := &stubIdentityClient{user: testUser()}
	srv, _ := newServer(t, stub)

	res := do(t, srv, http.MethodPost, "/api/v1/auth/register", "", `{
		"email": "ada@example.com",
		"password": "hunter2hunter2"
	}`)

	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusCreated, res.Body)
	}

	if stub.registerReq.GetEmail() != "ada@example.com" || stub.registerReq.GetPassword() != "hunter2hunter2" {
		t.Errorf("register called with %+v, want the request's credentials", stub.registerReq)
	}

	body := decodeBody(t, res)

	// Registering and signing in are separate acts. A tokens key appearing here
	// would mean the BFF invented a session the service did not open.
	if _, found := body["tokens"]; found {
		t.Errorf("body carries tokens, want only the created user: %s", res.Body)
	}

	user, _ := body["user"].(map[string]any)
	if user["id"] != testUserID {
		t.Errorf("user.id = %v, want %q", user["id"], testUserID)
	}
}

func TestLoginReturnsCamelCaseTokensAndUser(t *testing.T) {
	srv, _ := newServer(t, &stubIdentityClient{user: testUser()})

	res := do(t, srv, http.MethodPost, "/api/v1/auth/login", "", `{
		"email": "ada@example.com",
		"password": "hunter2"
	}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	tokens, _ := decodeBody(t, res)["tokens"].(map[string]any)

	// Every key is asserted by name because camelCase is the API, not a
	// preference: a json tag lost in an edit renames a field the frontend binds
	// to, and nothing else in the pipeline would notice.
	for _, key := range []string{
		"accessToken", "refreshToken", "accessTokenExpiresAt", "refreshTokenExpiresAt",
	} {
		if _, found := tokens[key]; !found {
			t.Errorf("tokens is missing %q: %s", key, res.Body)
		}
	}

	if tokens["accessToken"] != "access-token" || tokens["refreshToken"] != "refresh-token" {
		t.Errorf("tokens = %+v, want the values identity returned", tokens)
	}
}

// The refresh token travels in the body and nowhere else. Reading it from the
// Authorization header instead would put a 30-day credential on every request to
// every service, which is the whole reason it is not a JWT.
func TestRefreshReadsTokenFromBodyNotHeader(t *testing.T) {
	stub := &stubIdentityClient{}
	srv, _ := newServer(t, stub)

	res := do(t, srv, http.MethodPost, "/api/v1/auth/refresh", "Bearer not-a-refresh-token",
		`{"refreshToken": "opaque-refresh-token"}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	if stub.refreshReq.GetRefreshToken() != "opaque-refresh-token" {
		t.Errorf("refresh called with %q, want the body's token", stub.refreshReq.GetRefreshToken())
	}
}

func TestLogoutReturnsNoContent(t *testing.T) {
	stub := &stubIdentityClient{}
	srv, _ := newServer(t, stub)

	res := do(t, srv, http.MethodPost, "/api/v1/auth/logout", "", `{"refreshToken": "opaque-refresh-token"}`)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusNoContent, res.Body)
	}

	if res.Body.Len() != 0 {
		t.Errorf("body = %s, want empty", res.Body)
	}

	if stub.logoutReq.GetRefreshToken() != "opaque-refresh-token" {
		t.Errorf("logout called with %q, want the body's token", stub.logoutReq.GetRefreshToken())
	}
}

// A downstream failure keeps its meaning across the boundary. Flattening it into
// a 500 would tell a client with a stale session that the service is broken, and
// the reason code is what the frontend actually branches on.
func TestDownstreamErrorKeepsStatusAndReasonCode(t *testing.T) {
	failure := errorx.ToGRPC(errorx.New(errorx.KindUnauthenticated, "invalid credentials").
		WithReason("INVALID_CREDENTIALS"))

	srv, _ := newServer(t, &stubIdentityClient{err: failure})

	res := do(t, srv, http.MethodPost, "/api/v1/auth/login", "", `{
		"email": "ada@example.com",
		"password": "wrong-password"
	}`)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusUnauthorized, res.Body)
	}

	if code := errorCode(t, res); code != "INVALID_CREDENTIALS" {
		t.Errorf("error.code = %q, want INVALID_CREDENTIALS", code)
	}
}

// An internal failure is the one case where the message is replaced. Anything
// that leaked the downstream error here would put database detail in a browser.
func TestInternalErrorIsScrubbed(t *testing.T) {
	failure := status.Error(codes.Internal, "pq: relation \"users\" does not exist")
	srv, _ := newServer(t, &stubIdentityClient{err: failure})

	res := do(t, srv, http.MethodPost, "/api/v1/auth/login", "", `{
		"email": "ada@example.com",
		"password": "hunter2"
	}`)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}

	if strings.Contains(res.Body.String(), "does not exist") {
		t.Errorf("body leaks the downstream error: %s", res.Body)
	}
}

func TestValidationFailureNamesFieldsAsTheClientSentThem(t *testing.T) {
	srv, _ := newServer(t, &stubIdentityClient{})

	res := do(t, srv, http.MethodPost, "/api/v1/auth/register", "", `{
		"email": "not-an-email",
		"password": "short"
	}`)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusBadRequest, res.Body)
	}

	body, _ := decodeBody(t, res)["error"].(map[string]any)
	fields, _ := body["fields"].([]any)

	if len(fields) != 2 {
		t.Fatalf("fields = %v, want one entry per failing field", fields)
	}

	// Reported under the json tag, not the Go field name: a client that sent
	// "email" must not be told "Email" is wrong.
	for _, entry := range fields {
		field, _ := entry.(map[string]any)
		if name, _ := field["field"].(string); name != "email" && name != "password" {
			t.Errorf("field = %q, want the camelCase name the client sent", name)
		}
	}
}

func TestMeRejectsRequestWithoutToken(t *testing.T) {
	stub := &stubIdentityClient{user: testUser()}
	srv, _ := newServer(t, stub)

	res := do(t, srv, http.MethodGet, "/api/v1/me", "", "")

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusUnauthorized, res.Body)
	}

	if code := errorCode(t, res); code != "TOKEN_MISSING" {
		t.Errorf("error.code = %q, want TOKEN_MISSING", code)
	}

	if stub.getUserReq != nil {
		t.Error("identity was called for an unauthenticated request")
	}
}

// The user read is the one the signature named. Taking the ID from anywhere the
// caller controls — a header, a query parameter — would make this endpoint able
// to read somebody else's account.
func TestMeReadsSubjectFromTheVerifiedToken(t *testing.T) {
	stub := &stubIdentityClient{user: testUser()}
	srv, signer := newServer(t, stub)

	token, err := signer.Sign(testUserID, []string{"customer"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token.Value)
	// Set by hand, and ignored: identity comes from the signature this process
	// verified itself.
	req.Header.Set("X-User-ID", "11111111-1111-1111-1111-111111111111")

	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	if stub.getUserReq.GetId() != testUserID {
		t.Errorf("GetUser called with %q, want the token's subject", stub.getUserReq.GetId())
	}
}

func TestUnroutedPathAnswersInTheErrorShape(t *testing.T) {
	srv, _ := newServer(t, &stubIdentityClient{})

	res := do(t, srv, http.MethodGet, "/api/v1/nothing-here", "", "")

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNotFound)
	}

	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want JSON — chi's plain-text fallback is still mounted", got)
	}

	if code := errorCode(t, res); code != "ROUTE_NOT_FOUND" {
		t.Errorf("error.code = %q, want ROUTE_NOT_FOUND", code)
	}
}

func TestWrongMethodAnswers405(t *testing.T) {
	srv, _ := newServer(t, &stubIdentityClient{})

	res := do(t, srv, http.MethodGet, "/api/v1/auth/login", "", "")

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusMethodNotAllowed, res.Body)
	}
}

// newServer builds the router the way bootstrap does, minus the metrics
// registry: the tests below run in one process and a shared registry would
// panic on the second router built.
func newServer(t *testing.T, identity identityv1.IdentityServiceClient) (http.Handler, *auth.Signer) {
	t.Helper()

	privatePEM, publicPEM := generateKeyPair(t)

	signer, err := auth.NewSigner(auth.SignerConfig{
		PrivateKey: config.Secret(privatePEM),
		KeyID:      "test-k1",
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

	validator := httpx.MustNewValidator()
	router := httpx.MustNewRouter(validator)
	rest.NewHandler(identity, validator).Mount(router, auth.Authenticate(verifier))

	return router, signer
}

func do(t *testing.T, srv http.Handler, method, path, authorization, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)

	return res
}

func decodeBody(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", res.Body, err)
	}

	return body
}

func errorCode(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()

	body, _ := decodeBody(t, res)["error"].(map[string]any)
	code, _ := body["code"].(string)

	return code
}

func testUser() *identityv1.User {
	return &identityv1.User{
		Id:        testUserID,
		Email:     "ada@example.com",
		Roles:     []string{"customer"},
		CreatedAt: timestamppb.New(time.Now()),
		UpdatedAt: timestamppb.New(time.Now()),
	}
}

func testTokenPair() *identityv1.TokenPair {
	return &identityv1.TokenPair{
		AccessToken:           "access-token",
		RefreshToken:          "refresh-token",
		AccessTokenExpiresAt:  timestamppb.New(time.Now().Add(15 * time.Minute)),
		RefreshTokenExpiresAt: timestamppb.New(time.Now().Add(720 * time.Hour)),
	}
}

func generateKeyPair(t *testing.T) (privatePEM, publicPEM string) {
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

	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
}
