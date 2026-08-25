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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
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

	testProductID = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
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

// stubCatalogClient answers the two RPCs the storefront reads through.
//
// The interface is embedded rather than implemented method by method, so every
// other RPC — the writes an admin console will need — is a nil call that panics.
// That is the intended failure: a write wired into this BFF should stop a test
// rather than pass one.
type stubCatalogClient struct {
	catalogv1.CatalogServiceClient

	listReq *catalogv1.ListProductsRequest
	getReq  *catalogv1.GetProductRequest

	product       *catalogv1.Product
	products      []*catalogv1.Product
	nextPageToken string
	err           error
}

func (s *stubCatalogClient) GetProduct(
	_ context.Context, in *catalogv1.GetProductRequest, _ ...grpc.CallOption,
) (*catalogv1.GetProductResponse, error) {
	s.getReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &catalogv1.GetProductResponse{Product: s.product}, nil
}

func (s *stubCatalogClient) ListProducts(
	_ context.Context, in *catalogv1.ListProductsRequest, _ ...grpc.CallOption,
) (*catalogv1.ListProductsResponse, error) {
	s.listReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &catalogv1.ListProductsResponse{
		Products:      s.products,
		NextPageToken: s.nextPageToken,
	}, nil
}

// stubInventoryClient answers the one RPC the product page reads through.
//
// The interface is embedded for the same reason the catalog stub embeds its
// own: every RPC an order will reserve, commit, and release through is a nil
// call that panics, so a write wired into this BFF stops a test rather than
// passing one.
type stubInventoryClient struct {
	inventoryv1.InventoryServiceClient

	stockReq *inventoryv1.GetStockBySKUsRequest

	items []*inventoryv1.StockItem
	err   error
}

func (s *stubInventoryClient) GetStockBySKUs(
	_ context.Context, in *inventoryv1.GetStockBySKUsRequest, _ ...grpc.CallOption,
) (*inventoryv1.GetStockBySKUsResponse, error) {
	s.stockReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &inventoryv1.GetStockBySKUsResponse{Items: s.items}, nil
}

// stubOrderClient answers the checkout screens.
//
// The interface is embedded for the same reason the others embed theirs: an RPC
// no test set up is a nil call that panics, so a route wired to a method this
// stub does not implement stops a test rather than passing one.
type stubOrderClient struct {
	orderv1.OrderServiceClient

	checkoutReq *orderv1.CheckoutRequest
	getReq      *orderv1.GetOrderRequest
	listReq     *orderv1.ListOrdersRequest

	order         *orderv1.Order
	orders        []*orderv1.Order
	nextPageToken string
	err           error
}

func (s *stubOrderClient) Checkout(
	_ context.Context, in *orderv1.CheckoutRequest, _ ...grpc.CallOption,
) (*orderv1.CheckoutResponse, error) {
	s.checkoutReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &orderv1.CheckoutResponse{Order: s.order}, nil
}

func (s *stubOrderClient) GetOrder(
	_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption,
) (*orderv1.GetOrderResponse, error) {
	s.getReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &orderv1.GetOrderResponse{Order: s.order}, nil
}

func (s *stubOrderClient) ListOrders(
	_ context.Context, in *orderv1.ListOrdersRequest, _ ...grpc.CallOption,
) (*orderv1.ListOrdersResponse, error) {
	s.listReq = in
	if s.err != nil {
		return nil, s.err
	}

	return &orderv1.ListOrdersResponse{Orders: s.orders, NextPageToken: s.nextPageToken}, nil
}

func TestRegisterReturnsCreatedWithNoTokens(t *testing.T) {
	stub := &stubIdentityClient{user: testUser()}
	srv, _ := newServer(t, stub, &stubCatalogClient{}, &stubInventoryClient{})

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
	srv, _ := newServer(t, &stubIdentityClient{user: testUser()}, &stubCatalogClient{}, &stubInventoryClient{})

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
	srv, _ := newServer(t, stub, &stubCatalogClient{}, &stubInventoryClient{})

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
	srv, _ := newServer(t, stub, &stubCatalogClient{}, &stubInventoryClient{})

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

	srv, _ := newServer(t, &stubIdentityClient{err: failure}, &stubCatalogClient{}, &stubInventoryClient{})

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
	srv, _ := newServer(t, &stubIdentityClient{err: failure}, &stubCatalogClient{}, &stubInventoryClient{})

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
	srv, _ := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{})

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
	srv, _ := newServer(t, stub, &stubCatalogClient{}, &stubInventoryClient{})

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
	srv, signer := newServer(t, stub, &stubCatalogClient{}, &stubInventoryClient{})

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

// The listing is anonymous and shaped for a grid: the cheapest variant becomes
// the price a card quotes, and everything a card does not render is left behind
// rather than forwarded because catalog happened to return it.
func TestListProductsShapesCardsAndNeedsNoToken(t *testing.T) {
	catalog := &stubCatalogClient{
		products:      []*catalogv1.Product{testProduct()},
		nextPageToken: "b3BhcXVl",
	}
	srv, _ := newServer(t, &stubIdentityClient{}, catalog, &stubInventoryClient{})

	res := do(t, srv, http.MethodGet, "/api/v1/products", "", "")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	body := decodeBody(t, res)

	if body["nextPageToken"] != "b3BhcXVl" {
		t.Errorf("nextPageToken = %v, want the token catalog returned", body["nextPageToken"])
	}

	products, _ := body["products"].([]any)
	if len(products) != 1 {
		t.Fatalf("products = %v, want one card", products)
	}

	card, _ := products[0].(map[string]any)

	if card["name"] != "Oxford shirt" || card["category"] != "clothing/shirts" {
		t.Errorf("card = %+v, want the product's name and category", card)
	}

	// A description of up to 4000 characters per product, on a screen that
	// shows none of them, is the payload this shaping exists to avoid.
	if _, found := card["description"]; found {
		t.Errorf("card carries a description the grid does not render: %s", res.Body)
	}

	if _, found := card["variants"]; found {
		t.Errorf("card carries the variant list: %s", res.Body)
	}

	price, _ := card["priceFrom"].(map[string]any)
	if price["amountMinor"] != float64(89000) || price["currencyCode"] != "THB" {
		t.Errorf("priceFrom = %+v, want the cheapest variant's price", price)
	}

	if card["variantCount"] != float64(2) {
		t.Errorf("variantCount = %v, want 2", card["variantCount"])
	}
}

// The storefront cannot be asked for drafts. Catalog reads an unset status as
// ACTIVE only, so forwarding a status a client picked would put unfinished
// products on a shop window one guessed query parameter later.
func TestListProductsCannotBeAskedForDrafts(t *testing.T) {
	catalog := &stubCatalogClient{}
	srv, _ := newServer(t, &stubIdentityClient{}, catalog, &stubInventoryClient{})

	res := do(t, srv, http.MethodGet,
		"/api/v1/products?category=clothing/shirts&status=PRODUCT_STATUS_DRAFT&utm_source=newsletter", "", "")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	if got := catalog.listReq.GetStatus(); got != catalogv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED {
		t.Errorf("status = %v, want it left unset so catalog filters to ACTIVE", got)
	}

	// utm_source names no field and is ignored rather than rejected: a link
	// from a campaign is still a valid request.
	if catalog.listReq.GetCategory() != "clothing/shirts" {
		t.Errorf("category = %q, want the one the client asked for", catalog.listReq.GetCategory())
	}
}

// An empty page is an empty array, never null — a client should not need a nil
// check to iterate a collection that is simply empty.
func TestListProductsAnswersAnEmptyPageAsAnArray(t *testing.T) {
	srv, _ := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{})

	res := do(t, srv, http.MethodGet, "/api/v1/products", "", "")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	if !strings.Contains(res.Body.String(), `"products":[]`) {
		t.Errorf("body = %s, want products as an empty array", res.Body)
	}
}

// Query parameters are checked here, before the call: a page size the contract
// forbids is a 400 this tier answers rather than a round trip spent to be told
// the same thing.
func TestListProductsRejectsAPageSizeBeyondTheCap(t *testing.T) {
	catalog := &stubCatalogClient{}
	srv, _ := newServer(t, &stubIdentityClient{}, catalog, &stubInventoryClient{})

	res := do(t, srv, http.MethodGet, "/api/v1/products?pageSize=500", "", "")

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusBadRequest, res.Body)
	}

	body, _ := decodeBody(t, res)["error"].(map[string]any)
	fields, _ := body["fields"].([]any)

	if len(fields) != 1 {
		t.Fatalf("fields = %v, want the failing parameter named", fields)
	}

	// Named as the client wrote it in the URL, not as the Go field it decoded
	// into.
	field, _ := fields[0].(map[string]any)
	if field["field"] != "pageSize" {
		t.Errorf("field = %v, want pageSize", field["field"])
	}

	if catalog.listReq != nil {
		t.Error("catalog was called for a request that never passed validation")
	}
}

func TestListProductsRejectsANonNumericPageSize(t *testing.T) {
	srv, _ := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{})

	res := do(t, srv, http.MethodGet, "/api/v1/products?pageSize=many", "", "")

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusBadRequest, res.Body)
	}

	if code := errorCode(t, res); code != "INVALID_FIELD_TYPE" {
		t.Errorf("error.code = %q, want INVALID_FIELD_TYPE", code)
	}
}

// Catalog is critical to this screen: there is nothing else that can say what is
// on sale, so its failure is the request's failure and keeps the meaning it was
// given rather than becoming a 500.
func TestListProductsFailsWhenCatalogIsUnreachable(t *testing.T) {
	catalog := &stubCatalogClient{err: status.Error(codes.Unavailable, "connection refused")}
	srv, _ := newServer(t, &stubIdentityClient{}, catalog, &stubInventoryClient{})

	res := do(t, srv, http.MethodGet, "/api/v1/products", "", "")

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusServiceUnavailable, res.Body)
	}
}

func TestUnroutedPathAnswersInTheErrorShape(t *testing.T) {
	srv, _ := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{})

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
	srv, _ := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{})

	res := do(t, srv, http.MethodGet, "/api/v1/auth/login", "", "")

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusMethodNotAllowed, res.Body)
	}
}

// newServer builds the router the way bootstrap does, minus the metrics
// registry: the tests below run in one process and a shared registry would
// panic on the second router built.
func newServer(
	t *testing.T,
	identity identityv1.IdentityServiceClient,
	catalog catalogv1.CatalogServiceClient,
	inventory inventoryv1.InventoryServiceClient,
	// Variadic so that the twenty tests written before orders existed keep
	// reading as they did: a screen that never reaches the order service has
	// nothing to say about it, and passing an empty stub at each of those call
	// sites would be noise standing in for a dependency none of them uses.
	orders ...orderv1.OrderServiceClient,
) (*chi.Mux, *auth.Signer) {
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
	order := orderv1.OrderServiceClient(&stubOrderClient{})
	if len(orders) > 0 {
		order = orders[0]
	}

	rest.NewHandler(identity, catalog, inventory, order, validator).Mount(router, auth.Authenticate(verifier))

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

// testProduct is a published product with two variants, the second cheaper than
// the first — so a card quoting the first price passes nothing here.
// The product page is where two services meet, and the whole of what this
// asserts is that they were joined on the right key: the count the warehouse
// gave for a SKU lands on the variant carrying it, and a SKU the warehouse does
// not track reads as none left rather than as unknown.
func TestGetProductMergesAvailabilityOntoItsVariant(t *testing.T) {
	catalog := &stubCatalogClient{product: testProduct()}
	inventory := &stubInventoryClient{items: testStock()}
	srv, _ := newServer(t, &stubIdentityClient{}, catalog, inventory)

	res := do(t, srv, http.MethodGet, "/api/v1/products/"+testProductID, "", "")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	// One batch call carrying every SKU on the page, never one call per
	// variant.
	if got := inventory.stockReq.GetSkus(); !slices.Equal(got, []string{"SHIRT-OX-L", "SHIRT-OX-M"}) {
		t.Errorf("GetStockBySKUs called with %v, want both of the product's SKUs", got)
	}

	variants := productVariants(t, res)

	// SHIRT-OX-M is the one testStock names.
	if got := variants["SHIRT-OX-M"]["availableQuantity"]; got != float64(4) {
		t.Errorf("SHIRT-OX-M availableQuantity = %v, want 4", got)
	}

	// SHIRT-OX-L is tracked by nobody, which is a known zero: the warehouse
	// answered, and what it said is that there are none.
	if got := variants["SHIRT-OX-L"]["availableQuantity"]; got != float64(0) {
		t.Errorf("SHIRT-OX-L availableQuantity = %v, want 0", got)
	}

	// What the warehouse is holding for other people is its own business. A
	// reserved count on a storefront tells a competitor the sales rate.
	if _, found := variants["SHIRT-OX-M"]["reserved"]; found {
		t.Errorf("variant = %+v, want no reserved count", variants["SHIRT-OX-M"])
	}
}

// Availability is the optional half of this page. The warehouse being down
// costs the counts and nothing else — the alternative, a 503, would take the
// storefront down with a service the shopper does not need to read a page.
func TestGetProductServesThePageWhenInventoryFails(t *testing.T) {
	catalog := &stubCatalogClient{product: testProduct()}
	inventory := &stubInventoryClient{err: status.Error(codes.Unavailable, "connection refused")}
	srv, _ := newServer(t, &stubIdentityClient{}, catalog, inventory)

	res := do(t, srv, http.MethodGet, "/api/v1/products/"+testProductID, "", "")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	for sku, variant := range productVariants(t, res) {
		// Null and not zero. A client rendering an unanswered warehouse as
		// "sold out" hides a product that is on the shelf, so the two facts
		// stay distinguishable in the body.
		quantity, found := variant["availableQuantity"]
		if !found {
			t.Errorf("%s = %+v, want an availableQuantity key", sku, variant)
		}

		if quantity != nil {
			t.Errorf("%s availableQuantity = %v, want null", sku, quantity)
		}
	}
}

// The storefront names no status, so catalog answers about published products
// alone. Passing one through would make a draft reachable by anyone who guessed
// a UUID, which is the reason the field exists on that request at all.
func TestGetProductAsksTheCatalogForNoParticularStatus(t *testing.T) {
	catalog := &stubCatalogClient{product: testProduct()}
	srv, _ := newServer(t, &stubIdentityClient{}, catalog, &stubInventoryClient{})

	do(t, srv, http.MethodGet, "/api/v1/products/"+testProductID+"?status=DRAFT", "", "")

	if got := catalog.getReq.GetId(); got != testProductID {
		t.Errorf("GetProduct called with %q, want the path's id", got)
	}

	if got := catalog.getReq.GetStatus(); got != catalogv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED {
		t.Errorf("GetProduct called with status %v, want unspecified", got)
	}
}

// A product with nothing to sell has no SKU to ask about, and GetStockBySKUs
// refuses an empty list — so the call is skipped rather than made and logged as
// a failure this BFF caused.
func TestGetProductWithNoVariantsNeverCallsInventory(t *testing.T) {
	product := testProduct()
	product.Variants = nil

	inventory := &stubInventoryClient{}
	srv, _ := newServer(t, &stubIdentityClient{}, &stubCatalogClient{product: product}, inventory)

	res := do(t, srv, http.MethodGet, "/api/v1/products/"+testProductID, "", "")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}

	if inventory.stockReq != nil {
		t.Errorf("GetStockBySKUs called with %v, want no call at all", inventory.stockReq)
	}
}

// productVariants reads the page's variants back keyed by SKU, which is how the
// assertions above name one without depending on the order catalog returned.
func productVariants(t *testing.T, res *httptest.ResponseRecorder) map[string]map[string]any {
	t.Helper()

	product, _ := decodeBody(t, res)["product"].(map[string]any)

	raw, _ := product["variants"].([]any)
	if len(raw) == 0 {
		t.Fatalf("product = %+v, want variants", product)
	}

	variants := make(map[string]map[string]any, len(raw))
	for _, entry := range raw {
		variant, _ := entry.(map[string]any)

		sku, _ := variant["sku"].(string)
		variants[sku] = variant
	}

	return variants
}

func testProduct() *catalogv1.Product {
	return &catalogv1.Product{
		Id:          testProductID,
		Name:        "Oxford shirt",
		Description: "A shirt described at length, for the product page.",
		Category:    "clothing/shirts",
		Status:      catalogv1.ProductStatus_PRODUCT_STATUS_ACTIVE,
		Variants: []*catalogv1.Variant{
			{
				Id:    "3f2504e0-4f89-41d3-9a0c-0305e82c3401",
				Sku:   "SHIRT-OX-L",
				Price: &commonv1.Money{AmountMinor: 99000, CurrencyCode: "THB"},
			},
			{
				Id:    "3f2504e0-4f89-41d3-9a0c-0305e82c3402",
				Sku:   "SHIRT-OX-M",
				Price: &commonv1.Money{AmountMinor: 89000, CurrencyCode: "THB"},
			},
		},
		CreatedAt: timestamppb.New(time.Now()),
		UpdatedAt: timestamppb.New(time.Now()),
	}
}

// testStock is what the warehouse says about testProduct, with one of its two
// variants deliberately left out: a SKU inventory does not track is the ordinary
// case of something nobody has stocked yet, not an error.
func testStock() []*inventoryv1.StockItem {
	return []*inventoryv1.StockItem{
		{Sku: "SHIRT-OX-M", Available: 4, Reserved: 1},
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

// bearer signs a token for the test user, so an authenticated call reads as one
// line rather than four.
func bearer(t *testing.T, signer *auth.Signer) string {
	t.Helper()

	token, err := signer.Sign(testUserID, []string{"customer"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	return "Bearer " + token.Value
}

// decode reads a response into a typed shape, for the assertions that are about
// fields rather than about the whole body.
func decode(t *testing.T, res *httptest.ResponseRecorder, dst any) {
	t.Helper()

	if err := json.Unmarshal(res.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body %s: %v", res.Body, err)
	}
}

func testOrder() *orderv1.Order {
	return &orderv1.Order{
		Id:     "3f2504e0-4f89-41d3-9a0c-0305e82c3301",
		UserId: "9c4f3d7b-5e1a-4f4d-ac6b-80ae1d5f7b93",
		Status: orderv1.OrderStatus_ORDER_STATUS_PENDING_PAYMENT,
		Lines: []*orderv1.OrderLine{{
			Sku:       "SHIRT-BLUE-M",
			Quantity:  2,
			UnitPrice: &commonv1.Money{AmountMinor: 49900, CurrencyCode: "THB"},
		}},
		Total:         &commonv1.Money{AmountMinor: 99800, CurrencyCode: "THB"},
		ReservationId: "8b3e2c6a-4d0f-4e3c-9b5a-7f9d0c4e6a82",
		CreatedAt:     timestamppb.New(time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)),
		UpdatedAt:     timestamppb.New(time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)),
	}
}

func TestCheckoutPassesTheCartAndTheKeyThrough(t *testing.T) {
	orders := &stubOrderClient{order: testOrder()}
	srv, signer := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{}, orders)

	res := do(t, srv, http.MethodPost, "/api/v1/checkout", bearer(t, signer), `{
		"idempotencyKey": "checkout-4f2b1c8a",
		"lines": [{"sku": "SHIRT-BLUE-M", "quantity": 2}]
	}`)

	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusCreated, res.Body)
	}

	// The key has to reach the service that owns the order: this tier has no
	// database and cannot deduplicate anything itself.
	if got := orders.checkoutReq.GetIdempotencyKey(); got != "checkout-4f2b1c8a" {
		t.Errorf("idempotency key = %q, want the one the client sent", got)
	}
	if lines := orders.checkoutReq.GetLines(); len(lines) != 1 || lines[0].GetSku() != "SHIRT-BLUE-M" {
		t.Errorf("lines = %+v, want the cart", lines)
	}

	var body struct {
		Order struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Total  struct {
				AmountMinor int64 `json:"amountMinor"`
			} `json:"total"`
			ReservationID string `json:"reservationId"`
		} `json:"order"`
	}
	decode(t, res, &body)

	if body.Order.Status != "pendingPayment" {
		t.Errorf("status = %q, want %q", body.Order.Status, "pendingPayment")
	}
	if body.Order.Total.AmountMinor != 99800 {
		t.Errorf("total = %d, want %d", body.Order.Total.AmountMinor, 99800)
	}
	// Machinery, not a fact about the purchase. A client that learned to read
	// it would be a client the saga cannot be changed underneath.
	if body.Order.ReservationID != "" {
		t.Errorf("reservationId = %q, want it absent from the response", body.Order.ReservationID)
	}
}

func TestCheckoutRefusesAnUnsignedRequest(t *testing.T) {
	orders := &stubOrderClient{order: testOrder()}
	srv, _ := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{}, orders)

	res := do(t, srv, http.MethodPost, "/api/v1/checkout", "", `{
		"idempotencyKey": "checkout-4f2b1c8a",
		"lines": [{"sku": "SHIRT-BLUE-M", "quantity": 2}]
	}`)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusUnauthorized, res.Body)
	}
	// Nothing may reach the order service on an anonymous call.
	if orders.checkoutReq != nil {
		t.Error("checkout was forwarded without a verified caller")
	}
}

func TestCheckoutRejectsACartThisAPIWillNotAccept(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no idempotency key", `{"lines": [{"sku": "SHIRT-BLUE-M", "quantity": 2}]}`},
		{"key too short", `{"idempotencyKey": "short", "lines": [{"sku": "S-1", "quantity": 1}]}`},
		{"no lines", `{"idempotencyKey": "checkout-4f2b1c8a", "lines": []}`},
		{"zero quantity", `{"idempotencyKey": "checkout-4f2b1c8a", "lines": [{"sku": "S-1", "quantity": 0}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orders := &stubOrderClient{order: testOrder()}
			srv, signer := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{}, orders)

			res := do(t, srv, http.MethodPost, "/api/v1/checkout", bearer(t, signer), tt.body)

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusBadRequest, res.Body)
			}
			if orders.checkoutReq != nil {
				t.Error("a request this API refuses was forwarded anyway")
			}
		})
	}
}

func TestListOrdersPassesThePageThrough(t *testing.T) {
	orders := &stubOrderClient{orders: []*orderv1.Order{testOrder()}, nextPageToken: "next"}
	srv, signer := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{}, orders)

	res := do(t, srv, http.MethodGet, "/api/v1/orders?pageSize=10&pageToken=abc", bearer(t, signer), "")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusOK, res.Body)
	}
	if got := orders.listReq.GetPageSize(); got != 10 {
		t.Errorf("page size = %d, want %d", got, 10)
	}
	// The cursor is the order service's and is passed back unread.
	if got := orders.listReq.GetPageToken(); got != "abc" {
		t.Errorf("page token = %q, want %q", got, "abc")
	}

	var body struct {
		Orders        []struct{} `json:"orders"`
		NextPageToken string     `json:"nextPageToken"`
	}
	decode(t, res, &body)

	if len(body.Orders) != 1 || body.NextPageToken != "next" {
		t.Errorf("body = %+v, want one order and the next token", body)
	}
}

func TestGetOrderRefusesAnIDThisAPIWillNotAccept(t *testing.T) {
	orders := &stubOrderClient{order: testOrder()}
	srv, signer := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{}, orders)

	// Checked here rather than left to the order service, which would also
	// refuse it: a rejection from downstream arrives as a bare 400 where every
	// other bad request in this API carries error.fields naming what was wrong.
	res := do(t, srv, http.MethodGet, "/api/v1/orders/not-a-uuid", bearer(t, signer), "")

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", res.Code, http.StatusBadRequest, res.Body)
	}
	if orders.getReq != nil {
		t.Error("an id this API refuses was forwarded anyway")
	}
}
