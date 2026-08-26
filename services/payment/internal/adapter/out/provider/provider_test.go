package provider_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/provider"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/fakeprovider"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out"
)

const secret = "a-test-secret"

const (
	paymentID = domain.PaymentID("3f5b8e2a-1c9d-4a6e-b3f0-2d7c9e4a1b5f")
	orderID   = domain.OrderID("8b3e2c6a-4d0f-4e3c-9b5a-7f9d0c4e6a82")
)

// webhook is what the fake delivered, as it arrived.
type webhook struct {
	payload   []byte
	signature string
}

// setup runs the fake provider over real HTTP, with a callback endpoint that
// keeps whatever it is sent.
//
// The whole adapter is exercised against it — a real listener, real JSON, a
// real HMAC — because the failures this package has to get right are transport
// failures, and a stubbed RoundTripper would assert none of them.
func setup(t *testing.T) (*provider.Gateway, <-chan webhook) {
	t.Helper()

	delivered := make(chan webhook, 4)

	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the delivered webhook: %v", err)

			return
		}

		delivered <- webhook{payload: body, signature: r.Header.Get(fakeprovider.SignatureHeader)}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(callback.Close)

	fake := fakeprovider.New(fakeprovider.Config{
		CallbackURL:  callback.URL,
		Secret:       config.Secret(secret),
		WebhookDelay: 10 * time.Millisecond,
	}, zap.NewNop())

	api := httptest.NewServer(fake.Routes())
	t.Cleanup(api.Close)

	return provider.New(provider.Config{
		BaseURL: api.URL,
		Secret:  config.Secret(secret),
		Timeout: 2 * time.Second,
	}), delivered
}

func charge(t *testing.T, amountMinor int64) out.ChargeCommand {
	t.Helper()

	currency, err := domain.NewCurrencyCode("THB")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	amount, err := domain.NewMoney(amountMinor, currency)
	if err != nil {
		t.Fatalf("NewMoney() error = %v, want nil", err)
	}

	return out.ChargeCommand{PaymentID: paymentID, OrderID: orderID, Amount: amount, Method: "card"}
}

// An amount ending 00: a card charge with no 3-D Secure step settles during the
// call.
func TestChargeSettlesDuringTheCall(t *testing.T) {
	gateway, _ := setup(t)

	got, err := gateway.Charge(context.Background(), charge(t, 99800))
	if err != nil {
		t.Fatalf("Charge() error = %v, want nil", err)
	}
	if got.Status != domain.StatusSucceeded {
		t.Errorf("status = %q, want %q", got.Status, domain.StatusSucceeded)
	}
	if got.Reference == "" {
		t.Error("reference is empty, want the provider's own name for the charge")
	}
}

// An amount ending 01: the easy failure, and the only one a real sandbox
// offers.
func TestChargeReportsADecline(t *testing.T) {
	gateway, _ := setup(t)

	got, err := gateway.Charge(context.Background(), charge(t, 99801))
	if err != nil {
		t.Fatalf("Charge() error = %v, want nil — a decline is an answer, not a failure", err)
	}
	if got.Status != domain.StatusFailed {
		t.Errorf("status = %q, want %q", got.Status, domain.StatusFailed)
	}
	if got.FailureReason != "card_declined" {
		t.Errorf("failure reason = %q, want %q", got.FailureReason, "card_declined")
	}
}

// An amount ending 02: the ordinary shape. The call is accepted, the customer
// has somewhere to go, and the outcome arrives as a webhook.
func TestChargeAcceptsAndSettlesByWebhook(t *testing.T) {
	gateway, delivered := setup(t)

	got, err := gateway.Charge(context.Background(), charge(t, 99802))
	if err != nil {
		t.Fatalf("Charge() error = %v, want nil", err)
	}
	if got.Status != domain.StatusPending {
		t.Errorf("status = %q, want %q", got.Status, domain.StatusPending)
	}
	if got.NextActionURL == "" {
		t.Error("next action URL is empty, want somewhere to send the customer")
	}

	sent := receive(t, delivered)

	callback, err := gateway.ParseCallback(sent.payload, sent.signature)
	if err != nil {
		t.Fatalf("ParseCallback() error = %v, want nil", err)
	}
	if callback.PaymentID != paymentID {
		t.Errorf("callback payment id = %q, want %q", callback.PaymentID, paymentID)
	}
	if callback.Status != domain.StatusSucceeded {
		t.Errorf("callback status = %q, want %q", callback.Status, domain.StatusSucceeded)
	}
}

// An amount ending 03: at-least-once is what a provider gives you, and both
// deliveries have to verify and parse the same way. What the second one does to
// the aggregate is the domain's business, and it does nothing.
func TestAWebhookDeliveredTwiceVerifiesBothTimes(t *testing.T) {
	gateway, delivered := setup(t)

	if _, err := gateway.Charge(context.Background(), charge(t, 99803)); err != nil {
		t.Fatalf("Charge() error = %v, want nil", err)
	}

	first := receive(t, delivered)
	second := receive(t, delivered)

	for i, sent := range []webhook{first, second} {
		callback, err := gateway.ParseCallback(sent.payload, sent.signature)
		if err != nil {
			t.Fatalf("ParseCallback() on delivery %d error = %v, want nil", i+1, err)
		}
		if callback.Status != domain.StatusSucceeded {
			t.Errorf("delivery %d status = %q, want %q", i+1, callback.Status, domain.StatusSucceeded)
		}
	}
}

// An amount ending 04: the webhook is delivered before the call that caused it
// is answered. This is the ordering that finds a settle path which trusted the
// copy it had just written.
func TestAWebhookCanArriveBeforeTheResponse(t *testing.T) {
	gateway, delivered := setup(t)

	// The delivery is already waiting by the time Charge returns, which is what
	// a non-blocking read proves.
	got, err := gateway.Charge(context.Background(), charge(t, 99804))
	if err != nil {
		t.Fatalf("Charge() error = %v, want nil", err)
	}

	select {
	case sent := <-delivered:
		callback, err := gateway.ParseCallback(sent.payload, sent.signature)
		if err != nil {
			t.Fatalf("ParseCallback() error = %v, want nil", err)
		}
		if callback.Reference != got.Reference {
			t.Errorf("callback names %q, want the charge %q the call returned", callback.Reference, got.Reference)
		}
	default:
		t.Fatal("no webhook had arrived by the time Charge returned, want it to have overtaken the response")
	}
}

// An amount ending 05: the provider never answers, and the caller's deadline is
// what ends the call. The charge may or may not have been made, and nothing
// here pretends to know — which is why the attempt is written before this call
// rather than after it.
func TestChargeGivesUpOnAProviderThatNeverAnswers(t *testing.T) {
	gateway, _ := setup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := gateway.Charge(ctx, charge(t, 99805))
	if err == nil {
		t.Fatal("Charge() error = nil, want the deadline to have ended it")
	}
	if errorx.Reason(err) != "PROVIDER_UNAVAILABLE" {
		t.Errorf("reason = %q, want %q", errorx.Reason(err), "PROVIDER_UNAVAILABLE")
	}
}

// The retry after an ambiguous timeout has to reach the charge that was already
// made rather than make a second one, and the payment id is what makes that
// true.
func TestChargeIsIdempotentOnThePaymentID(t *testing.T) {
	gateway, _ := setup(t)
	ctx := context.Background()

	first, err := gateway.Charge(ctx, charge(t, 99800))
	if err != nil {
		t.Fatalf("first Charge() error = %v, want nil", err)
	}

	second, err := gateway.Charge(ctx, charge(t, 99800))
	if err != nil {
		t.Fatalf("second Charge() error = %v, want nil", err)
	}

	if first.Reference != second.Reference {
		t.Errorf("second charge = %q, want the first one %q back", second.Reference, first.Reference)
	}
}

func TestRetrieveReadsBackACharge(t *testing.T) {
	gateway, _ := setup(t)
	ctx := context.Background()

	made, err := gateway.Charge(ctx, charge(t, 99802))
	if err != nil {
		t.Fatalf("Charge() error = %v, want nil", err)
	}

	got, err := gateway.Retrieve(ctx, made.Reference)
	if err != nil {
		t.Fatalf("Retrieve() error = %v, want nil", err)
	}
	if got.Reference != made.Reference {
		t.Errorf("reference = %q, want %q", got.Reference, made.Reference)
	}
	if got.NextActionURL == "" {
		t.Error("next action URL is empty, want the pending charge's own")
	}
}

// The path a replay depends on: a charge the provider has never heard of comes
// back as not found rather than as a broken call.
func TestRetrieveReportsAChargeTheProviderDoesNotHave(t *testing.T) {
	gateway, _ := setup(t)

	_, err := gateway.Retrieve(context.Background(), "chrg_never_made")
	if !errors.Is(err, domain.ErrPaymentNotFound) {
		t.Fatalf("Retrieve() error = %v, want %v", err, domain.ErrPaymentNotFound)
	}
}

func TestParseCallbackRefusesWhatDoesNotVerify(t *testing.T) {
	gateway, delivered := setup(t)

	if _, err := gateway.Charge(context.Background(), charge(t, 99802)); err != nil {
		t.Fatalf("Charge() error = %v, want nil", err)
	}

	sent := receive(t, delivered)

	tests := []struct {
		name      string
		payload   []byte
		signature string
	}{
		// The signature is over exactly these bytes, so a body edited in
		// transit no longer verifies — which is the whole reason the payload
		// travels as bytes and is never decoded and re-encoded on the way.
		{"a tampered body", append(sent.payload, ' '), sent.signature},
		{"somebody else's signature", sent.payload, provider.Sign("not-the-secret", sent.payload)},
		{"no signature", sent.payload, ""},
		{"a signature that is not hex", sent.payload, "zzzz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gateway.ParseCallback(tt.payload, tt.signature)
			if errorx.KindOf(err) != errorx.KindUnauthenticated {
				t.Fatalf("ParseCallback() error kind = %v, want %v", errorx.KindOf(err), errorx.KindUnauthenticated)
			}
			if errorx.Reason(err) != "CALLBACK_SIGNATURE_INVALID" {
				t.Errorf("reason = %q, want %q", errorx.Reason(err), "CALLBACK_SIGNATURE_INVALID")
			}
		})
	}
}

// A body that verifies but names nothing this system issued is refused rather
// than turned into a lookup that could not succeed.
func TestParseCallbackRefusesAKeyThatIsNotOurs(t *testing.T) {
	gateway, _ := setup(t)

	payload := []byte(`{"id":"chrg_x","idempotency_key":"not-a-uuid","status":"succeeded"}`)

	_, err := gateway.ParseCallback(payload, provider.Sign(secret, payload))
	if errorx.KindOf(err) != errorx.KindInvalidInput {
		t.Fatalf("ParseCallback() error kind = %v, want %v", errorx.KindOf(err), errorx.KindInvalidInput)
	}
}

// A provider that grew a state this build does not know about is something
// somebody has to look at. Filing it under "not yet settled" is how a charge
// stays pending forever with nothing reporting why.
func TestParseCallbackRefusesAnUnknownStatus(t *testing.T) {
	gateway, _ := setup(t)

	payload := []byte(`{"id":"chrg_x","idempotency_key":"` + paymentID.String() + `","status":"disputed"}`)

	_, err := gateway.ParseCallback(payload, provider.Sign(secret, payload))
	if err == nil {
		t.Fatal("ParseCallback() error = nil, want an unknown status refused")
	}
	if errorx.Reason(err) != "PROVIDER_MALFORMED" {
		t.Errorf("reason = %q, want %q", errorx.Reason(err), "PROVIDER_MALFORMED")
	}
}

func receive(t *testing.T, delivered <-chan webhook) webhook {
	t.Helper()

	select {
	case sent := <-delivered:
		return sent
	case <-time.After(3 * time.Second):
		t.Fatal("no webhook was delivered")

		return webhook{}
	}
}
