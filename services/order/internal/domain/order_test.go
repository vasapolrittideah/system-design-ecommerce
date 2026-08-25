package domain_test

import (
	"errors"
	"math"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

const (
	userID        = domain.UserID("11111111-1111-1111-1111-111111111111")
	reservationID = domain.ReservationID("22222222-2222-2222-2222-222222222222")
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

func line(t *testing.T, sku string, quantity int, unitPrice domain.Money) domain.OrderLine {
	t.Helper()

	parsed, err := domain.NewSKU(sku)
	if err != nil {
		t.Fatalf("NewSKU(%q) error = %v, want nil", sku, err)
	}

	built, err := domain.NewOrderLine(parsed, quantity, unitPrice)
	if err != nil {
		t.Fatalf("NewOrderLine() error = %v, want nil", err)
	}

	return built
}

func order(t *testing.T, lines ...domain.OrderLine) *domain.Order {
	t.Helper()

	if len(lines) == 0 {
		lines = []domain.OrderLine{line(t, "SHIRT-BLUE-M", 2, thb(t, 49900))}
	}

	built, err := domain.NewOrder(domain.NewOrderID(), userID, reservationID, lines)
	if err != nil {
		t.Fatalf("NewOrder() error = %v, want nil", err)
	}

	return built
}

func TestNewOrderTotalsItsLines(t *testing.T) {
	o := order(t,
		line(t, "SHIRT-BLUE-M", 2, thb(t, 49900)),
		line(t, "MUG-01", 3, thb(t, 15000)),
	)

	// 2 x 499.00 + 3 x 150.00
	const want = 2*49900 + 3*15000
	if got := o.Total().AmountMinor(); got != want {
		t.Errorf("total = %d, want %d", got, want)
	}
	if got := o.Total().Currency().String(); got != "THB" {
		t.Errorf("total currency = %q, want %q", got, "THB")
	}
}

func TestNewOrderStartsPendingPayment(t *testing.T) {
	o := order(t)

	// The entry call returns as soon as the order is persisted in a pending
	// state; anything else would hold the caller open for a payment provider.
	if got := o.Status(); got != domain.StatusPendingPayment {
		t.Errorf("status = %q, want %q", got, domain.StatusPendingPayment)
	}
	if got := o.Version(); got != 1 {
		t.Errorf("version = %d, want 1", got)
	}
	// The timestamps are the database's; the aggregate has none until the
	// insert returns.
	if !o.CreatedAt().IsZero() {
		t.Error("created_at is set, want the zero time until the row is written")
	}
}

func TestNewOrderRaisesOrderPlaced(t *testing.T) {
	o := order(t, line(t, "SHIRT-BLUE-M", 2, thb(t, 49900)))

	pulled := o.PullEvents()
	if len(pulled) != 1 {
		t.Fatalf("PullEvents() returned %d events, want 1", len(pulled))
	}

	event, ok := pulled[0].(domain.OrderPlaced)
	if !ok {
		t.Fatalf("PullEvents() returned %T, want domain.OrderPlaced", pulled[0])
	}
	if event.EventName() != "OrderPlaced" {
		t.Errorf("EventName() = %q, want %q", event.EventName(), "OrderPlaced")
	}
	if event.OrderID != o.ID() {
		t.Errorf("event order id = %q, want %q", event.OrderID, o.ID())
	}
	// The hold travels with the event so that a consumer of it needs no read of
	// the order.
	if event.ReservationID != reservationID {
		t.Errorf("event reservation id = %q, want %q", event.ReservationID, reservationID)
	}
	if event.Total.AmountMinor() != o.Total().AmountMinor() {
		t.Errorf("event total = %d, want %d", event.Total.AmountMinor(), o.Total().AmountMinor())
	}
	if len(event.Lines) != 1 {
		t.Errorf("event carries %d lines, want 1", len(event.Lines))
	}
}

func TestPullEventsEmptiesTheAggregate(t *testing.T) {
	o := order(t)

	if len(o.PullEvents()) != 1 {
		t.Fatal("first PullEvents() returned nothing, want the placement")
	}
	if pulled := o.PullEvents(); len(pulled) != 0 {
		t.Errorf("second PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestReconstituteOrderRaisesNothing(t *testing.T) {
	o := order(t)

	// An order read back from storage has made no change, and announcing its
	// placement on every read would be a fact the system hears twice.
	if pulled := domain.ReconstituteOrder(o.Snapshot()).PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestNewOrderRejectsWhatIsNotAnOrder(t *testing.T) {
	priced := thb(t, 10000)

	tests := []struct {
		name          string
		id            domain.OrderID
		userID        domain.UserID
		reservationID domain.ReservationID
		lines         []domain.OrderLine
	}{
		{"no id", "", userID, reservationID, []domain.OrderLine{line(t, "MUG-01", 1, priced)}},
		{"no user", domain.NewOrderID(), "", reservationID, []domain.OrderLine{line(t, "MUG-01", 1, priced)}},
		// An order with no hold is a promise to sell something nobody set aside.
		{"no reservation", domain.NewOrderID(), userID, "", []domain.OrderLine{line(t, "MUG-01", 1, priced)}},
		{"no lines", domain.NewOrderID(), userID, reservationID, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, err := domain.NewOrder(tt.id, tt.userID, tt.reservationID, tt.lines)
			if err == nil {
				t.Fatal("NewOrder() error = nil, want a refusal")
			}
			if o != nil {
				t.Error("NewOrder() returned an order alongside an error")
			}

			var invalid domain.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("NewOrder() error = %v, want a ValidationError", err)
			}
			if invalid.ErrorKind() != "invalid_input" {
				t.Errorf("ErrorKind() = %q, want %q", invalid.ErrorKind(), "invalid_input")
			}
		})
	}
}

func TestNewOrderRefusesTheSameSKUTwice(t *testing.T) {
	// One quantity described twice. If the two lines disagree on price there is
	// no way to tell which one the customer agreed to, so neither is guessed.
	_, err := domain.NewOrder(domain.NewOrderID(), userID, reservationID, []domain.OrderLine{
		line(t, "MUG-01", 1, thb(t, 15000)),
		line(t, "MUG-01", 2, thb(t, 12000)),
	})

	var invalid domain.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("NewOrder() error = %v, want a ValidationError", err)
	}
}

func TestNewOrderRefusesTwoCurrencies(t *testing.T) {
	usd, err := domain.NewCurrencyCode("USD")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}
	priced, err := domain.NewMoney(1000, usd)
	if err != nil {
		t.Fatalf("NewMoney() error = %v, want nil", err)
	}

	// An order priced in two currencies has no total, and the failure it would
	// otherwise become is a number that added baht to dollars.
	_, err = domain.NewOrder(domain.NewOrderID(), userID, reservationID, []domain.OrderLine{
		line(t, "MUG-01", 1, thb(t, 15000)),
		line(t, "SHIRT-BLUE-M", 1, priced),
	})

	var invalid domain.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("NewOrder() error = %v, want a ValidationError", err)
	}
}

func TestNewOrderRefusesATotalThatWouldWrap(t *testing.T) {
	huge := thb(t, math.MaxInt64/2)

	// Three lines that each fit and together do not. An amount that wrapped is
	// a charge for the wrong money rather than an error anyone would see.
	_, err := domain.NewOrder(domain.NewOrderID(), userID, reservationID, []domain.OrderLine{
		line(t, "MUG-01", 1, huge),
		line(t, "SHIRT-BLUE-M", 1, huge),
		line(t, "CAP-01", 1, huge),
	})
	if !errors.Is(err, domain.ErrTotalOutOfRange) {
		t.Fatalf("NewOrder() error = %v, want ErrTotalOutOfRange", err)
	}
}

func TestLinesReturnsACopy(t *testing.T) {
	o := order(t, line(t, "MUG-01", 1, thb(t, 15000)))

	lines := o.Lines()
	lines[0] = line(t, "SHIRT-BLUE-M", 99, thb(t, 1))

	if got := o.Lines()[0].SKU().String(); got != "MUG-01" {
		t.Errorf("aggregate line = %q after the caller rewrote its copy, want %q", got, "MUG-01")
	}
}

func TestMarkPaid(t *testing.T) {
	tests := []struct {
		name    string
		setUp   func(*testing.T, *domain.Order)
		want    bool
		wantErr error
	}{
		{
			name: "from pending",
			want: true,
		},
		{
			// Every step of a saga is retried eventually. A second delivery of
			// the same payment must not read as a failure.
			name:  "already paid",
			setUp: func(t *testing.T, o *domain.Order) { mustMarkPaid(t, o) },
			want:  false,
		},
		{
			// The stock has been given back and may be somebody else's by now.
			name:    "cancelled",
			setUp:   func(t *testing.T, o *domain.Order) { mustCancel(t, o) },
			wantErr: domain.ErrOrderCancelled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := order(t)
			if tt.setUp != nil {
				tt.setUp(t, o)
			}

			moved, err := o.MarkPaid()

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("MarkPaid() error = %v, want %v", err, tt.wantErr)
			}
			if moved != tt.want {
				t.Errorf("MarkPaid() = %v, want %v", moved, tt.want)
			}
			if tt.wantErr == nil && o.Status() != domain.StatusPaid {
				t.Errorf("status = %q, want %q", o.Status(), domain.StatusPaid)
			}
		})
	}
}

func TestCancel(t *testing.T) {
	tests := []struct {
		name    string
		setUp   func(*testing.T, *domain.Order)
		want    bool
		wantErr error
	}{
		{
			name: "from pending",
			want: true,
		},
		{
			// Compensation runs more than once.
			name:  "already cancelled",
			setUp: func(t *testing.T, o *domain.Order) { mustCancel(t, o) },
			want:  false,
		},
		{
			// Undoing a payment is a refund, with a provider on the other end
			// of it, and not a move this aggregate may make on its own.
			name:    "paid",
			setUp:   func(t *testing.T, o *domain.Order) { mustMarkPaid(t, o) },
			wantErr: domain.ErrOrderPaid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := order(t)
			if tt.setUp != nil {
				tt.setUp(t, o)
			}

			moved, err := o.Cancel()

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Cancel() error = %v, want %v", err, tt.wantErr)
			}
			if moved != tt.want {
				t.Errorf("Cancel() = %v, want %v", moved, tt.want)
			}
			if tt.wantErr == nil && o.Status() != domain.StatusCancelled {
				t.Errorf("status = %q, want %q", o.Status(), domain.StatusCancelled)
			}
		})
	}
}

func TestConflictsAreConflicts(t *testing.T) {
	// The boundary turns a kind into a status code, and these two are 409
	// rather than 400: the request was well formed and the order is simply not
	// in a state where it can be honoured.
	for _, err := range []error{domain.ErrOrderCancelled, domain.ErrOrderPaid} {
		var kinded interface{ ErrorKind() string }
		if !errors.As(err, &kinded) {
			t.Fatalf("%v declares no kind", err)
		}
		if got := kinded.ErrorKind(); got != "conflict" {
			t.Errorf("%v ErrorKind() = %q, want %q", err, got, "conflict")
		}
	}
}

func TestMarkPaidRaisesOrderPaid(t *testing.T) {
	o := order(t, line(t, "SHIRT-BLUE-M", 2, thb(t, 49900)))
	o.PullEvents() // drop the placement

	mustMarkPaid(t, o)

	pulled := o.PullEvents()
	if len(pulled) != 1 {
		t.Fatalf("PullEvents() returned %d events, want 1", len(pulled))
	}

	event, ok := pulled[0].(domain.OrderPaid)
	if !ok {
		t.Fatalf("PullEvents() returned %T, want domain.OrderPaid", pulled[0])
	}
	if event.EventName() != "OrderPaid" {
		t.Errorf("EventName() = %q, want %q", event.EventName(), "OrderPaid")
	}
	if event.OrderID != o.ID() {
		t.Errorf("event order id = %q, want %q", event.OrderID, o.ID())
	}
	// The service consumes this event back to commit the hold, so the hold it
	// names has to travel with it.
	if event.ReservationID != reservationID {
		t.Errorf("event reservation id = %q, want %q", event.ReservationID, reservationID)
	}
	if event.Total.AmountMinor() != o.Total().AmountMinor() {
		t.Errorf("event total = %d, want %d", event.Total.AmountMinor(), o.Total().AmountMinor())
	}
}

// A second delivery of the same payment must not raise the fact again: the
// consumer would commit the reservation twice, and each commit is a sale.
func TestMarkPaidRaisesNothingTheSecondTime(t *testing.T) {
	o := order(t)
	mustMarkPaid(t, o)
	o.PullEvents()

	mustMarkPaid(t, o)

	if pulled := o.PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestCancelRaisesOrderCancelled(t *testing.T) {
	o := order(t)
	o.PullEvents() // drop the placement

	mustCancel(t, o)

	pulled := o.PullEvents()
	if len(pulled) != 1 {
		t.Fatalf("PullEvents() returned %d events, want 1", len(pulled))
	}

	event, ok := pulled[0].(domain.OrderCancelled)
	if !ok {
		t.Fatalf("PullEvents() returned %T, want domain.OrderCancelled", pulled[0])
	}
	if event.EventName() != "OrderCancelled" {
		t.Errorf("EventName() = %q, want %q", event.EventName(), "OrderCancelled")
	}
	if event.OrderID != o.ID() {
		t.Errorf("event order id = %q, want %q", event.OrderID, o.ID())
	}
	// The hold this names is what the compensating step gives back.
	if event.ReservationID != reservationID {
		t.Errorf("event reservation id = %q, want %q", event.ReservationID, reservationID)
	}
}

func TestCancelRaisesNothingTheSecondTime(t *testing.T) {
	o := order(t)
	mustCancel(t, o)
	o.PullEvents()

	mustCancel(t, o)

	if pulled := o.PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

// A refused transition raises nothing either: the order did not move, and an
// event says something happened.
func TestARefusedTransitionRaisesNothing(t *testing.T) {
	o := order(t)
	mustMarkPaid(t, o)
	o.PullEvents()

	if _, err := o.Cancel(); err == nil {
		t.Fatal("Cancel() error = nil, want a refusal on a paid order")
	}
	if pulled := o.PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}

func mustMarkPaid(t *testing.T, o *domain.Order) {
	t.Helper()

	if _, err := o.MarkPaid(); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
}

func mustCancel(t *testing.T, o *domain.Order) {
	t.Helper()

	if _, err := o.Cancel(); err != nil {
		t.Fatalf("Cancel() error = %v, want nil", err)
	}
}
