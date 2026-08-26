// Package fakeprovider implements the provider API this system speaks, and can
// be made to misbehave in the ways a real provider's sandbox will not.
//
// It lives under internal/ beside the service it stands in for rather than in
// pkg/, because it is not infrastructure: it knows what a charge is, what a
// decline is, and which amounts mean which failure — all of which is business
// vocabulary that pkg/ may not learn. Its two callers are cmd/fakeprovider,
// which runs it in the cluster, and the adapter's tests, which run it in
// process.
package fakeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/provider"
)

// Behaviour is what the last two digits of an amount select.
//
// Encoded in the amount rather than in a header or a query parameter, so that a
// person clicking through the storefront chooses one by choosing what to buy —
// and so that a test does not have to reach past the service under test to
// configure the provider behind it.
type Behaviour int

// The behaviours, and what each one is for.
const (
	// BehaviourSucceed settles during the call, which a card charge with no
	// 3-D Secure step does.
	BehaviourSucceed Behaviour = 0

	// BehaviourDecline is the easy failure, and the only one a real sandbox
	// offers.
	BehaviourDecline Behaviour = 1

	// BehaviourWebhook accepts and settles later, which is the ordinary shape:
	// a 3-D Secure page, a QR code, a bank transfer.
	BehaviourWebhook Behaviour = 2

	// BehaviourDuplicateWebhook sends the same success twice. At-least-once is
	// what a provider gives you, and this is what proves the aggregate refuses
	// to publish the fact a second time.
	BehaviourDuplicateWebhook Behaviour = 3

	// BehaviourWebhookFirst delivers the webhook before answering the call that
	// caused it. This is the one that finds a settle path which trusted the
	// copy it had just written instead of re-reading a locked row.
	BehaviourWebhookFirst Behaviour = 4

	// BehaviourHang never answers, so the caller's deadline ends the call with
	// the charge in an unknown state — the case the whole write-before-you-call
	// ordering exists for.
	BehaviourHang Behaviour = 5
)

// behaviourModulus is how many behaviours the last digits select between. The
// amount's remainder against it is the choice, so 99800 and 100 both succeed.
const behaviourModulus = 100

// Config is what the fake reads from the environment.
//
// The variables are named in full rather than loaded under a prefix, because
// one of them is deliberately not the fake's own: the signing secret is
// PAYMENT_PROVIDER_SECRET, the same variable the payment service verifies
// with. Two names for one value is how a webhook starts failing to verify
// after somebody rotates half of it, with nothing reporting why.
type Config struct {
	Log logger.Config `envPrefix:"LOG_"`

	Addr string `env:"FAKEPROVIDER_ADDR" envDefault:":8080"`

	// CallbackURL is where webhooks are posted. It is the BFF's callback
	// endpoint rather than the payment service directly, because that is the
	// path a real provider takes — one that reached the service would exercise
	// a route no provider can use.
	CallbackURL string `env:"FAKEPROVIDER_CALLBACK_URL,required"`

	// Secret signs those webhooks. See the note above on why this one name is
	// the service's rather than this process's.
	Secret config.Secret `env:"PAYMENT_PROVIDER_SECRET,required"`

	// WebhookDelay is how long a deferred settlement waits, short enough that a
	// person clicking through does not think it broke.
	WebhookDelay time.Duration `env:"FAKEPROVIDER_WEBHOOK_DELAY" envDefault:"2s"`

	// ShutdownTimeout has to outlast WebhookDelay, or a rollout cancels the
	// very behaviour this exists to produce.
	ShutdownTimeout time.Duration `env:"FAKEPROVIDER_SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

// charge is one row of the fake's memory.
type charge struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason,omitempty"`
	NextActionURL string `json:"next_action_url,omitempty"`
}

// Provider is the fake, and it keeps its charges in memory.
//
// In memory on purpose: it is not a service anybody depends on the durability
// of, and a restart losing its charges is the same as a provider this system
// has never spoken to. Giving it a database would make it a thing to migrate.
type Provider struct {
	cfg  Config
	log  *zap.Logger
	http *http.Client

	mu      sync.Mutex
	charges map[string]*charge

	// wg tracks the webhooks still to be sent, so a shutdown can wait for them
	// rather than cutting off the delay it was asked to produce.
	wg sync.WaitGroup
}

// New builds the fake.
func New(cfg Config, log *zap.Logger) *Provider {
	return &Provider{
		cfg:     cfg,
		log:     log,
		http:    &http.Client{Timeout: 10 * time.Second},
		charges: make(map[string]*charge),
	}
}

// Routes returns the provider's API.
func (p *Provider) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/charges", p.create)
	mux.HandleFunc("GET /v1/charges/{id}", p.get)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

// Wait blocks until every webhook still owed has been sent or ctx is done.
func (p *Provider) Wait(ctx context.Context) {
	done := make(chan struct{})

	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// createRequest is what the payment service sends.
type createRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Reference      string `json:"reference"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	Method         string `json:"method,omitempty"`
}

func (p *Provider) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)

		return
	}

	if req.IdempotencyKey == "" || req.AmountMinor <= 0 || req.Currency == "" {
		http.Error(w, "missing idempotency_key, amount_minor or currency", http.StatusBadRequest)

		return
	}

	// Deduplicating on the key is the behaviour that matters most here: it is
	// what makes the caller's retry after an ambiguous timeout reach the charge
	// that was already made.
	if existing, found := p.lookup(chargeID(req.IdempotencyKey)); found {
		p.log.Info("fakeprovider: replaying a charge for a key already used",
			zap.String("id", existing.ID),
		)
		writeJSON(w, http.StatusOK, existing)

		return
	}

	behaviour := Behaviour(req.AmountMinor % behaviourModulus)

	if behaviour == BehaviourHang {
		// Never answer. The caller's deadline is what ends this, leaving it
		// unable to tell whether the money moved — which is the whole point.
		p.log.Info("fakeprovider: hanging on purpose", zap.String("key", req.IdempotencyKey))
		<-r.Context().Done()

		return
	}

	made := p.store(chargeID(req.IdempotencyKey), behaviour)

	p.log.Info("fakeprovider: charge created",
		zap.String("id", made.ID),
		zap.String("status", made.Status),
		zap.Int("behaviour", int(behaviour)),
	)

	switch behaviour {
	case BehaviourWebhook, BehaviourDuplicateWebhook:
		p.sendLater(req.IdempotencyKey, made.ID, p.cfg.WebhookDelay, deliveries(behaviour))
	case BehaviourWebhookFirst:
		// Delivered before this handler answers, which is the ordering a real
		// provider produces whenever its webhook beats its own response.
		p.send(r.Context(), req.IdempotencyKey, made.ID)
	}

	writeJSON(w, http.StatusOK, made)
}

func (p *Provider) get(w http.ResponseWriter, r *http.Request) {
	found, ok := p.lookup(r.PathValue("id"))
	if !ok {
		http.Error(w, "no such charge", http.StatusNotFound)

		return
	}

	writeJSON(w, http.StatusOK, found)
}

// store records a new charge in whatever state its behaviour starts in.
func (p *Provider) store(id string, behaviour Behaviour) charge {
	made := &charge{ID: id, Status: "pending"}

	switch behaviour {
	case BehaviourSucceed:
		made.Status = "succeeded"
	case BehaviourDecline:
		made.Status = "failed"
		made.FailureReason = "card_declined"
	default:
		// Still pending, and the customer has somewhere to go. The URL is this
		// provider's own page, which is as real as the rest of it.
		made.NextActionURL = strings.TrimRight(p.cfg.CallbackURL, "/") + "/next-action/" + id
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.charges[id] = made

	return *made
}

func (p *Provider) lookup(id string) (charge, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	found, ok := p.charges[id]
	if !ok {
		return charge{}, false
	}

	return *found, true
}

// settle moves a charge to succeeded, which is what a deferred webhook reports.
func (p *Provider) settle(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if found, ok := p.charges[id]; ok {
		found.Status = "succeeded"
		found.NextActionURL = ""
	}
}

// deliveries is how many times one settlement is delivered.
func deliveries(behaviour Behaviour) int {
	if behaviour == BehaviourDuplicateWebhook {
		return 2
	}

	return 1
}

// sendLater settles after a delay and delivers the news the given number of
// times, on a context of its own: the request that triggered it is long over.
func (p *Provider) sendLater(key, id string, delay time.Duration, times int) {
	p.wg.Add(1)

	go func() {
		defer p.wg.Done()

		time.Sleep(delay)
		p.settle(id)

		for range times {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			p.send(ctx, key, id)
			cancel()
		}
	}()
}

// callbackBody is the webhook, and it carries the key back so the payment
// service can name the attempt without indexing on this provider's id.
type callbackBody struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key"`
	Status         string `json:"status"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

// send posts one webhook, signed the way the adapter verifies.
func (p *Provider) send(ctx context.Context, key, id string) {
	found, ok := p.lookup(id)
	if !ok {
		return
	}

	body, err := json.Marshal(callbackBody{
		ID:             found.ID,
		IdempotencyKey: key,
		Status:         found.Status,
		FailureReason:  found.FailureReason,
	})
	if err != nil {
		p.log.Error("fakeprovider: encoding the callback failed", zap.Error(err))

		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.CallbackURL, bytes.NewReader(body))
	if err != nil {
		p.log.Error("fakeprovider: building the callback failed", zap.Error(err))

		return
	}

	req.Header.Set("Content-Type", "application/json")
	// Signed with provider.Sign rather than a second implementation of the
	// scheme: two that agree until somebody edits one is how a webhook starts
	// failing to verify for no visible reason.
	req.Header.Set(SignatureHeader, provider.Sign(p.cfg.Secret.Reveal(), body))

	res, err := p.http.Do(req)
	if err != nil {
		p.log.Warn("fakeprovider: the callback was not delivered",
			zap.String("id", id),
			zap.Error(err),
		)

		return
	}
	defer func() { _ = res.Body.Close() }()

	p.log.Info("fakeprovider: callback delivered",
		zap.String("id", id),
		zap.String("status", found.Status),
		zap.Int("response", res.StatusCode),
	)
}

// SignatureHeader is where the signature travels. Exported because whatever
// forwards a webhook has to read it off the request and pass it on.
const SignatureHeader = "X-Provider-Signature"

// chargeID is what this provider calls a charge. Derived from the key it was
// asked with rather than random, so that looking one up needs no second index —
// which is a liberty a fake may take and a real provider does not.
func chargeID(key string) string { return "chrg_" + key }

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}
