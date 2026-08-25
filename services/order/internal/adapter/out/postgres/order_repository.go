package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/events"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/out/postgres/sqlc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// OrderRepository implements the driven port over pgx.
type OrderRepository struct {
	pool *pgxpool.Pool
}

var _ out.OrderRepository = (*OrderRepository)(nil)

// NewOrderRepository builds the repository over a pool.
func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

// Create writes the order, its lines, and the events it raised.
//
// All three go through the handle txmanager put on the context, which is the
// whole point: an order nobody was told about and an announcement of an order
// that rolled back are the two failures the outbox exists to remove, and they
// come back the moment one of these runs on its own connection.
func (r *OrderRepository) Create(ctx context.Context, order *domain.Order) (*domain.Order, error) {
	snapshot := order.Snapshot()

	id, err := parseID(snapshot.ID.String(), "order id")
	if err != nil {
		return nil, err
	}

	userID, err := parseID(snapshot.UserID.String(), "user id")
	if err != nil {
		return nil, err
	}

	reservationID, err := parseID(snapshot.ReservationID.String(), "reservation id")
	if err != nil {
		return nil, err
	}

	queries := queriesFrom(ctx, r.pool)

	row, err := queries.CreateOrder(ctx, sqlc.CreateOrderParams{
		ID:               id,
		UserID:           userID,
		Status:           string(snapshot.Status),
		TotalAmountMinor: snapshot.Total.AmountMinor(),
		TotalCurrency:    snapshot.Total.Currency().String(),
		ReservationID:    reservationID,
	})
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "create order")
	}

	lines := make([]sqlc.OrderLine, 0, len(snapshot.Lines))
	for _, line := range snapshot.Lines {
		stored, err := queries.CreateOrderLine(ctx, sqlc.CreateOrderLineParams{
			ID:                   uuid.New(),
			OrderID:              id,
			Sku:                  line.SKU().String(),
			Quantity:             narrow(line.Quantity()),
			UnitPriceAmountMinor: line.UnitPrice().AmountMinor(),
			UnitPriceCurrency:    line.UnitPrice().Currency().String(),
		})
		if err != nil {
			return nil, errorx.Wrap(err, errorx.KindInternal, "create order line")
		}

		lines = append(lines, stored)
	}

	if err := r.writeEvents(ctx, order, &row); err != nil {
		return nil, err
	}

	return toDomain(&row, lines), nil
}

// FindByID returns one order with its lines.
func (r *OrderRepository) FindByID(ctx context.Context, id domain.OrderID) (*domain.Order, error) {
	orderID, err := parseID(id.String(), "order id")
	if err != nil {
		return nil, err
	}

	queries := queriesFrom(ctx, r.pool)

	row, err := queries.GetOrder(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrderNotFound
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "get order")
	}

	lines, err := queries.GetOrderLines(ctx, orderID)
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "get order lines")
	}

	return toDomain(&row, lines), nil
}

// List pages through one customer's orders, newest first.
//
// The lines of the whole page come back in one query rather than one per order,
// which is the N+1 a listing turns into the first time somebody has fifty
// orders.
func (r *OrderRepository) List(ctx context.Context, filter out.OrderFilter) ([]*domain.Order, error) {
	userID, err := parseID(filter.UserID.String(), "user id")
	if err != nil {
		return nil, err
	}

	createdAt, cursorID := cursor(filter.After)

	queries := queriesFrom(ctx, r.pool)

	rows, err := queries.ListOrdersByUser(ctx, sqlc.ListOrdersByUserParams{
		UserID:          userID,
		BeforeCreatedAt: createdAt,
		BeforeID:        cursorID,
		RowLimit:        narrow(filter.Limit),
	})
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "list orders")
	}

	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}

	lines, err := queries.GetOrderLinesByOrderIDs(ctx, ids)
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "list order lines")
	}

	byOrder := make(map[uuid.UUID][]sqlc.OrderLine, len(rows))
	for _, line := range lines {
		byOrder[line.OrderID] = append(byOrder[line.OrderID], line)
	}

	orders := make([]*domain.Order, 0, len(rows))
	for i := range rows {
		orders = append(orders, toDomain(&rows[i], byOrder[rows[i].ID]))
	}

	return orders, nil
}

// writeEvents drains what the aggregate raised into the outbox.
//
// The row is what stamps the events: created_at is the database's clock rather
// than this replica's, and version is the value the row actually landed with.
func (r *OrderRepository) writeEvents(ctx context.Context, order *domain.Order, row *sqlc.Order) error {
	pulled := order.PullEvents()
	if len(pulled) == 0 {
		return nil
	}

	records := make([]outbox.Record, 0, len(pulled))
	for _, event := range pulled {
		payload, err := toPayload(event, row)
		if err != nil {
			return err
		}

		record, err := events.Record(ctx, events.Fact{
			AggregateType: aggregateType,
			AggregateID:   row.ID.String(),
			EventType:     event.EventName(),
			Topic:         topic,
			Version:       int64(row.Version),
			OccurredAt:    row.CreatedAt,
			Payload:       payload,
		})
		if err != nil {
			return errorx.Wrap(err, errorx.KindInternal, "build %s event", event.EventName())
		}

		records = append(records, record)
	}

	if err := outbox.Write(ctx, txmanager.From(ctx, r.pool), records...); err != nil {
		return errorx.Wrap(err, errorx.KindInternal, "write events to the outbox")
	}

	return nil
}

// toPayload maps a domain event to the message consumers receive. It is the
// whole of what this adapter does with an event: the domain names the fact, the
// contract in proto/ecommerce/events/v1 shapes it.
func toPayload(event domain.Event, row *sqlc.Order) (proto.Message, error) {
	switch e := event.(type) {
	case domain.OrderPlaced:
		lines := make([]*eventsv1.OrderLine, 0, len(e.Lines))
		for _, line := range e.Lines {
			lines = append(lines, &eventsv1.OrderLine{
				Sku:      line.SKU().String(),
				Quantity: narrow(line.Quantity()),
				UnitPrice: &commonv1.Money{
					AmountMinor:  line.UnitPrice().AmountMinor(),
					CurrencyCode: line.UnitPrice().Currency().String(),
				},
			})
		}

		return &eventsv1.OrderPlaced{
			OrderId:       e.OrderID.String(),
			UserId:        e.UserID.String(),
			ReservationId: e.ReservationID.String(),
			Lines:         lines,
			Total: &commonv1.Money{
				AmountMinor:  e.Total.AmountMinor(),
				CurrencyCode: e.Total.Currency().String(),
			},
			PlacedAt: timestamppb.New(row.CreatedAt),
		}, nil
	default:
		// Unreachable unless a domain event was added without a mapping, which
		// would otherwise be an event nobody outside this service ever hears.
		return nil, errorx.New(errorx.KindInternal, "no payload mapping for %s", event.EventName())
	}
}

// cursor splits a page boundary into the two halves the query compares, and
// returns the zero values for a first page — which the query turns into the
// largest value either column can hold.
func cursor(after *out.OrderCursor) (pgtype.Timestamptz, uuid.NullUUID) {
	if after == nil {
		return pgtype.Timestamptz{}, uuid.NullUUID{}
	}

	id, err := uuid.Parse(after.ID.String())
	if err != nil {
		// A cursor that does not parse is treated as no cursor: the id came
		// back through a page token the use case already validated, and failing
		// the listing over it would answer an error where the first page is
		// both harmless and what a client with a stale token wants.
		return pgtype.Timestamptz{}, uuid.NullUUID{}
	}

	return pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}, uuid.NullUUID{UUID: id, Valid: true}
}

// toDomain rebuilds the aggregate from its rows.
func toDomain(row *sqlc.Order, lines []sqlc.OrderLine) *domain.Order {
	built := make([]domain.OrderLine, 0, len(lines))
	for _, line := range lines {
		built = append(built, domain.ReconstituteOrderLine(
			domain.SKU(line.Sku),
			int(line.Quantity),
			domain.ReconstituteMoney(line.UnitPriceAmountMinor, domain.CurrencyCode(line.UnitPriceCurrency)),
		))
	}

	return domain.ReconstituteOrder(domain.OrderSnapshot{
		ID:            domain.OrderID(row.ID.String()),
		UserID:        domain.UserID(row.UserID.String()),
		Status:        domain.Status(row.Status),
		Lines:         built,
		Total:         domain.ReconstituteMoney(row.TotalAmountMinor, domain.CurrencyCode(row.TotalCurrency)),
		ReservationID: domain.ReservationID(row.ReservationID.String()),
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		Version:       int(row.Version),
	})
}
