package rest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	paymentv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/payment/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
)

// The spec is the published contract for this BFF, and these tests are what
// stop it from becoming a description of an API that used to exist: a route
// mounted without an entry, a response the spec does not describe, and a field
// added to a DTO all fail here rather than reaching a client as a surprise.
//
// It is not a second implementation of the handlers. What it asserts is shape —
// which routes exist, which statuses they answer with, and which fields those
// answers carry. What each field *means* is the business of the tests beside it.
const specPath = "../../../../../../api/openapi/bff-web.yaml"

// specHost is the server the spec declares. The OpenAPI router matches on it,
// so the requests below are built as absolute URLs; chi routes on the path
// alone and does not care either way.
const specHost = "http://localhost:8000"

func TestSpecIsValid(t *testing.T) {
	loadSpec(t)
}

// A route the spec does not describe is undocumented API, and a path the spec
// describes that nothing serves is a promise this BFF does not keep. Both are
// drift, and neither shows up in a handler test.
func TestSpecDescribesExactlyTheMountedRoutes(t *testing.T) {
	doc := loadSpec(t)
	router, _ := newServer(t, &stubIdentityClient{}, &stubCatalogClient{}, &stubInventoryClient{})

	documented := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			documented[method+" "+path] = false
		}
	}

	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi reports a route group's paths with the trailing slash its
		// subrouter was mounted under; the spec names them as a client writes
		// them.
		route = strings.TrimSuffix(route, "/")

		key := method + " " + route
		if _, found := documented[key]; !found {
			t.Errorf("%s is mounted but not in %s", key, filepath.Base(specPath))

			return nil
		}

		documented[key] = true

		return nil
	}

	if err := chi.Walk(router, walk); err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	for key, mounted := range documented {
		if !mounted {
			t.Errorf("%s is in %s but nothing serves it", key, filepath.Base(specPath))
		}
	}
}

// The table drives the real router and holds every answer the spec describes up
// against the one the handler gives.
func TestResponsesMatchTheSpec(t *testing.T) {
	conflict := errorx.ToGRPC(errorx.New(errorx.KindConflict, "email is already registered").
		WithReason("EMAIL_ALREADY_REGISTERED"))
	missingUser := errorx.ToGRPC(errorx.New(errorx.KindNotFound, "user not found").
		WithReason("USER_NOT_FOUND"))
	missingProduct := errorx.ToGRPC(errorx.New(errorx.KindNotFound, "product not found").
		WithReason("PRODUCT_NOT_FOUND"))
	missingOrder := errorx.ToGRPC(errorx.New(errorx.KindNotFound, "order not found").
		WithReason("ORDER_NOT_FOUND"))
	notPayable := errorx.ToGRPC(errorx.New(errorx.KindConflict, "order is not waiting for payment").
		WithReason("ORDER_NOT_PAYABLE"))
	badSignature := errorx.ToGRPC(errorx.New(errorx.KindUnauthenticated, "callback signature").
		WithReason("CALLBACK_SIGNATURE_INVALID"))

	cases := []struct {
		name string

		method        string
		path          string
		body          string
		contentType   string
		authenticated bool

		// headers are the ones no other field covers — a provider's signature,
		// which is what stands in for a token on the one endpoint no person
		// calls.
		headers map[string]string

		// authorization is a header set by hand, for the tokens no signer of
		// this test's would produce.
		authorization string

		identity  *stubIdentityClient
		catalog   *stubCatalogClient
		inventory *stubInventoryClient
		payments  *stubPaymentClient

		wantStatus int

		// specRejects says the spec must refuse this request too. It is what
		// keeps the constraints in the spec in step with the `validate` tags
		// the handler enforces: a bound that exists in one and not the other
		// shows up as a request only one of them turns away.
		specRejects bool
	}{
		{
			name:       "register",
			method:     http.MethodPost,
			path:       "/api/v1/auth/register",
			body:       `{"email": "ada@example.com", "password": "hunter2hunter2"}`,
			identity:   &stubIdentityClient{user: testUser()},
			wantStatus: http.StatusCreated,
		},
		{
			name:        "register with a password shorter than the minimum",
			method:      http.MethodPost,
			path:        "/api/v1/auth/register",
			body:        `{"email": "ada@example.com", "password": "short"}`,
			identity:    &stubIdentityClient{},
			wantStatus:  http.StatusBadRequest,
			specRejects: true,
		},
		{
			name:        "register with a field the API does not have",
			method:      http.MethodPost,
			path:        "/api/v1/auth/register",
			body:        `{"email": "ada@example.com", "password": "hunter2hunter2", "isAdmin": true}`,
			identity:    &stubIdentityClient{},
			wantStatus:  http.StatusBadRequest,
			specRejects: true,
		},
		{
			name:        "register with an empty body",
			method:      http.MethodPost,
			path:        "/api/v1/auth/register",
			identity:    &stubIdentityClient{},
			wantStatus:  http.StatusBadRequest,
			specRejects: true,
		},
		{
			name:        "register with a body that is not JSON at all",
			method:      http.MethodPost,
			path:        "/api/v1/auth/register",
			body:        `{"email": "ada@example.com",`,
			identity:    &stubIdentityClient{},
			wantStatus:  http.StatusBadRequest,
			specRejects: true,
		},
		{
			name:        "register with a body this API cannot parse",
			method:      http.MethodPost,
			path:        "/api/v1/auth/register",
			body:        `email=ada@example.com`,
			contentType: "text/plain",
			identity:    &stubIdentityClient{},
			wantStatus:  http.StatusUnsupportedMediaType,
			specRejects: true,
		},
		{
			name:        "register with a body over the limit",
			method:      http.MethodPost,
			path:        "/api/v1/auth/register",
			body:        `{"email": "ada@example.com", "password": "` + strings.Repeat("x", 1<<20) + `"}`,
			identity:    &stubIdentityClient{},
			wantStatus:  http.StatusRequestEntityTooLarge,
			specRejects: true,
		},
		{
			name:       "register an email somebody already has",
			method:     http.MethodPost,
			path:       "/api/v1/auth/register",
			body:       `{"email": "ada@example.com", "password": "hunter2hunter2"}`,
			identity:   &stubIdentityClient{err: conflict},
			wantStatus: http.StatusConflict,
		},
		{
			name:       "login",
			method:     http.MethodPost,
			path:       "/api/v1/auth/login",
			body:       `{"email": "ada@example.com", "password": "hunter2"}`,
			identity:   &stubIdentityClient{user: testUser()},
			wantStatus: http.StatusOK,
		},
		{
			name:   "login with the wrong password",
			method: http.MethodPost,
			path:   "/api/v1/auth/login",
			body:   `{"email": "ada@example.com", "password": "wrong-password"}`,
			identity: &stubIdentityClient{
				err: errorx.ToGRPC(errorx.New(errorx.KindUnauthenticated, "invalid credentials").
					WithReason("INVALID_CREDENTIALS")),
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "login while identity is broken",
			method:     http.MethodPost,
			path:       "/api/v1/auth/login",
			body:       `{"email": "ada@example.com", "password": "hunter2"}`,
			identity:   &stubIdentityClient{err: status.Error(codes.Internal, "pq: relation does not exist")},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "refresh",
			method:     http.MethodPost,
			path:       "/api/v1/auth/refresh",
			body:       `{"refreshToken": "opaque-refresh-token"}`,
			identity:   &stubIdentityClient{},
			wantStatus: http.StatusOK,
		},
		{
			name:   "refresh a token that was already spent",
			method: http.MethodPost,
			path:   "/api/v1/auth/refresh",
			body:   `{"refreshToken": "opaque-refresh-token"}`,
			identity: &stubIdentityClient{
				err: errorx.ToGRPC(errorx.New(errorx.KindUnauthenticated, "refresh").
					WithReason("REFRESH_TOKEN_INVALID")),
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "logout",
			method:     http.MethodPost,
			path:       "/api/v1/auth/logout",
			body:       `{"refreshToken": "opaque-refresh-token"}`,
			identity:   &stubIdentityClient{},
			wantStatus: http.StatusNoContent,
		},
		{
			name:          "me",
			method:        http.MethodGet,
			path:          "/api/v1/me",
			authenticated: true,
			identity:      &stubIdentityClient{user: testUser()},
			wantStatus:    http.StatusOK,
		},
		{
			name:          "me with a token this service did not sign",
			method:        http.MethodGet,
			path:          "/api/v1/me",
			authorization: "Bearer not.a.token",
			identity:      &stubIdentityClient{},
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:       "me without a token",
			method:     http.MethodGet,
			path:       "/api/v1/me",
			identity:   &stubIdentityClient{},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "me for an account that is gone",
			method:        http.MethodGet,
			path:          "/api/v1/me",
			authenticated: true,
			identity:      &stubIdentityClient{err: missingUser},
			wantStatus:    http.StatusNotFound,
		},
		{
			name:   "products",
			method: http.MethodGet,
			path:   "/api/v1/products?category=clothing/shirts&pageSize=20",
			catalog: &stubCatalogClient{
				products:      []*catalogv1.Product{testProduct()},
				nextPageToken: "b3BhcXVl",
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "products, last page",
			method:     http.MethodGet,
			path:       "/api/v1/products",
			catalog:    &stubCatalogClient{},
			wantStatus: http.StatusOK,
		},
		{
			name:        "products past the page size cap",
			method:      http.MethodGet,
			path:        "/api/v1/products?pageSize=500",
			catalog:     &stubCatalogClient{},
			wantStatus:  http.StatusBadRequest,
			specRejects: true,
		},
		{
			name:       "products while the catalog is unreachable",
			method:     http.MethodGet,
			path:       "/api/v1/products",
			catalog:    &stubCatalogClient{err: status.Error(codes.Unavailable, "connection refused")},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "products while the catalog is too slow",
			method:     http.MethodGet,
			path:       "/api/v1/products",
			catalog:    &stubCatalogClient{err: status.Error(codes.DeadlineExceeded, "context deadline exceeded")},
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "one product",
			method:     http.MethodGet,
			path:       "/api/v1/products/" + testProductID,
			catalog:    &stubCatalogClient{product: testProduct()},
			inventory:  &stubInventoryClient{items: testStock()},
			wantStatus: http.StatusOK,
		},
		{
			name:   "one product while the warehouse is unreachable",
			method: http.MethodGet,
			path:   "/api/v1/products/" + testProductID,
			// The page is still the answer: availability is the optional half
			// of it, and a 503 here would take the shop down with the
			// warehouse.
			catalog:    &stubCatalogClient{product: testProduct()},
			inventory:  &stubInventoryClient{err: status.Error(codes.Unavailable, "connection refused")},
			wantStatus: http.StatusOK,
		},
		{
			name:        "one product under an id that is not a uuid",
			method:      http.MethodGet,
			path:        "/api/v1/products/oxford-shirt",
			wantStatus:  http.StatusBadRequest,
			specRejects: true,
		},
		{
			name:       "one product nobody published",
			method:     http.MethodGet,
			path:       "/api/v1/products/" + testProductID,
			catalog:    &stubCatalogClient{err: missingProduct},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "one product while the catalog is unreachable",
			method:     http.MethodGet,
			path:       "/api/v1/products/" + testProductID,
			catalog:    &stubCatalogClient{err: status.Error(codes.Unavailable, "connection refused")},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:          "start a payment",
			method:        http.MethodPost,
			path:          "/api/v1/orders/" + testProductID + "/payment",
			body:          `{"idempotencyKey": "pay-4f2b1c8a", "method": "promptpay"}`,
			authenticated: true,
			payments: &stubPaymentClient{
				payment:       testPayment(paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING),
				nextActionURL: "https://provider.test/3ds/b7c0c5d2",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:          "start a payment the provider declined",
			method:        http.MethodPost,
			path:          "/api/v1/orders/" + testProductID + "/payment",
			body:          `{"idempotencyKey": "pay-4f2b1c8a"}`,
			authenticated: true,
			payments:      &stubPaymentClient{payment: testPayment(paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED)},
			wantStatus:    http.StatusCreated,
		},
		{
			name:          "start a payment without a key",
			method:        http.MethodPost,
			path:          "/api/v1/orders/" + testProductID + "/payment",
			body:          `{}`,
			authenticated: true,
			wantStatus:    http.StatusBadRequest,
			specRejects:   true,
		},
		{
			name:          "start a payment under an order id that is not a uuid",
			method:        http.MethodPost,
			path:          "/api/v1/orders/not-a-uuid/payment",
			body:          `{"idempotencyKey": "pay-4f2b1c8a"}`,
			authenticated: true,
			wantStatus:    http.StatusBadRequest,
			specRejects:   true,
		},
		{
			name:       "start a payment without a token",
			method:     http.MethodPost,
			path:       "/api/v1/orders/" + testProductID + "/payment",
			body:       `{"idempotencyKey": "pay-4f2b1c8a"}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "start a payment for an order that is not the caller's",
			method:        http.MethodPost,
			path:          "/api/v1/orders/" + testProductID + "/payment",
			body:          `{"idempotencyKey": "pay-4f2b1c8a"}`,
			authenticated: true,
			payments:      &stubPaymentClient{err: missingOrder},
			wantStatus:    http.StatusNotFound,
		},
		{
			name:          "start a payment for an order nobody may pay for any more",
			method:        http.MethodPost,
			path:          "/api/v1/orders/" + testProductID + "/payment",
			body:          `{"idempotencyKey": "pay-4f2b1c8a"}`,
			authenticated: true,
			payments:      &stubPaymentClient{err: notPayable},
			wantStatus:    http.StatusConflict,
		},
		{
			name:       "a provider's webhook",
			method:     http.MethodPost,
			path:       "/api/v1/webhooks/payment",
			body:       `{"id": "chrg_b7c0c5d2", "status": "succeeded"}`,
			headers:    map[string]string{"X-Provider-Signature": "b2f1c0"},
			payments:   &stubPaymentClient{payment: testPayment(paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED)},
			wantStatus: http.StatusNoContent,
		},
		{
			name:        "a webhook nobody signed",
			method:      http.MethodPost,
			path:        "/api/v1/webhooks/payment",
			body:        `{"id": "chrg_b7c0c5d2", "status": "succeeded"}`,
			wantStatus:  http.StatusBadRequest,
			specRejects: true,
		},
		{
			name:       "a webhook signed by somebody else",
			method:     http.MethodPost,
			path:       "/api/v1/webhooks/payment",
			body:       `{"id": "chrg_b7c0c5d2", "status": "succeeded"}`,
			headers:    map[string]string{"X-Provider-Signature": "0000"},
			payments:   &stubPaymentClient{err: badSignature},
			wantStatus: http.StatusUnauthorized,
		},
	}

	doc := loadSpec(t)

	specRouter, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			identity := tc.identity
			if identity == nil {
				identity = &stubIdentityClient{}
			}

			catalog := tc.catalog
			if catalog == nil {
				catalog = &stubCatalogClient{}
			}

			inventory := tc.inventory
			if inventory == nil {
				inventory = &stubInventoryClient{}
			}

			payments := tc.payments
			if payments == nil {
				payments = &stubPaymentClient{}
			}

			srv, signer := newServer(t, identity, catalog, inventory, backends{payments: payments})

			authorization := tc.authorization
			if tc.authenticated {
				token, err := signer.Sign(testUserID, []string{"customer"})
				if err != nil {
					t.Fatalf("Sign() error = %v", err)
				}

				authorization = "Bearer " + token.Value
			}

			newRequest := func() *http.Request {
				req := httptest.NewRequest(tc.method, specHost+tc.path, strings.NewReader(tc.body))
				if tc.body != "" {
					contentType := tc.contentType
					if contentType == "" {
						contentType = "application/json"
					}

					req.Header.Set("Content-Type", contentType)
				}
				if authorization != "" {
					req.Header.Set("Authorization", authorization)
				}
				for name, value := range tc.headers {
					req.Header.Set(name, value)
				}

				return req
			}

			res := httptest.NewRecorder()
			srv.ServeHTTP(res, newRequest())

			if res.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", res.Code, tc.wantStatus, res.Body)
			}

			// The request the spec sees is a second copy: validating one reads
			// its body, and the handler has already consumed the first.
			validated := newRequest()

			route, pathParams, err := specRouter.FindRoute(validated)
			if err != nil {
				t.Fatalf("FindRoute(%s %s) error = %v", tc.method, tc.path, err)
			}

			input := &openapi3filter.RequestValidationInput{
				Request:    validated,
				PathParams: pathParams,
				Route:      route,
				Options:    &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
			}

			switch err := openapi3filter.ValidateRequest(context.Background(), input); {
			case tc.specRejects && err == nil:
				t.Error("the spec accepts a request the handler refused — a bound is missing from one of them")
			case !tc.specRejects && err != nil:
				t.Errorf("the spec refuses a request the handler accepted: %v", err)
			}

			assertResponseMatchesSpec(t, input, route, res)
		})
	}
}

// assertResponseMatchesSpec checks the answer against the operation that
// produced it: the status is one the spec describes, the body validates against
// its schema, and every field in it is documented.
func assertResponseMatchesSpec(
	t *testing.T,
	input *openapi3filter.RequestValidationInput,
	route *routers.Route,
	res *httptest.ResponseRecorder,
) {
	t.Helper()

	err := openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input,
		Status:                 res.Code,
		Header:                 res.Header(),
		Body:                   io.NopCloser(bytes.NewReader(res.Body.Bytes())),
		Options: &openapi3filter.Options{
			// Without this an undocumented status passes silently, which is
			// most of what this test is here to catch.
			IncludeResponseStatus: true,
			MultiError:            true,
		},
	})
	if err != nil {
		t.Errorf("response does not match the spec: %v (body %s)", err, res.Body)
	}

	response := route.Operation.Responses.Status(res.Code)
	if response == nil || response.Value == nil {
		return
	}

	media := response.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		return
	}

	var body any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", res.Body, err)
	}

	assertFieldsAreDocumented(t, "", []*openapi3.Schema{media.Schema.Value}, body)
}

// assertFieldsAreDocumented reports a field the response carries and no schema
// describing it names.
//
// Schema validation alone would not: JSON Schema permits properties it has not
// heard of, which is the right default for a client — a field added to a
// response should not break one — and the wrong default for this test, where an
// undocumented field is precisely the drift being hunted. The strictness belongs
// here rather than as `additionalProperties: false` in the spec, which would
// tell every client the opposite.
//
// It takes a set of schemas rather than one because an error body is described
// by two at once: the shared shape, and the branch narrowing `error.code` to
// what this response can answer with. A field named by either is documented.
func assertFieldsAreDocumented(t *testing.T, path string, schemas []*openapi3.Schema, value any) {
	t.Helper()

	described := flatten(schemas)

	switch typed := value.(type) {
	case map[string]any:
		for name, field := range typed {
			children := propertySchemas(described, name)
			if len(children) == 0 {
				t.Errorf("the response carries %s, which the spec does not describe", join(path, name))

				continue
			}

			assertFieldsAreDocumented(t, join(path, name), children, field)
		}
	case []any:
		var items []*openapi3.Schema
		for _, schema := range described {
			if schema.Items != nil && schema.Items.Value != nil {
				items = append(items, schema.Items.Value)
			}
		}

		for i, item := range typed {
			assertFieldsAreDocumented(t, fmt.Sprintf("%s[%d]", path, i), items, item)
		}
	}
}

// flatten expands the two compositions this spec uses into the schemas that
// actually name properties: allOf, which refines a shared shape, and oneOf,
// which is how a nullable object is written in 3.1.
func flatten(schemas []*openapi3.Schema) []*openapi3.Schema {
	var described []*openapi3.Schema

	for _, schema := range schemas {
		if schema == nil {
			continue
		}

		var branches []*openapi3.Schema
		for _, branch := range append(append([]*openapi3.SchemaRef{}, schema.AllOf...), schema.OneOf...) {
			if branch.Value != nil {
				branches = append(branches, branch.Value)
			}
		}

		described = append(described, flatten(branches)...)

		if len(schema.Properties) > 0 || schema.AdditionalProperties.Schema != nil || schema.Items != nil {
			described = append(described, schema)
		}
	}

	return described
}

// propertySchemas returns every schema describing the named field, which is
// none when nothing documents it.
func propertySchemas(described []*openapi3.Schema, name string) []*openapi3.Schema {
	var children []*openapi3.Schema

	for _, schema := range described {
		if property, found := schema.Properties[name]; found && property.Value != nil {
			children = append(children, property.Value)

			continue
		}

		if extra := schema.AdditionalProperties.Schema; extra != nil && extra.Value != nil {
			children = append(children, extra.Value)
		}
	}

	return children
}

func join(path, name string) string {
	if path == "" {
		return name
	}

	return path + "." + name
}

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false

	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load %s: %v", specPath, err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("%s is not a valid OpenAPI document: %v", specPath, err)
	}

	return doc
}
