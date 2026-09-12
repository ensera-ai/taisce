// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/notify"
	"github.com/ensera-ai/taisce/internal/recall"
)

// receiver is a real endpoint: an HTTP server that records what arrived and can be told to fail.
type receiver struct {
	mu       sync.Mutex
	bodies   [][]byte
	headers  []http.Header
	status   int
	server   *httptest.Server
	attempts int
}

func newReceiver(t *testing.T, status int) *receiver {
	t.Helper()
	r := &receiver{status: status}
	// TLS, because the deployment refuses a plaintext destination: a notification carries a
	// signature and a scope name, and over plaintext the signature is still valid to a replayer and
	// the scope is readable by anybody on the path.
	r.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.attempts++
		r.bodies = append(r.bodies, body)
		r.headers = append(r.headers, req.Header.Clone())
		status := r.status
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

func (r *receiver) last() ([]byte, http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return nil, nil
	}
	return r.bodies[len(r.bodies)-1], r.headers[len(r.headers)-1]
}

// permitting builds a sender that will send to this test's own server, which resolves to loopback —
// so the test states what a private-network operator would state, and by doing so proves that
// stating it is what makes it possible.
func permitting(t *testing.T, r *receiver) *notify.Sender {
	t.Helper()
	// The test's certificate is its own, which is exactly the case an operator with an internal
	// authority is in.
	return notify.NewSender(notify.Permit(hostOf(t, r.server.URL), true), nil).
		WithHTTPClient(r.server.Client())
}

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Hostname()
}

// ── Notifications ─────────────────────────────────────────────────────────────────────────────
//
// A notification is delivered, carries no memory, is signed so a receiver can tell our call from
// anybody's, and can be discarded by offset when it arrives twice. Against a real deployment and a
// real endpoint, because a mock of the endpoint would prove that the mock matched the assertion.
func TestAFormedScopeTellsItsEndpointAndTheNotificationCarriesNoMemory(t *testing.T) {
	h := newHarnessWithNotifications(t, "api_notify", 200)
	ctx := context.Background()

	var registered pg.Registered
	h.do(t, http.MethodPost, "/v1/notifications/endpoints/register",
		map[string]any{"url": h.receiver.server.URL + "/hook"}, http.StatusOK, &registered)
	if registered.Secret == "" || registered.ID == "" {
		t.Fatalf("a destination was registered without a signing secret: %+v", registered)
	}

	// The same destination twice is one destination.
	if status, _ := h.raw(t, http.MethodPost, "/v1/notifications/endpoints/register",
		map[string]any{"url": h.receiver.server.URL + "/hook"}, h.token); status != http.StatusConflict {
		t.Fatalf("the same destination registered twice: %d", status)
	}

	// Something to be told about.
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "marta",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, http.StatusCreated, nil)
	h.form(t)

	freshness, err := pg.NewObservationStore(h.pool).Freshness(ctx, h.schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.notifications.Owe(ctx, "p1", freshness.Formed, freshness.Stored, freshness.Parked); err != nil {
		t.Fatal(err)
	}
	delivered, attempted, err := h.notifications.Deliver(ctx, 4)
	if err != nil || delivered != 1 || attempted != 1 {
		t.Fatalf("one notification was owed and %d of %d landed: %v", delivered, attempted, err)
	}

	body, headers := h.receiver.last()
	var payload notify.Payload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("the delivery was not readable: %v", err)
	}
	if payload.Scope != "p1" || payload.FormedThrough != freshness.Formed || payload.Version != notify.PayloadVersion {
		t.Fatalf("the notification does not say what formed: %+v", payload)
	}
	// The whole point: identifiers and counts, never memory.
	for _, leak := range []string{"Ensera", "marta", "I work at"} {
		if bytes.Contains(body, []byte(leak)) {
			t.Fatalf("the notification carried memory: %q is in %s", leak, body)
		}
	}

	// Signed, and a receiver can check it the way a receiver would.
	secret, err := hex.DecodeString(registered.Secret)
	if err != nil {
		t.Fatal(err)
	}
	signature := headers.Get(notify.SignatureHeader)
	if err := notify.Verify(secret, signature, body, time.Now(), 5*time.Minute); err != nil {
		t.Fatalf("the delivery did not verify under the secret we were given: %v", err)
	}
	altered := append([]byte(nil), body...)
	altered[len(altered)-2] ^= 0x20
	if err := notify.Verify(secret, signature, altered, time.Now(), 5*time.Minute); err == nil {
		t.Fatal("a body altered by one byte still verified")
	}

	// Owing the same watermark again writes nothing: formation runs continuously and most passes
	// have no news, so this is the ordinary case rather than the exception.
	again, err := h.notifications.Owe(ctx, "p1", freshness.Formed, freshness.Stored, freshness.Parked)
	if err != nil || again != 0 {
		t.Fatalf("the same news was owed twice: %d %v", again, err)
	}
	if _, attempted, err := h.notifications.Deliver(ctx, 4); err != nil || attempted != 0 {
		t.Fatalf("a delivery was attempted with nothing owed: %d %v", attempted, err)
	}
	if h.receiver.count() != 1 {
		t.Fatalf("the endpoint was called %d times for one piece of news", h.receiver.count())
	}

	// And it is on the ledger, with the destination but without the memory.
	var audit string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(
		`SELECT coalesce(json_agg(a)::text,'[]') FROM {schema}.audit_entry a WHERE operation LIKE 'notification.%'`)).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(audit), []byte("notification.register")) {
		t.Fatalf("registering a destination left no ledger row: %s", audit)
	}
	if bytes.Contains([]byte(audit), []byte(registered.Secret)) {
		t.Fatal("a signing secret entered the audit")
	}

	// The receipt a customer reads back: what was sent, and that it landed.
	var list struct {
		Deliveries []pg.DeliveryRecord `json:"deliveries"`
	}
	h.do(t, http.MethodPost, "/v1/notifications/deliveries/list", map[string]any{}, http.StatusOK, &list)
	if len(list.Deliveries) != 1 || list.Deliveries[0].DeliveredAt == nil {
		t.Fatalf("the delivery is not readable as delivered: %+v", list.Deliveries)
	}
}

// A destination that keeps refusing is retried and then parked, visibly. A queue that never empties
// is a queue nobody can read, and a notification nobody is coming for should be findable by asking.
func TestADestinationThatKeepsFailingIsRetriedThenParkedWhereAnOperatorCanSeeIt(t *testing.T) {
	h := newHarnessWithNotifications(t, "api_notify_park", http.StatusServiceUnavailable)
	ctx := context.Background()
	// A budget of three and no wait, because the point is the parking and not the patience.
	h.notifications.WithBudget(3, 0)

	h.do(t, http.MethodPost, "/v1/notifications/endpoints/register",
		map[string]any{"url": h.receiver.server.URL + "/hook"}, http.StatusOK, nil)
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "marta",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, http.StatusCreated, nil)
	h.form(t)
	freshness, err := pg.NewObservationStore(h.pool).Freshness(ctx, h.schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.notifications.Owe(ctx, "p1", freshness.Formed, freshness.Stored, freshness.Parked); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		if _, _, err := h.notifications.Deliver(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	if h.receiver.count() != 3 {
		t.Fatalf("the attempt budget was %d and the endpoint was called %d times", 3, h.receiver.count())
	}

	var list struct {
		Deliveries []pg.DeliveryRecord `json:"deliveries"`
	}
	h.do(t, http.MethodPost, "/v1/notifications/deliveries/list", map[string]any{}, http.StatusOK, &list)
	if len(list.Deliveries) != 1 || list.Deliveries[0].ParkedAt == nil || list.Deliveries[0].Attempts != 3 {
		t.Fatalf("a delivery that exhausted its budget is not visibly parked: %+v", list.Deliveries)
	}
	if list.Deliveries[0].LastStatus == nil || *list.Deliveries[0].LastStatus != http.StatusServiceUnavailable {
		t.Fatalf("the parked delivery does not say what the destination answered: %+v", list.Deliveries[0])
	}
	// What it says is a category, never the transport's own words: those carry the address and port a
	// destination led to, and this list is readable by any key in the project.
	if got := list.Deliveries[0].LastError; got == nil || *got != "destination answered 503" {
		t.Fatalf("the parked delivery's error is not the category a reader may see: %v", got)
	}

	// Disabled, and the destination stops being owed anything.
	var endpoints struct {
		Endpoints []pg.Endpoint `json:"endpoints"`
	}
	h.do(t, http.MethodPost, "/v1/notifications/endpoints/list", map[string]any{}, http.StatusOK, &endpoints)
	h.do(t, http.MethodPost, "/v1/notifications/endpoints/disable",
		map[string]any{"id": endpoints.Endpoints[0].ID}, http.StatusOK, nil)
	if _, err := h.notifications.Owe(ctx, "p1", freshness.Formed+1, freshness.Stored+1, 0); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/notifications/deliveries/list", map[string]any{}, http.StatusOK, &list)
	if len(list.Deliveries) != 1 {
		t.Fatalf("a disabled destination was still owed: %+v", list.Deliveries)
	}
}

// A delivery's failure can be stored only as one of the categories the sender produces. The
// transport's own words name the address a destination led to, and the delivery list is readable by
// read-only keys, so the substrate refuses them even if a later sender forgets. Every category the
// sender can produce is accepted; a raw transport error is refused.
func TestADeliveryFailureCanOnlyBeStoredAsACategory(t *testing.T) {
	h := newHarnessWithNotifications(t, "api_notify_category", http.StatusServiceUnavailable)
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/notifications/endpoints/register",
		map[string]any{"url": h.receiver.server.URL + "/hook"}, http.StatusOK, nil)
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "marta",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, http.StatusCreated, nil)
	h.form(t)
	freshness, err := pg.NewObservationStore(h.pool).Freshness(ctx, h.schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.notifications.Owe(ctx, "p1", freshness.Formed, freshness.Stored, freshness.Parked); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Deliveries []pg.DeliveryRecord `json:"deliveries"`
	}
	h.do(t, http.MethodPost, "/v1/notifications/deliveries/list", map[string]any{}, http.StatusOK, &list)
	if len(list.Deliveries) != 1 {
		t.Fatalf("expected one owed delivery, have %+v", list.Deliveries)
	}
	id := list.Deliveries[0].ID

	for _, raw := range []string{
		`Post "https://hooks.example.com/hook": dial tcp 10.0.4.7:5432: connect: connection refused`,
		"the destination answered 503",
		"destination answered 5033",
	} {
		if err := h.notifyStore.Failed(ctx, id, 1, 3, 0, raw, 0); err == nil {
			t.Errorf("the substrate stored a failure that is not a category: %q", raw)
		}
	}
	for _, category := range []string{
		"destination not permitted", "address not permitted", "redirect not followed",
		"destination answered 503", "destination answered 429", "destination refused with 404",
		"timeout", "connection refused", "tls failure", "network error",
	} {
		if err := h.notifyStore.Failed(ctx, id, 1, 3, 0, category, 0); err != nil {
			t.Errorf("the substrate refused a category the sender produces, %q: %v", category, err)
		}
	}
}

// A deployment whose operator has named no destination refuses every notification route, rather than
// accepting a registration it will never honour.
func TestADeploymentWithNoPermittedDestinationRefusesEveryNotificationRoute(t *testing.T) {
	h := newHarness(t, "api_notify_off")
	for path, body := range map[string]map[string]any{
		"/v1/notifications/endpoints/register": {"url": "https://hooks.example.com/in"},
		"/v1/notifications/endpoints/list":     {},
		"/v1/notifications/endpoints/disable":  {"id": "6a5e3a2c-1f0b-4a4e-9a1e-0c2b5a7d1f30"},
		"/v1/notifications/deliveries/list":    {},
	} {
		status, body := h.raw(t, http.MethodPost, path, body, h.token)
		if status != http.StatusNotImplemented || !bytes.Contains(body, []byte("notifications_unavailable")) {
			t.Fatalf("%s answered %d %s on a deployment that sends nothing", path, status, body)
		}
	}
}

// newHarnessWithNotifications is the ordinary harness with a receiver and a sender that may reach
// it. The sender is told to permit a private destination, because the test's own server is on
// loopback — which is exactly the statement an operator with an internal receiver makes, and making
// the test make it is what proves that nothing is permitted without it.
func newHarnessWithNotifications(t *testing.T, tenant string, status int) *harness {
	t.Helper()
	h := newHarness(t, tenant)
	h.receiver = newReceiver(t, status)
	sender := permitting(t, h.receiver)
	store := pg.NewNotificationStore(h.pool, h.schema)
	h.notifications = formation.NewNotifications(store, sender)
	h.notifyStore, h.notifier = store, sender

	// A second server, this one knowing where it may send. The first is left alone so the
	// unconfigured case keeps its own harness.
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	server := httptest.NewServer(api.NewServer(
		credentials, api.Stores{
			Audit:        pg.NewAuditStore(h.pool, h.schema),
			Exporter:     pg.NewExporter(h.pool, h.schema),
			Projects:     pg.NewProjectStore(h.pool, h.schema),
			Observations: pg.NewObservationStore(h.pool),
			Eraser:       pg.NewEraser(h.pool),
			Recaller:     recall.NewWithBudget(pg.NewRecallStore(h.pool, h.schema), recall.Budget{Characters: 100000, MaxRows: 50}),
			Citations:    pg.NewCitationStore(h.pool, h.schema),
			Records:      pg.NewRecordStore(h.pool, h.schema), Feedback: pg.NewFeedbackStore(h.pool, h.schema, pg.NewRecordStore(h.pool, h.schema)),
			Artifacts:     pg.NewArtifactStore(h.pool, h.schema),
			Subjects:      pg.NewSubjectStore(h.pool, h.schema),
			Contexts:      pg.NewSegmentStore(h.pool, h.schema),
			Notifications: store,
			Notifier:      sender,
		}, h.schema, nil,
	).Handler())
	t.Cleanup(server.Close)
	h.server = server
	return h
}

// A destination that says the request is wrong is not asked again. Retrying a 4xx is asking the same
// question and expecting a different answer, and it spends a budget that exists for the failures
// that are not this delivery's fault.
func TestADestinationThatCallsTheRequestWrongIsParkedOnTheFirstAttempt(t *testing.T) {
	h := newHarnessWithNotifications(t, "api_notify_final", http.StatusBadRequest)
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/notifications/endpoints/register",
		map[string]any{"url": h.receiver.server.URL + "/hook"}, http.StatusOK, nil)
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "marta",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, http.StatusCreated, nil)
	h.form(t)
	freshness, err := pg.NewObservationStore(h.pool).Freshness(ctx, h.schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.notifications.Owe(ctx, "p1", freshness.Formed, freshness.Stored, freshness.Parked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.notifications.Deliver(ctx, 4); err != nil {
		t.Fatal(err)
	}
	// Once, and never again, even though the budget was nowhere near spent.
	if _, _, err := h.notifications.Deliver(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if h.receiver.count() != 1 {
		t.Fatalf("a refusal was retried: the endpoint was called %d times", h.receiver.count())
	}
	var list struct {
		Deliveries []pg.DeliveryRecord `json:"deliveries"`
	}
	h.do(t, http.MethodPost, "/v1/notifications/deliveries/list", map[string]any{"limit": 1000}, http.StatusOK, &list)
	if len(list.Deliveries) != 1 || list.Deliveries[0].ParkedAt == nil || list.Deliveries[0].Attempts != 1 {
		t.Fatalf("a refused delivery is parked at once and says so: %+v", list.Deliveries)
	}
}

// The refusals a customer meets when they get an identifier wrong, and the one they meet when they
// disable something twice.
func TestDisablingWhatIsNotThereIsRefusedAndDisablingTwiceIsTheSameRefusal(t *testing.T) {
	h := newHarnessWithNotifications(t, "api_notify_refusals", http.StatusOK)

	var registered pg.Registered
	h.do(t, http.MethodPost, "/v1/notifications/endpoints/register",
		map[string]any{"url": h.receiver.server.URL + "/hook"}, http.StatusOK, &registered)

	for name, id := range map[string]string{
		"an id that is not one": "not-an-id",
		"an id nobody issued":   "6a5e3a2c-1f0b-4a4e-9a1e-0c2b5a7d1f30",
	} {
		if status, _ := h.raw(t, http.MethodPost, "/v1/notifications/endpoints/disable",
			map[string]any{"id": id}, h.token); status != http.StatusNotFound {
			t.Fatalf("disabling %s answered %d", name, status)
		}
	}

	h.do(t, http.MethodPost, "/v1/notifications/endpoints/disable", map[string]any{"id": registered.ID}, http.StatusOK, nil)
	// Again: already disabled is not there to disable, and says so the same way.
	if status, _ := h.raw(t, http.MethodPost, "/v1/notifications/endpoints/disable",
		map[string]any{"id": registered.ID}, h.token); status != http.StatusNotFound {
		t.Fatal("disabling twice succeeded twice")
	}

	// A disabled destination is still listed, because an operator asking where this goes is owed the
	// ones that used to.
	var endpoints struct {
		Endpoints []pg.Endpoint `json:"endpoints"`
	}
	h.do(t, http.MethodPost, "/v1/notifications/endpoints/list", map[string]any{}, http.StatusOK, &endpoints)
	if len(endpoints.Endpoints) != 1 || endpoints.Endpoints[0].DisabledAt == nil {
		t.Fatalf("a disabled destination was forgotten: %+v", endpoints.Endpoints)
	}

	// A destination the operator never permitted is refused when it is typed, not hours later in a
	// delivery nobody is watching.
	status, body := h.raw(t, http.MethodPost, "/v1/notifications/endpoints/register",
		map[string]any{"url": "https://somewhere.else.example/in"}, h.token)
	if status != http.StatusBadRequest || !bytes.Contains(body, []byte("invalid_destination")) {
		t.Fatalf("an unlisted destination was accepted: %d %s", status, body)
	}
}

// When the store cannot answer, every notification route says so without saying what went wrong.
// A refusal that names a table teaches a caller the shape of the schema, and a 500 with a reason is
// the most common way that happens.
func TestNotificationRoutesAnswerInternallyWithoutDescribingTheDatabase(t *testing.T) {
	h := newHarnessWithNotifications(t, "api_notify_internal", http.StatusOK)
	ctx := context.Background()

	var registered pg.Registered
	h.do(t, http.MethodPost, "/v1/notifications/endpoints/register",
		map[string]any{"url": h.receiver.server.URL + "/hook"}, http.StatusOK, &registered)

	// The tables move out from under the running server, which is what a failed migration or a
	// hand-edited database looks like from here.
	for _, table := range []string{"notification_delivery", "notification_endpoint"} {
		if _, err := h.pool.Exec(ctx, h.schema.SQL(
			`ALTER TABLE {schema}.`+table+` RENAME TO `+table+`_moved`)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, table := range []string{"notification_endpoint", "notification_delivery"} {
			_, _ = h.pool.Exec(context.Background(), h.schema.SQL(
				`ALTER TABLE {schema}.`+table+`_moved RENAME TO `+table))
		}
	})

	for path, body := range map[string]map[string]any{
		"/v1/notifications/endpoints/register": {"url": h.receiver.server.URL + "/second"},
		"/v1/notifications/endpoints/list":     {},
		"/v1/notifications/endpoints/disable":  {"id": registered.ID},
		"/v1/notifications/deliveries/list":    {},
	} {
		status, answer := h.raw(t, http.MethodPost, path, body, h.token)
		if status != http.StatusInternalServerError {
			t.Fatalf("%s answered %d with its tables gone: %s", path, status, answer)
		}
		for _, leak := range []string{"notification_endpoint", "notification_delivery", "relation", "SQLSTATE"} {
			if bytes.Contains(answer, []byte(leak)) {
				t.Fatalf("%s disclosed %q: %s", path, leak, answer)
			}
		}
	}
}
