// Package provider is the driven adapter for the payment provider: it maps
// between this service's vocabulary and one provider's HTTP API, and verifies
// the webhooks that API sends.
//
// It is the only package here that knows a provider exists, and the only one
// that knows what it calls its states.
//
// The API it speaks is this system's own, implemented by the fake provider in
// cmd/fakeprovider. That is deliberate rather than a placeholder: signing with
// a real provider means writing a sibling adapter against their API and wiring
// it instead, which is what out.ProviderGateway exists to make possible. The
// fake is what the failure modes worth testing are produced from — an
// ambiguous timeout, a duplicated webhook, a webhook that overtakes the
// response — none of which a hosted sandbox will produce on demand.
package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out"
)

// maxResponseBytes bounds what is read back from the provider. A body is a
// handful of fields; anything larger is a misconfigured address answering, and
// reading it into memory is how one bad DNS entry becomes an OOM.
const maxResponseBytes = 1 << 16

// The states the provider names, and what each one means here. Translating in
// one place is the adapter's whole job: a use case that read these strings
// would be one that has to change when the shop signs with somebody else.
const (
	statusPending   = "pending"
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
)

// Config is what this adapter reads from the environment.
type Config struct {
	// BaseURL is the provider's API root, e.g. http://payment-fakeprovider:8080.
	BaseURL string `env:"BASE_URL,required"`

	// Secret signs webhooks, and is what makes a body a fact rather than
	// anything that reached the endpoint. A config.Secret rather than a string
	// so that one zap.Any("cfg", cfg) added while chasing a startup failure
	// does not leave it in a log backend for a year.
	Secret config.Secret `env:"SECRET,required"`

	// Timeout bounds one call. It sits inside the deadline the caller already
	// carries and exists for the case where there is none.
	Timeout time.Duration `env:"TIMEOUT" envDefault:"5s"`
}

// Gateway calls the payment provider over HTTP.
type Gateway struct {
	baseURL string
	secret  []byte
	client  *http.Client
}

var _ out.ProviderGateway = (*Gateway)(nil)

// New builds the gateway from its configuration.
func New(cfg Config) *Gateway {
	return &Gateway{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		secret:  []byte(cfg.Secret.Reveal()),
		client:  &http.Client{Timeout: cfg.Timeout},
	}
}

// chargeRequest is what this system sends to ask for money.
type chargeRequest struct {
	// IdempotencyKey is the attempt's own id. The provider deduplicates on it,
	// so a retry after an ambiguous timeout reaches the charge that was already
	// made rather than making a second one.
	IdempotencyKey string `json:"idempotency_key"`

	// Reference is the order, passed through so a charge can be found from an
	// order in the provider's dashboard.
	Reference string `json:"reference"`

	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Method      string `json:"method,omitempty"`
}

// chargeResponse is what the provider says about a charge, in its words.
type chargeResponse struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason,omitempty"`
	NextActionURL string `json:"next_action_url,omitempty"`
}

// callbackBody is a webhook. It carries the idempotency key back, which is what
// lets a callback name the attempt it is about without this service having to
// index on the provider's own identifier.
type callbackBody struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key"`
	Status         string `json:"status"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

// Charge asks for the money.
func (g *Gateway) Charge(ctx context.Context, cmd out.ChargeCommand) (out.ChargeResult, error) {
	body, err := json.Marshal(chargeRequest{
		IdempotencyKey: cmd.PaymentID.String(),
		Reference:      cmd.OrderID.String(),
		AmountMinor:    cmd.Amount.AmountMinor(),
		Currency:       cmd.Amount.Currency().String(),
		Method:         cmd.Method.String(),
	})
	if err != nil {
		return out.ChargeResult{}, errorx.Wrap(err, errorx.KindInternal, "encode the charge request")
	}

	var answer chargeResponse
	if err := g.call(ctx, http.MethodPost, "/v1/charges", body, &answer); err != nil {
		return out.ChargeResult{}, err
	}

	return toResult(answer)
}

// Retrieve asks what the provider now says about a charge it already knows.
func (g *Gateway) Retrieve(
	ctx context.Context,
	reference domain.ProviderReference,
) (out.ChargeResult, error) {
	var answer chargeResponse

	path := "/v1/charges/" + url.PathEscape(reference.String())
	if err := g.call(ctx, http.MethodGet, path, nil, &answer); err != nil {
		return out.ChargeResult{}, err
	}

	return toResult(answer)
}

// ParseCallback verifies a webhook's signature and returns what it says.
//
// The signature is checked before the body is parsed, and the two are one
// method because they are one decision: a body that has not been checked is not
// a fact about anything, and a shape that let a caller hold the parsed form
// first would be one somebody eventually acts on.
func (g *Gateway) ParseCallback(payload []byte, signature string) (out.Callback, error) {
	if err := g.verify(payload, signature); err != nil {
		return out.Callback{}, err
	}

	var body callbackBody
	if err := json.Unmarshal(payload, &body); err != nil {
		return out.Callback{}, errorx.Wrap(err, errorx.KindInvalidInput, "decode the callback body").
			WithReason("CALLBACK_MALFORMED")
	}

	paymentID, err := domain.ParsePaymentID(body.IdempotencyKey)
	if err != nil {
		// A callback naming something that is not one of this system's
		// identifiers is not one of this system's charges. It is refused here
		// rather than turned into a lookup that could not succeed.
		return out.Callback{}, errorx.Wrap(err, errorx.KindInvalidInput, "the callback names no attempt of ours").
			WithReason("CALLBACK_MALFORMED")
	}

	status, err := toStatus(body.Status)
	if err != nil {
		return out.Callback{}, err
	}

	reference, err := domain.ParseProviderReference(body.ID)
	if err != nil {
		return out.Callback{}, err
	}

	return out.Callback{
		PaymentID:     paymentID,
		Reference:     reference,
		Status:        status,
		FailureReason: body.FailureReason,
	}, nil
}

// verify checks the signature over exactly the bytes that arrived.
//
// hmac.Equal rather than ==, because a comparison that returns early on the
// first differing byte tells an attacker how much of a forged signature was
// right.
func (g *Gateway) verify(payload []byte, signature string) error {
	rejected := errorx.New(errorx.KindUnauthenticated, "the callback signature does not verify").
		WithReason("CALLBACK_SIGNATURE_INVALID")

	given, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return rejected
	}

	mac := hmac.New(sha256.New, g.secret)
	mac.Write(payload)

	if !hmac.Equal(given, mac.Sum(nil)) {
		return rejected
	}

	return nil
}

// call performs one request and decodes the answer.
func (g *Gateway) call(ctx context.Context, method, path string, body []byte, into any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, reader)
	if err != nil {
		return errorx.Wrap(err, errorx.KindInternal, "build the provider request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := g.client.Do(req)
	if err != nil {
		// Includes the case this service most has to survive: the call timed
		// out and the charge may or may not have been made. Nothing is guessed
		// here — the attempt is already written, and the caller decides.
		return errorx.Wrap(err, errorx.KindInternal, "call the provider").
			WithReason("PROVIDER_UNAVAILABLE")
	}
	defer func() { _ = res.Body.Close() }()

	answer, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return errorx.Wrap(err, errorx.KindInternal, "read the provider response").
			WithReason("PROVIDER_UNAVAILABLE")
	}

	if res.StatusCode == http.StatusNotFound {
		return domain.ErrPaymentNotFound
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		// The provider's own message is not passed on. It is written for
		// whoever operates the integration, not for the customer, and a 4xx
		// here means this service sent something wrong — which is this
		// service's bug rather than the caller's.
		return errorx.New(errorx.KindInternal, "the provider answered %d: %s",
			res.StatusCode, strings.TrimSpace(string(answer))).
			WithReason("PROVIDER_REJECTED")
	}

	if err := json.Unmarshal(answer, into); err != nil {
		return errorx.Wrap(err, errorx.KindInternal, "decode the provider response").
			WithReason("PROVIDER_MALFORMED")
	}

	return nil
}

// toResult turns what the provider said into this service's vocabulary.
func toResult(answer chargeResponse) (out.ChargeResult, error) {
	status, err := toStatus(answer.Status)
	if err != nil {
		return out.ChargeResult{}, err
	}

	// A provider that accepted a request without naming what it accepted has
	// left nothing to reconcile against, and passing that on would produce an
	// attempt nobody can ever ask about. It is refused here.
	reference, err := domain.ParseProviderReference(answer.ID)
	if err != nil {
		return out.ChargeResult{}, errorx.Wrap(err, errorx.KindInternal, "the provider named no charge").
			WithReason("PROVIDER_MALFORMED")
	}

	return out.ChargeResult{
		Reference:     reference,
		Status:        status,
		FailureReason: answer.FailureReason,
		NextActionURL: answer.NextActionURL,
	}, nil
}

// toStatus maps the provider's state to this service's.
//
// An unknown one is refused rather than treated as pending. A provider that
// grew a state this build does not know about is a thing somebody has to look
// at, and quietly filing it under "not yet settled" is how a charge stays
// pending forever with nothing reporting why.
func toStatus(status string) (domain.Status, error) {
	switch status {
	case statusPending:
		return domain.StatusPending, nil
	case statusSucceeded:
		return domain.StatusSucceeded, nil
	case statusFailed:
		return domain.StatusFailed, nil
	default:
		return "", errorx.New(errorx.KindInternal, "the provider reported an unknown status %q", status).
			WithReason("PROVIDER_MALFORMED")
	}
}

// Sign produces the signature this gateway will accept for a body.
//
// Exported because the fake provider signs with it: one implementation of the
// scheme rather than two that agree until somebody edits one. A real provider
// signs with its own secret and this is unused against it.
func Sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)

	return hex.EncodeToString(mac.Sum(nil))
}
