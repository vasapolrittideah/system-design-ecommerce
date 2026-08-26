package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/events"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/postgres/sqlc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out"
)

// PaymentRepository implements the driven port over pgx.
type PaymentRepository struct {
	pool *pgxpool.Pool
}

var _ out.PaymentRepository = (*PaymentRepository)(nil)

// NewPaymentRepository builds the repository over a pool.
func NewPaymentRepository(pool *pgxpool.Pool) *PaymentRepository {
	return &PaymentRepository{pool: pool}
}

// Create writes a new attempt and any events it raised.
//
// Both go through the handle txmanager put on the context, which is the whole
// point: an attempt nobody was told about and an announcement of an attempt
// that rolled back are the two failures the outbox exists to remove, and they
// come back the moment one of these runs on its own connection.
func (r *PaymentRepository) Create(ctx context.Context, payment *domain.Payment) (*domain.Payment, error) {
	snapshot := payment.Snapshot()

	ids, err := identifiers(snapshot)
	if err != nil {
		return nil, err
	}

	row, err := queriesFrom(ctx, r.pool).CreatePayment(ctx, sqlc.CreatePaymentParams{
		ID:             ids.payment,
		OrderID:        ids.order,
		UserID:         ids.user,
		Status:         string(snapshot.Status),
		AmountMinor:    snapshot.Amount.AmountMinor(),
		AmountCurrency: snapshot.Amount.Currency().String(),
		Method:         snapshot.Method.String(),
	})
	if err != nil {
		// The index that allows one unresolved attempt per order. It is the
		// guard against two genuine submissions — a double-clicked button
		// carrying two idempotency keys — which the keys themselves cannot
		// catch, since each is a first use of its own key.
		if isConstraintViolation(err, unresolvedConstraint) {
			return nil, errorx.Wrap(err, errorx.KindConflict, "order already has an unresolved payment").
				WithReason("PAYMENT_ALREADY_IN_FLIGHT").
				WithMetadata(map[string]string{"order_id": snapshot.OrderID.String()})
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "create payment")
	}

	if err := r.writeEvents(ctx, payment, &row); err != nil {
		return nil, err
	}

	return toDomain(&row), nil
}

// Update writes a settled attempt and the events it raised, carrying the
// version it was loaded at.
func (r *PaymentRepository) Update(ctx context.Context, payment *domain.Payment) (*domain.Payment, error) {
	snapshot := payment.Snapshot()

	ids, err := identifiers(snapshot)
	if err != nil {
		return nil, err
	}

	row, err := queriesFrom(ctx, r.pool).SettlePayment(ctx, sqlc.SettlePaymentParams{
		ID:                ids.payment,
		Version:           narrow(snapshot.Version),
		Status:            string(snapshot.Status),
		ProviderReference: snapshot.ProviderReference.String(),
		FailureReason:     snapshot.FailureReason,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either the row is gone, which cannot happen, or somebody else
			// settled it first. Reported as a conflict so the loser finds out
			// rather than believing it wrote.
			return nil, errorx.Wrap(err, errorx.KindConflict, "payment was modified concurrently").
				WithReason("PAYMENT_MODIFIED")
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "settle payment")
	}

	if err := r.writeEvents(ctx, payment, &row); err != nil {
		return nil, err
	}

	return toDomain(&row), nil
}

// FindByID returns one attempt, or ErrPaymentNotFound.
func (r *PaymentRepository) FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	paymentID, err := parseID(id.String(), "payment id")
	if err != nil {
		return nil, err
	}

	row, err := queriesFrom(ctx, r.pool).GetPayment(ctx, paymentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPaymentNotFound
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "get payment")
	}

	return toDomain(&row), nil
}

// FindByIDForUpdate is FindByID holding the row until the surrounding
// transaction ends.
func (r *PaymentRepository) FindByIDForUpdate(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	paymentID, err := parseID(id.String(), "payment id")
	if err != nil {
		return nil, err
	}

	row, err := queriesFrom(ctx, r.pool).GetPaymentForUpdate(ctx, paymentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPaymentNotFound
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "lock payment")
	}

	return toDomain(&row), nil
}

// FindByOrderIDs returns every attempt made against any of these orders.
func (r *PaymentRepository) FindByOrderIDs(
	ctx context.Context,
	orderIDs []domain.OrderID,
) ([]*domain.Payment, error) {
	ids := make([]uuid.UUID, 0, len(orderIDs))
	for _, orderID := range orderIDs {
		parsed, err := parseID(orderID.String(), "order id")
		if err != nil {
			return nil, err
		}

		ids = append(ids, parsed)
	}

	rows, err := queriesFrom(ctx, r.pool).GetPaymentsByOrderIDs(ctx, ids)
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "get payments by order ids")
	}

	payments := make([]*domain.Payment, 0, len(rows))
	for i := range rows {
		payments = append(payments, toDomain(&rows[i]))
	}

	return payments, nil
}

// writeEvents drains what the aggregate raised into the outbox.
//
// The row is what stamps the events: updated_at is the database's clock rather
// than this replica's, and version is the value the row actually landed with.
// updated_at rather than created_at because these events are facts about the
// change that was just made, and on an insert the two are the same value.
func (r *PaymentRepository) writeEvents(ctx context.Context, payment *domain.Payment, row *sqlc.Payment) error {
	pulled := payment.PullEvents()
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
			OccurredAt:    row.UpdatedAt,
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
func toPayload(event domain.Event, row *sqlc.Payment) (proto.Message, error) {
	switch e := event.(type) {
	case domain.PaymentSucceeded:
		return &eventsv1.PaymentSucceeded{
			PaymentId:         e.PaymentID.String(),
			OrderId:           e.OrderID.String(),
			UserId:            e.UserID.String(),
			Amount:            money(e.Amount),
			ProviderReference: e.ProviderReference.String(),
			PaidAt:            timestamppb.New(row.UpdatedAt),
		}, nil
	case domain.PaymentFailed:
		return &eventsv1.PaymentFailed{
			PaymentId: e.PaymentID.String(),
			OrderId:   e.OrderID.String(),
			UserId:    e.UserID.String(),
			Amount:    money(e.Amount),
			Reason:    e.Reason,
			FailedAt:  timestamppb.New(row.UpdatedAt),
		}, nil
	default:
		// Unreachable unless a domain event was added without a mapping, which
		// would otherwise be an event nobody outside this service ever hears.
		return nil, errorx.New(errorx.KindInternal, "no payload mapping for %s", event.EventName())
	}
}

func money(m domain.Money) *commonv1.Money {
	return &commonv1.Money{
		AmountMinor:  m.AmountMinor(),
		CurrencyCode: m.Currency().String(),
	}
}

// paymentIDs is the three identifiers a write needs, converted once.
type paymentIDs struct {
	payment uuid.UUID
	order   uuid.UUID
	user    uuid.UUID
}

func identifiers(s domain.PaymentSnapshot) (paymentIDs, error) {
	payment, err := parseID(s.ID.String(), "payment id")
	if err != nil {
		return paymentIDs{}, err
	}

	order, err := parseID(s.OrderID.String(), "order id")
	if err != nil {
		return paymentIDs{}, err
	}

	user, err := parseID(s.UserID.String(), "user id")
	if err != nil {
		return paymentIDs{}, err
	}

	return paymentIDs{payment: payment, order: order, user: user}, nil
}

// toDomain rebuilds the aggregate from its row.
func toDomain(row *sqlc.Payment) *domain.Payment {
	return domain.ReconstitutePayment(domain.PaymentSnapshot{
		ID:                domain.PaymentID(row.ID.String()),
		OrderID:           domain.OrderID(row.OrderID.String()),
		UserID:            domain.UserID(row.UserID.String()),
		Status:            domain.Status(row.Status),
		Amount:            domain.ReconstituteMoney(row.AmountMinor, domain.CurrencyCode(row.AmountCurrency)),
		Method:            domain.Method(row.Method),
		ProviderReference: domain.ProviderReference(row.ProviderReference),
		FailureReason:     row.FailureReason,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
		Version:           int(row.Version),
	})
}
