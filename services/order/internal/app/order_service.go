// Package app holds the use cases: the orchestration between a request and the
// domain rules that answer it. There is deliberately very little here — an `if`
// in this package that encodes a business policy is a rule that escaped the
// domain, where it could have been tested without a mock.
//
// It is also where the checkout saga is driven from. Orchestration and not
// choreography: the whole sequence is in one method, so what happens after a
// payment is a thing somebody can read rather than infer from three consumers.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// What a listing returns when the caller does not say. The upper bound is the
// proto's to enforce; it is repeated here as a ceiling for callers that are not
// gRPC, since the cost of an unbounded page is paid by this process.
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// releaseTimeout bounds the compensating call. It is short because the caller
// is already being answered — and it is bounded at all because the reaper
// returns an abandoned hold anyway, so waiting longer buys nothing.
const releaseTimeout = 3 * time.Second

// errReplay is how the transaction says "this key already has an answer".
//
// A sentinel rather than a value returned from the closure, because the
// transaction must roll back: the order it was about to write is not the one
// the client is going to be given.
var errReplay = errors.New("order: idempotency key already answered")

// OrderService implements the order use cases and drives the checkout saga.
type OrderService struct {
	orders    out.OrderRepository
	idem      out.IdempotencyStore
	inventory out.InventoryGateway
	catalog   out.CatalogGateway
	tx        out.TxManager
}

// Compile-time proof that the driving port is satisfied. Without it the failure
// surfaces in bootstrap, naming the wiring rather than the missing method.
var _ in.OrderUseCase = (*OrderService)(nil)

// NewOrderService wires the use cases to their driven ports.
func NewOrderService(
	orders out.OrderRepository,
	idem out.IdempotencyStore,
	inventory out.InventoryGateway,
	catalog out.CatalogGateway,
	tx out.TxManager,
) *OrderService {
	return &OrderService{orders: orders, idem: idem, inventory: inventory, catalog: catalog, tx: tx}
}

// Checkout places an order and returns as soon as it exists.
//
// The sequence is the whole saga's first half, and each step is where it is for
// a reason that the next one would get wrong:
//
//  1. A key already answered replays that answer, before anything else happens.
//     This is what keeps a retry from reserving stock a second time.
//  2. Prices come from the catalog and are copied into the order. An order is a
//     record of an agreement, so the price is frozen here rather than read
//     again later.
//  3. The stock is reserved synchronously, so a shopper learns that something
//     is sold out now rather than in an email afterwards.
//  4. The claim, the order, its lines, and the event announcing it are one
//     transaction. Anything less and a client's retry can be answered with an
//     order that was rolled back, or an order can exist that nothing was ever
//     told about.
//
// Everything after step 3 that fails releases the hold. The reservation would
// expire on its own — inventory sweeps it — but leaving stock held for the TTL
// because a request failed is capacity taken from whoever asks next.
func (s *OrderService) Checkout(ctx context.Context, cmd in.CheckoutCommand) (*domain.Order, error) {
	userID, err := domain.ParseUserID(cmd.UserID)
	if err != nil {
		return nil, err
	}

	if cmd.IdempotencyKey == "" {
		return nil, errorx.New(errorx.KindInvalidInput, "idempotency_key is required").
			WithReason("IDEMPOTENCY_KEY_REQUIRED")
	}

	requested, err := requestedLines(cmd.Lines)
	if err != nil {
		return nil, err
	}
	hash := hashRequest(userID, requested)

	replayed, err := s.replay(ctx, userID, cmd.IdempotencyKey, hash)
	if err != nil || replayed != nil {
		return replayed, err
	}

	lines, err := s.price(ctx, requested)
	if err != nil {
		return nil, err
	}

	// Minted before the hold, because the hold is taken under it: the order id
	// is what inventory deduplicates a retried reservation on.
	orderID := domain.NewOrderID()

	reservationID, err := s.inventory.Reserve(ctx, orderID, requested)
	if err != nil {
		return nil, err
	}

	var (
		order      *domain.Order
		replayedID domain.OrderID
	)

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		claim, taken, err := s.idem.Claim(ctx, userID, cmd.IdempotencyKey, hash, orderID)
		if err != nil {
			return err
		}
		if !taken {
			if !bytes.Equal(claim.RequestHash, hash) {
				return errKeyReused()
			}

			replayedID = claim.OrderID

			return errReplay
		}

		built, err := domain.NewOrder(orderID, userID, reservationID, lines)
		if err != nil {
			return err
		}

		order, err = s.orders.Create(ctx, built)

		return err
	})
	if err != nil {
		// The hold belonged to an order that does not exist, whichever way this
		// went — including the replay, where the answer is a different order
		// that has a hold of its own.
		s.release(ctx, reservationID)

		if errors.Is(err, errReplay) {
			return s.orders.FindByID(ctx, replayedID)
		}

		return nil, err
	}

	return order, nil
}

// replay answers a key that already has an answer, and reports nil when the key
// is free.
//
// It is a read outside any transaction, which makes it a hint rather than a
// decision: a key free here can be taken before Checkout's transaction runs,
// and that transaction is where the question is settled. What it buys is the
// common case — a client retrying after a successful call is answered without
// reserving stock or writing anything.
func (s *OrderService) replay(
	ctx context.Context,
	userID domain.UserID,
	key string,
	hash []byte,
) (*domain.Order, error) {
	claim, found, err := s.idem.Find(ctx, userID, key)
	if err != nil || !found {
		return nil, err
	}

	if !bytes.Equal(claim.RequestHash, hash) {
		return nil, errKeyReused()
	}

	return s.orders.FindByID(ctx, claim.OrderID)
}

// price asks the catalog what each SKU costs and turns the answer into order
// lines.
//
// A SKU the catalog will not price cannot be bought, and saying so here is the
// difference between an order for something that does not exist and a checkout
// the shopper can fix. It is a conflict rather than a not-found: the request
// named a real endpoint and a real cart, and one line of it is unsellable.
func (s *OrderService) price(ctx context.Context, requested []out.ReservationLine) ([]domain.OrderLine, error) {
	skus := make([]domain.SKU, 0, len(requested))
	for _, line := range requested {
		skus = append(skus, line.SKU)
	}

	priced, err := s.catalog.PriceSKUs(ctx, skus)
	if err != nil {
		return nil, err
	}

	prices := make(map[domain.SKU]domain.Money, len(priced))
	for _, p := range priced {
		prices[p.SKU] = p.Price
	}

	lines := make([]domain.OrderLine, 0, len(requested))
	for _, line := range requested {
		price, ok := prices[line.SKU]
		if !ok {
			return nil, errorx.New(errorx.KindConflict, "sku is not for sale").
				WithReason("SKU_NOT_SELLABLE").
				WithMetadata(map[string]string{"sku": line.SKU.String()})
		}

		built, err := domain.NewOrderLine(line.SKU, line.Quantity, price)
		if err != nil {
			return nil, err
		}

		lines = append(lines, built)
	}

	return lines, nil
}

// release gives a hold back after a checkout that could not be finished.
//
// It runs on a context of its own, because the request's may already be
// cancelled — a client that hung up is exactly when this matters — and a
// compensating call that inherited that cancellation would never be made.
//
// A failure is logged and not returned. The caller is already being told what
// went wrong with their order, and the hold expires on its own: inventory's
// reaper is the backstop, and this is the fast path to the same outcome.
func (s *OrderService) release(ctx context.Context, reservationID domain.ReservationID) {
	log := logger.From(ctx)

	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	if err := s.inventory.Release(detached, reservationID); err != nil {
		log.Warn("order: releasing the reservation failed, leaving it to expire",
			zap.String("reservation_id", reservationID.String()),
			zap.Error(err),
		)
	}
}

// GetOrder reads one of the caller's own orders.
func (s *OrderService) GetOrder(ctx context.Context, query in.GetOrderQuery) (*domain.Order, error) {
	orderID, err := domain.ParseOrderID(query.OrderID)
	if err != nil {
		return nil, err
	}

	userID, err := domain.ParseUserID(query.UserID)
	if err != nil {
		return nil, err
	}

	order, err := s.orders.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	// Somebody else's order is not found rather than forbidden. A 403 here
	// would confirm that an order with this id exists, which is a fact about
	// another customer.
	if order.UserID() != userID {
		return nil, domain.ErrOrderNotFound
	}

	return order, nil
}

// ListOrders pages through the caller's own orders, newest first.
func (s *OrderService) ListOrders(ctx context.Context, query in.ListOrdersQuery) (in.OrdersPage, error) {
	userID, err := domain.ParseUserID(query.UserID)
	if err != nil {
		return in.OrdersPage{}, err
	}

	after, err := decodeCursor(query.PageToken)
	if err != nil {
		return in.OrdersPage{}, err
	}

	size := query.PageSize
	switch {
	case size <= 0:
		size = defaultPageSize
	case size > maxPageSize:
		size = maxPageSize
	}

	// One more than the page, which is how the last page is recognised without
	// a second query counting rows nobody will read.
	orders, err := s.orders.List(ctx, out.OrderFilter{UserID: userID, After: after, Limit: size + 1})
	if err != nil {
		return in.OrdersPage{}, err
	}

	var next string
	if len(orders) > size {
		last := orders[size-1]
		next = encodeCursor(out.OrderCursor{CreatedAt: last.CreatedAt(), ID: last.ID()})
		orders = orders[:size]
	}

	return in.OrdersPage{Orders: orders, NextPageToken: next}, nil
}

// requestedLines turns what arrived into domain values, refusing anything that
// is not a line before a single downstream call is made.
func requestedLines(lines []in.CheckoutLine) ([]out.ReservationLine, error) {
	if len(lines) == 0 {
		return nil, errorx.New(errorx.KindInvalidInput, "an order has at least one line")
	}

	parsed := make([]out.ReservationLine, 0, len(lines))
	for _, line := range lines {
		sku, err := domain.NewSKU(line.SKU)
		if err != nil {
			return nil, err
		}

		parsed = append(parsed, out.ReservationLine{SKU: sku, Quantity: line.Quantity})
	}

	return parsed, nil
}

// hashRequest fingerprints what the client asked for, so that a key reused for
// a different cart is told apart from a retry of the same one.
//
// The prices are deliberately not in it. They come from the catalog and may
// have moved between two attempts, and a retry of the same cart must still be
// answered with the first order rather than refused as a different request.
//
// The lines are sorted first: a client that sends the same cart in a different
// order is retrying, not asking for something else.
func hashRequest(userID domain.UserID, lines []out.ReservationLine) []byte {
	sorted := make([]string, 0, len(lines))
	for _, line := range lines {
		sorted = append(sorted, fmt.Sprintf("%s:%d", line.SKU, line.Quantity))
	}
	sort.Strings(sorted)

	digest := sha256.New()
	digest.Write([]byte(userID))
	for _, line := range sorted {
		digest.Write([]byte{0})
		digest.Write([]byte(line))
	}

	return digest.Sum(nil)
}

// errKeyReused is the answer to a key that was first used for something else.
func errKeyReused() error {
	return errorx.New(errorx.KindConflict, "idempotency_key was used for a different request").
		WithReason("IDEMPOTENCY_KEY_REUSED")
}
