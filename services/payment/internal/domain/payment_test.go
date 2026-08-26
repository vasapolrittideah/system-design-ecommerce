package domain_test

import (
	"errors"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
)

const (
	orderID   = domain.OrderID("11111111-1111-1111-1111-111111111111")
	userID    = domain.UserID("22222222-2222-2222-2222-222222222222")
	reference = domain.ProviderReference("pi_3Nk9Xy2eZvKYlo2C0abcdefg")
)

func thb(t *testing.T, amountMinor int64) domain.Money {
	t.Helper()

	currency, err := domain.NewCurrencyCode("thb")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	money, err := domain.NewMoney(amountMinor, currency)
	if err != nil {
		t.Fatalf("NewMoney() error = %v, want nil", err)
	}

	return money
}

func payment(t *testing.T) *domain.Payment {
	t.Helper()

	built, err := domain.NewPayment(domain.NewPaymentID(), orderID, userID, thb(t, 99800), "card")
	if err != nil {
		t.Fatalf("NewPayment() error = %v, want nil", err)
	}

	return built
}

func TestNewPaymentStartsPending(t *testing.T) {
	p := payment(t)

	if p.Status() != domain.StatusPending {
		t.Errorf("status = %q, want %q", p.Status(), domain.StatusPending)
	}
	if p.OrderID() != orderID || p.UserID() != userID {
		t.Errorf("payment = %s / %s, want %s / %s", p.OrderID(), p.UserID(), orderID, userID)
	}
	if p.Amount().AmountMinor() != 99800 {
		t.Errorf("amount = %d, want 99800", p.Amount().AmountMinor())
	}
	if p.Version() != 1 {
		t.Errorf("version = %d, want 1", p.Version())
	}
	// The provider has not answered yet, and that state has to be
	// representable: the row is written before the call so that a call which
	// times out has left a record of itself.
	if p.ProviderReference() != "" {
		t.Errorf("provider reference = %q, want it empty until the provider answers", p.ProviderReference())
	}
	// The timestamps are the database's; the aggregate has none until the
	// insert returns.
	if !p.CreatedAt().IsZero() {
		t.Error("created_at is set, want the zero time until the row is written")
	}
}

// An attempt that has begun is not a fact anybody outside this service can act
// on: the order is already waiting for payment and knows it.
func TestNewPaymentRaisesNothing(t *testing.T) {
	if pulled := payment(t).PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestNewPaymentRejectsWhatIsNotAnAttempt(t *testing.T) {
	tests := []struct {
		name    string
		id      domain.PaymentID
		orderID domain.OrderID
		userID  domain.UserID
		amount  func(*testing.T) domain.Money
	}{
		{"no id", "", orderID, userID, func(t *testing.T) domain.Money { return thb(t, 100) }},
		{"no order", domain.NewPaymentID(), "", userID, func(t *testing.T) domain.Money { return thb(t, 100) }},
		{"no user", domain.NewPaymentID(), orderID, "", func(t *testing.T) domain.Money { return thb(t, 100) }},
		{"no currency", domain.NewPaymentID(), orderID, userID, func(*testing.T) domain.Money { return domain.Money{} }},
		// A charge for nothing is not a charge, and a provider asked to make one
		// answers in its own way rather than in ours.
		{"zero amount", domain.NewPaymentID(), orderID, userID, func(t *testing.T) domain.Money { return thb(t, 0) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.NewPayment(tt.id, tt.orderID, tt.userID, tt.amount(t), "")

			var invalid domain.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("NewPayment() error = %v, want a ValidationError", err)
			}
		})
	}
}

func TestSucceed(t *testing.T) {
	tests := []struct {
		name    string
		setUp   func(*testing.T, *domain.Payment)
		want    bool
		wantErr error
	}{
		{"a pending attempt settles", nil, true, nil},
		{
			name:  "a second callback changes nothing",
			setUp: func(t *testing.T, p *domain.Payment) { mustSucceed(t, p) },
			want:  false,
		},
		{
			// A provider reporting success for a charge it declined is a
			// contradiction rather than a late answer.
			name:    "a failed attempt refuses",
			setUp:   func(t *testing.T, p *domain.Payment) { mustFail(t, p, "card_declined") },
			wantErr: domain.ErrPaymentFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := payment(t)
			if tt.setUp != nil {
				tt.setUp(t, p)
			}

			moved, err := p.Succeed(reference)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Succeed() error = %v, want %v", err, tt.wantErr)
			}
			if moved != tt.want {
				t.Errorf("Succeed() = %v, want %v", moved, tt.want)
			}
			if tt.wantErr == nil && p.Status() != domain.StatusSucceeded {
				t.Errorf("status = %q, want %q", p.Status(), domain.StatusSucceeded)
			}
		})
	}
}

func TestSucceedRaisesPaymentSucceeded(t *testing.T) {
	p := payment(t)

	mustSucceed(t, p)

	pulled := p.PullEvents()
	if len(pulled) != 1 {
		t.Fatalf("PullEvents() returned %d events, want 1", len(pulled))
	}

	event, ok := pulled[0].(domain.PaymentSucceeded)
	if !ok {
		t.Fatalf("PullEvents() returned %T, want domain.PaymentSucceeded", pulled[0])
	}
	if event.EventName() != "PaymentSucceeded" {
		t.Errorf("EventName() = %q, want %q", event.EventName(), "PaymentSucceeded")
	}
	if event.PaymentID != p.ID() || event.OrderID != orderID {
		t.Errorf("event = %+v, want it to name this attempt and its order", event)
	}
	// The amount travels so a consumer can compare what was collected against
	// what was owed, and the reference so an order traces to a line in the
	// provider's dashboard without a second read.
	if !event.Amount.Equal(p.Amount()) {
		t.Errorf("event amount = %d, want %d", event.Amount.AmountMinor(), p.Amount().AmountMinor())
	}
	if event.ProviderReference != reference {
		t.Errorf("event reference = %q, want %q", event.ProviderReference, reference)
	}
}

// A provider redelivers, and raising the fact twice would tell the order
// service to mark one order paid on two separate deliveries.
func TestSucceedRaisesNothingTheSecondTime(t *testing.T) {
	p := payment(t)
	mustSucceed(t, p)
	p.PullEvents()

	mustSucceed(t, p)

	if pulled := p.PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

// A settled attempt told about a *different* charge is the same contradiction
// as two references on one attempt, and is refused rather than ignored.
func TestSucceedRefusesADifferentReferenceOnASettledAttempt(t *testing.T) {
	p := payment(t)
	mustSucceed(t, p)

	if _, err := p.Succeed("chrg_somebody_elses"); !errors.Is(err, domain.ErrProviderReferenceConflict) {
		t.Fatalf("Succeed() error = %v, want %v", err, domain.ErrProviderReferenceConflict)
	}
}

func TestSucceedRequiresAReference(t *testing.T) {
	p := payment(t)

	_, err := p.Succeed("")

	var invalid domain.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Succeed() error = %v, want a ValidationError", err)
	}
	if p.Status() != domain.StatusPending {
		t.Errorf("status = %q, want the attempt left %q", p.Status(), domain.StatusPending)
	}
}

func TestFail(t *testing.T) {
	tests := []struct {
		name    string
		setUp   func(*testing.T, *domain.Payment)
		want    bool
		wantErr error
	}{
		{"a pending attempt ends", nil, true, nil},
		{
			name:  "a second callback changes nothing",
			setUp: func(t *testing.T, p *domain.Payment) { mustFail(t, p, "card_declined") },
			want:  false,
		},
		{
			// Reversing money that has settled is a refund or a chargeback, and
			// not a transition this aggregate may make on being told the first
			// one failed.
			name:    "a succeeded attempt refuses",
			setUp:   func(t *testing.T, p *domain.Payment) { mustSucceed(t, p) },
			wantErr: domain.ErrPaymentSucceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := payment(t)
			if tt.setUp != nil {
				tt.setUp(t, p)
			}

			moved, err := p.Fail("card_declined")

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Fail() error = %v, want %v", err, tt.wantErr)
			}
			if moved != tt.want {
				t.Errorf("Fail() = %v, want %v", moved, tt.want)
			}
			if tt.wantErr == nil && p.Status() != domain.StatusFailed {
				t.Errorf("status = %q, want %q", p.Status(), domain.StatusFailed)
			}
		})
	}
}

func TestFailRaisesPaymentFailed(t *testing.T) {
	p := payment(t)

	mustFail(t, p, "card_declined")

	pulled := p.PullEvents()
	if len(pulled) != 1 {
		t.Fatalf("PullEvents() returned %d events, want 1", len(pulled))
	}

	event, ok := pulled[0].(domain.PaymentFailed)
	if !ok {
		t.Fatalf("PullEvents() returned %T, want domain.PaymentFailed", pulled[0])
	}
	if event.EventName() != "PaymentFailed" {
		t.Errorf("EventName() = %q, want %q", event.EventName(), "PaymentFailed")
	}
	if event.PaymentID != p.ID() || event.OrderID != orderID {
		t.Errorf("event = %+v, want it to name this attempt and its order", event)
	}
	if event.Reason != "card_declined" {
		t.Errorf("event reason = %q, want %q", event.Reason, "card_declined")
	}
}

// A provider that declined without saying why has still declined.
func TestFailAcceptsAnEmptyReason(t *testing.T) {
	p := payment(t)

	moved, err := p.Fail("")
	if err != nil || !moved {
		t.Fatalf("Fail(\"\") = %v, %v, want true, nil", moved, err)
	}
	if p.FailureReason() != "" {
		t.Errorf("failure reason = %q, want it empty", p.FailureReason())
	}
}

func TestFailRaisesNothingTheSecondTime(t *testing.T) {
	p := payment(t)
	mustFail(t, p, "card_declined")
	p.PullEvents()

	mustFail(t, p, "card_declined")

	if pulled := p.PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

// A refused transition raises nothing either: the attempt did not move, and an
// event says something happened.
func TestARefusedTransitionRaisesNothing(t *testing.T) {
	p := payment(t)
	mustSucceed(t, p)
	p.PullEvents()

	if _, err := p.Fail("card_declined"); err == nil {
		t.Fatal("Fail() error = nil, want a refusal on a succeeded attempt")
	}
	if pulled := p.PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestAttachProviderReference(t *testing.T) {
	p := payment(t)

	moved, err := p.AttachProviderReference(reference)
	if err != nil || !moved {
		t.Fatalf("AttachProviderReference() = %v, %v, want true, nil", moved, err)
	}
	if p.ProviderReference() != reference {
		t.Errorf("provider reference = %q, want %q", p.ProviderReference(), reference)
	}

	// The call that produces a reference is retried after an ambiguous timeout,
	// and the second answer is the same charge.
	moved, err = p.AttachProviderReference(reference)
	if err != nil || moved {
		t.Fatalf("AttachProviderReference() = %v, %v, want false, nil on a repeat", moved, err)
	}

	// Two references mean two charges, and this aggregate can only describe one.
	if _, err := p.AttachProviderReference("chrg_a_second_charge"); !errors.Is(err, domain.ErrProviderReferenceConflict) {
		t.Fatalf("AttachProviderReference() error = %v, want %v", err, domain.ErrProviderReferenceConflict)
	}
}

// Which identifier the provider chose is not a fact about the money, and the
// events that are carry the reference with them.
func TestAttachProviderReferenceRaisesNothing(t *testing.T) {
	p := payment(t)
	p.PullEvents()

	if _, err := p.AttachProviderReference(reference); err != nil {
		t.Fatalf("AttachProviderReference() error = %v, want nil", err)
	}
	if pulled := p.PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestPullEventsEmptiesTheAggregate(t *testing.T) {
	p := payment(t)
	mustSucceed(t, p)

	if len(p.PullEvents()) != 1 {
		t.Fatal("first PullEvents() returned nothing, want the settlement")
	}
	if pulled := p.PullEvents(); len(pulled) != 0 {
		t.Errorf("second PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestReconstitutePaymentRaisesNothing(t *testing.T) {
	p := payment(t)
	mustSucceed(t, p)

	// An attempt read back from storage has made no change, and announcing its
	// settlement on every read would be a fact the system hears twice.
	if pulled := domain.ReconstitutePayment(p.Snapshot()).PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestSnapshotRoundTrips(t *testing.T) {
	p := payment(t)
	mustFail(t, p, "insufficient_funds")

	back := domain.ReconstitutePayment(p.Snapshot())

	if back.ID() != p.ID() || back.OrderID() != p.OrderID() || back.UserID() != p.UserID() {
		t.Error("identifiers did not survive the round trip")
	}
	if back.Status() != p.Status() || back.FailureReason() != p.FailureReason() {
		t.Errorf("outcome = %q / %q, want %q / %q",
			back.Status(), back.FailureReason(), p.Status(), p.FailureReason())
	}
	if !back.Amount().Equal(p.Amount()) || back.Method() != p.Method() {
		t.Error("amount or method did not survive the round trip")
	}
}

// Every refusal this package raises resolves to a conflict, which is what the
// boundary turns into 409 rather than 500.
func TestConflictsDeclareTheirKind(t *testing.T) {
	for _, err := range []error{
		domain.ErrPaymentSucceeded,
		domain.ErrPaymentFailed,
		domain.ErrProviderReferenceConflict,
	} {
		kinded, ok := err.(interface{ ErrorKind() string })
		if !ok {
			t.Fatalf("%v does not declare a kind", err)
		}
		if got := kinded.ErrorKind(); got != "conflict" {
			t.Errorf("%v ErrorKind() = %q, want %q", err, got, "conflict")
		}
	}
}

func TestPaymentNotFoundDeclaresItsKind(t *testing.T) {
	kinded, ok := error(domain.ErrPaymentNotFound).(interface{ ErrorKind() string })
	if !ok {
		t.Fatal("ErrPaymentNotFound does not declare a kind")
	}
	if got := kinded.ErrorKind(); got != "not_found" {
		t.Errorf("ErrorKind() = %q, want %q", got, "not_found")
	}
}

func mustSucceed(t *testing.T, p *domain.Payment) {
	t.Helper()

	if _, err := p.Succeed(reference); err != nil {
		t.Fatalf("Succeed() error = %v, want nil", err)
	}
}

func mustFail(t *testing.T, p *domain.Payment, reason string) {
	t.Helper()

	if _, err := p.Fail(reason); err != nil {
		t.Fatalf("Fail() error = %v, want nil", err)
	}
}
