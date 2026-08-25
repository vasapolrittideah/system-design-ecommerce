package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out/mocks"
)

const (
	userID        = "6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60"
	otherUserID   = "7a2d1b5f-3c9e-4d2b-8a4f-6e8c9b3d5f71"
	reservationID = "8b3e2c6a-4d0f-4e3c-9b5a-7f9d0c4e6a82"
	key           = "checkout-0001"
	sku           = "MUG-01"
)

// The mocks assert their own expectations on cleanup, so a call that was set up
// and never made fails the test — which is how "no stock was reserved" below is
// checked without asserting on a counter.
func setup(t *testing.T) (
	*mocks.MockOrderRepository,
	*mocks.MockIdempotencyStore,
	*mocks.MockInventoryGateway,
	*mocks.MockCatalogGateway,
	*mocks.MockTxManager,
	*app.OrderService,
) {
	t.Helper()

	orders := mocks.NewMockOrderRepository(t)
	idem := mocks.NewMockIdempotencyStore(t)
	inventory := mocks.NewMockInventoryGateway(t)
	catalog := mocks.NewMockCatalogGateway(t)
	tx := mocks.NewMockTxManager(t)

	return orders, idem, inventory, catalog, tx, app.NewOrderService(orders, idem, inventory, catalog, tx)
}

// expectTx runs the use case's transactional work inline, so the assertions
// below are about what happened inside one and not about txmanager.
func expectTx(tx *mocks.MockTxManager) {
	tx.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		})
}

func command() in.CheckoutCommand {
	return in.CheckoutCommand{
		UserID:         userID,
		IdempotencyKey: key,
		Lines:          []in.CheckoutLine{{SKU: sku, Quantity: 2}},
	}
}

func thb(t *testing.T, amountMinor int64) domain.Money {
	t.Helper()

	currency, err := domain.NewCurrencyCode("THB")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	money, err := domain.NewMoney(amountMinor, currency)
	if err != nil {
		t.Fatalf("NewMoney() error = %v, want nil", err)
	}

	return money
}

// priced answers the catalog gateway with one price for the test's SKU.
func priced(t *testing.T, catalog *mocks.MockCatalogGateway, amountMinor int64) {
	t.Helper()

	parsed, err := domain.NewSKU(sku)
	if err != nil {
		t.Fatalf("NewSKU() error = %v, want nil", err)
	}

	catalog.EXPECT().
		PriceSKUs(mock.Anything, mock.Anything).
		Return([]out.PricedSKU{{SKU: parsed, Price: thb(t, amountMinor)}}, nil)
}

func TestCheckoutReservesThenPersists(t *testing.T) {
	orders, idem, inventory, catalog, tx, service := setup(t)

	idem.EXPECT().Find(mock.Anything, mock.Anything, key).Return(out.Claim{}, false, nil)
	priced(t, catalog, 15000)

	var reservedFor domain.OrderID
	inventory.EXPECT().
		Reserve(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, id domain.OrderID, _ []out.ReservationLine) (domain.ReservationID, error) {
			reservedFor = id

			return reservationID, nil
		})

	expectTx(tx)
	idem.EXPECT().
		Claim(mock.Anything, mock.Anything, key, mock.Anything, mock.Anything).
		Return(out.Claim{}, true, nil)
	orders.EXPECT().
		Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, o *domain.Order) (*domain.Order, error) { return o, nil })

	order, err := service.Checkout(context.Background(), command())
	if err != nil {
		t.Fatalf("Checkout() error = %v, want nil", err)
	}

	// The order returns pending: the entry call does not wait for the purchase.
	if order.Status() != domain.StatusPendingPayment {
		t.Errorf("status = %q, want %q", order.Status(), domain.StatusPendingPayment)
	}
	// 2 x 150.00, at the price the catalog quoted rather than one the client sent.
	if got := order.Total().AmountMinor(); got != 30000 {
		t.Errorf("total = %d, want %d", got, 30000)
	}
	// The hold is taken under the order's own id, which is what makes a retried
	// reservation the same reservation.
	if reservedFor != order.ID() {
		t.Errorf("reserved for %q, want the order's id %q", reservedFor, order.ID())
	}
	if order.ReservationID() != reservationID {
		t.Errorf("reservation id = %q, want %q", order.ReservationID(), reservationID)
	}
}

func TestCheckoutReplaysARetryWithoutReservingAgain(t *testing.T) {
	orders, idem, inventory, catalog, tx, service := setup(t)

	// The first attempt, kept only for the hash it makes the store record.
	idem.EXPECT().Find(mock.Anything, mock.Anything, key).Return(out.Claim{}, false, nil).Once()
	priced(t, catalog, 15000)
	inventory.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return(reservationID, nil).Once()
	expectTx(tx)

	var stored out.Claim
	idem.EXPECT().
		Claim(mock.Anything, mock.Anything, key, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context, _ domain.UserID, _ string, hash []byte, id domain.OrderID,
		) (out.Claim, bool, error) {
			stored = out.Claim{RequestHash: hash, OrderID: id}

			return out.Claim{}, true, nil
		}).Once()
	orders.EXPECT().
		Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, o *domain.Order) (*domain.Order, error) { return o, nil }).Once()

	first, err := service.Checkout(context.Background(), command())
	if err != nil {
		t.Fatalf("first Checkout() error = %v, want nil", err)
	}

	// The retry. The catalog and inventory are set up Once above and are never
	// reached again: answering from the store is what keeps a retry from
	// pricing twice and from taking a second hold on the same goods.
	idem.EXPECT().Find(mock.Anything, mock.Anything, key).Return(stored, true, nil).Once()
	orders.EXPECT().FindByID(mock.Anything, first.ID()).Return(first, nil).Once()

	second, err := service.Checkout(context.Background(), command())
	if err != nil {
		t.Fatalf("retried Checkout() error = %v, want nil", err)
	}
	if second.ID() != first.ID() {
		t.Errorf("retry produced order %q, want the first one %q", second.ID(), first.ID())
	}
}

func TestCheckoutReplaysWhenAConcurrentSubmitWonTheKey(t *testing.T) {
	orders, idem, inventory, catalog, tx, service := setup(t)

	const winner = domain.OrderID("9c4f3d7b-5e1a-4f4d-ac6b-80ae1d5f7b93")
	existing := domain.ReconstituteOrder(domain.OrderSnapshot{ID: winner, UserID: userID})

	// The read outside the transaction is a hint, not the decision: the key was
	// free a moment ago and taken by the time this transaction ran.
	idem.EXPECT().Find(mock.Anything, mock.Anything, key).Return(out.Claim{}, false, nil)
	priced(t, catalog, 15000)
	inventory.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return(reservationID, nil)
	expectTx(tx)

	idem.EXPECT().
		Claim(mock.Anything, mock.Anything, key, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context, _ domain.UserID, _ string, hash []byte, _ domain.OrderID,
		) (out.Claim, bool, error) {
			return out.Claim{RequestHash: hash, OrderID: winner}, false, nil
		})

	// This attempt's own hold has to go back: the order it was taken for was
	// never written, and the answer is a different order with a hold of its own.
	released := false
	inventory.EXPECT().
		Release(mock.Anything, domain.ReservationID(reservationID)).
		RunAndReturn(func(context.Context, domain.ReservationID) error {
			released = true

			return nil
		})
	orders.EXPECT().FindByID(mock.Anything, winner).Return(existing, nil)

	got, err := service.Checkout(context.Background(), command())
	if err != nil {
		t.Fatalf("Checkout() error = %v, want nil", err)
	}
	if got.ID() != winner {
		t.Errorf("returned order %q, want the one that won the key %q", got.ID(), winner)
	}
	if !released {
		t.Error("the losing attempt's reservation was not released")
	}
}

func TestCheckoutRefusesAKeyReusedForAnotherCart(t *testing.T) {
	_, idem, _, _, _, service := setup(t)

	// A key whose stored hash is not this request's. That is a client bug, and
	// answering it with the first order would answer a question nobody asked.
	idem.EXPECT().
		Find(mock.Anything, mock.Anything, key).
		Return(out.Claim{RequestHash: []byte("some other cart"), OrderID: "irrelevant"}, true, nil)

	_, err := service.Checkout(context.Background(), command())
	if err == nil {
		t.Fatal("Checkout() error = nil, want a conflict")
	}
	if got := errorx.KindOf(err); got != errorx.KindConflict {
		t.Errorf("kind = %v, want %v", got, errorx.KindConflict)
	}
	if got := errorx.Reason(err); got != "IDEMPOTENCY_KEY_REUSED" {
		t.Errorf("reason = %q, want %q", got, "IDEMPOTENCY_KEY_REUSED")
	}
}

func TestCheckoutReleasesTheHoldWhenTheOrderCannotBeWritten(t *testing.T) {
	orders, idem, inventory, catalog, tx, service := setup(t)

	idem.EXPECT().Find(mock.Anything, mock.Anything, key).Return(out.Claim{}, false, nil)
	priced(t, catalog, 15000)
	inventory.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return(reservationID, nil)

	expectTx(tx)
	idem.EXPECT().Claim(mock.Anything, mock.Anything, key, mock.Anything, mock.Anything).Return(out.Claim{}, true, nil)
	orders.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, errors.New("the database went away"))

	// Compensation: stock held for an order that does not exist is capacity
	// taken from whoever asks next, and waiting for the reaper is slower than
	// saying so now.
	released := false
	inventory.EXPECT().
		Release(mock.Anything, domain.ReservationID(reservationID)).
		RunAndReturn(func(context.Context, domain.ReservationID) error {
			released = true

			return nil
		})

	if _, err := service.Checkout(context.Background(), command()); err == nil {
		t.Fatal("Checkout() error = nil, want the write to fail")
	}
	if !released {
		t.Error("the reservation was not released")
	}
}

func TestCheckoutReleasesTheHoldEvenWhenReleasingFails(t *testing.T) {
	orders, idem, inventory, catalog, tx, service := setup(t)

	idem.EXPECT().Find(mock.Anything, mock.Anything, key).Return(out.Claim{}, false, nil)
	priced(t, catalog, 15000)
	inventory.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return(reservationID, nil)
	expectTx(tx)
	idem.EXPECT().Claim(mock.Anything, mock.Anything, key, mock.Anything, mock.Anything).Return(out.Claim{}, true, nil)

	wantErr := errors.New("the database went away")
	orders.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, wantErr)
	inventory.EXPECT().Release(mock.Anything, mock.Anything).Return(errors.New("inventory is down"))

	// The caller hears about their order, not about the compensation: the hold
	// expires on its own and the reaper is the backstop.
	_, err := service.Checkout(context.Background(), command())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Checkout() error = %v, want %v", err, wantErr)
	}
}

func TestCheckoutRefusesASKUTheCatalogWillNotPrice(t *testing.T) {
	_, idem, _, catalog, _, service := setup(t)

	idem.EXPECT().Find(mock.Anything, mock.Anything, key).Return(out.Claim{}, false, nil)
	// The catalog answers about none of the SKUs asked for.
	catalog.EXPECT().PriceSKUs(mock.Anything, mock.Anything).Return(nil, nil)

	// Nothing is reserved: pricing comes first precisely so that an unsellable
	// line costs no hold.
	_, err := service.Checkout(context.Background(), command())
	if err == nil {
		t.Fatal("Checkout() error = nil, want a conflict")
	}
	if got := errorx.Reason(err); got != "SKU_NOT_SELLABLE" {
		t.Errorf("reason = %q, want %q", got, "SKU_NOT_SELLABLE")
	}
	if got := errorx.Metadata(err)["sku"]; got != sku {
		t.Errorf("metadata sku = %q, want %q", got, sku)
	}
}

func TestCheckoutStopsWhenTheStockIsGone(t *testing.T) {
	_, idem, inventory, catalog, _, service := setup(t)

	idem.EXPECT().Find(mock.Anything, mock.Anything, key).Return(out.Claim{}, false, nil)
	priced(t, catalog, 15000)

	soldOut := errorx.New(errorx.KindConflict, "not enough stock").WithReason("OUT_OF_STOCK")
	inventory.EXPECT().Reserve(mock.Anything, mock.Anything, mock.Anything).Return("", soldOut)

	// Nothing is written and nothing is released: there is no hold to give back
	// and no order to undo. The shopper is told now rather than in an email.
	_, err := service.Checkout(context.Background(), command())
	if got := errorx.Reason(err); got != "OUT_OF_STOCK" {
		t.Errorf("reason = %q, want the failure inventory reported", got)
	}
}

func TestCheckoutRejectsWhatIsNotARequest(t *testing.T) {
	tests := []struct {
		name string
		cmd  in.CheckoutCommand
	}{
		{"no user", in.CheckoutCommand{IdempotencyKey: key, Lines: command().Lines}},
		{"no idempotency key", in.CheckoutCommand{UserID: userID, Lines: command().Lines}},
		{"no lines", in.CheckoutCommand{UserID: userID, IdempotencyKey: key}},
		{"a sku this service will not accept", in.CheckoutCommand{
			UserID:         userID,
			IdempotencyKey: key,
			Lines:          []in.CheckoutLine{{SKU: "no", Quantity: 1}},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No mocks are set up: none of these may reach a dependency.
			_, _, _, _, _, service := setup(t)

			_, err := service.Checkout(context.Background(), tt.cmd)
			if err == nil {
				t.Fatal("Checkout() error = nil, want a refusal")
			}
			if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
				t.Errorf("kind = %v, want %v", got, errorx.KindInvalidInput)
			}
		})
	}
}

func TestGetOrderHidesSomebodyElsesOrder(t *testing.T) {
	orders, _, _, _, _, service := setup(t)

	const id = "9c4f3d7b-5e1a-4f4d-ac6b-80ae1d5f7b93"
	orders.EXPECT().FindByID(mock.Anything, domain.OrderID(id)).Return(
		domain.ReconstituteOrder(domain.OrderSnapshot{ID: id, UserID: otherUserID}), nil)

	// Not found rather than forbidden. A 403 would confirm that an order with
	// this id exists, which is a fact about another customer.
	_, err := service.GetOrder(context.Background(), in.GetOrderQuery{OrderID: id, UserID: userID})
	if !errors.Is(err, domain.ErrOrderNotFound) {
		t.Fatalf("GetOrder() error = %v, want ErrOrderNotFound", err)
	}
}

func TestListOrdersPagesWithACursor(t *testing.T) {
	orders, _, _, _, _, service := setup(t)

	// One more than the page size comes back, which is how the service learns
	// another page exists without counting rows nobody will read.
	page := make([]*domain.Order, 0, 3)
	for _, id := range []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
	} {
		page = append(page, domain.ReconstituteOrder(domain.OrderSnapshot{ID: domain.OrderID(id), UserID: userID}))
	}

	var asked out.OrderFilter
	orders.EXPECT().
		List(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, f out.OrderFilter) ([]*domain.Order, error) {
			asked = f

			return page, nil
		})

	got, err := service.ListOrders(context.Background(), in.ListOrdersQuery{UserID: userID, PageSize: 2})
	if err != nil {
		t.Fatalf("ListOrders() error = %v, want nil", err)
	}

	if asked.Limit != 3 {
		t.Errorf("asked the repository for %d rows, want one more than the page", asked.Limit)
	}
	if len(got.Orders) != 2 {
		t.Errorf("returned %d orders, want the page size", len(got.Orders))
	}
	if got.NextPageToken == "" {
		t.Error("next page token is empty, want one while a page remains")
	}
}

func TestListOrdersRefusesATokenItDidNotIssue(t *testing.T) {
	_, _, _, _, _, service := setup(t)

	_, err := service.ListOrders(context.Background(), in.ListOrdersQuery{UserID: userID, PageToken: "not-a-cursor"})
	if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
		t.Errorf("kind = %v, want %v", got, errorx.KindInvalidInput)
	}
}
