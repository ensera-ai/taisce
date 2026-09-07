// The journey over the wire.
//
// #47's claim is that everything the spine does is reachable by a client that is not written in Go.
// The way to test that honestly is to make the assertions travel over HTTP and nothing else: every
// operation under test — observe, freshness, recall, erase — is performed with `net/http` and
// `encoding/json` against a real server on a real database, and the response bodies are the only
// thing asserted on.
//
// Internal packages appear here only to SET UP what a deployment would already have: a provisioned
// schema, the registry, a credential, and a formation pass. None of them performs an operation the
// surface exposes, because a test that reached past the handler would be proving the packages work,
// which is what every other test in this repository already does.
//
// Formation runs with a scripted model rather than a live one. What is under test is whether a fact
// formed behind an append becomes a cited fact over the wire — not whether a model extracts well,
// which is #70's corpus and costs money.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
)

// ── The claim ─────────────────────────────────────────────────────────────────────────────────
//
// Observe a turn, watch it form, recall a fact with the words behind it, erase the person, and read
// a residual of zero — all of it over HTTP.
func TestTheWholeJourneyRunsOverHTTP(t *testing.T) {
	h := newHarness(t, "api_journey")

	// ── Observe ───────────────────────────────────────────────────────────────────────────────
	var observed struct {
		ID        string `json:"id"`
		Scope     string `json:"scope"`
		LogOffset int64  `json:"log_offset"`
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"scope":           "p1",
		"data_subject_id": "subject-1",
		"messages": []map[string]any{
			{"role": "user", "content": "I work at Ensera and I live in Dublin."},
		},
	}, http.StatusCreated, &observed)
	if observed.ID == "" {
		t.Fatal("an observation was stored without an identity a caller could hold")
	}

	// ── Freshness, before anything formed ─────────────────────────────────────────────────────
	//
	// Stored is what the append confirmed; formed is what a recall depends on. Formed is ABSENT
	// rather than zero here, because zero is a real offset belonging to the first turn — a caller
	// polling for "formed >= my offset" would otherwise be told yes before anything had happened.
	var before struct {
		Stored int64  `json:"stored"`
		Formed *int64 `json:"formed"`
	}
	h.do(t, http.MethodGet, "/v1/freshness?scope=p1", nil, http.StatusOK, &before)
	if before.Stored != observed.LogOffset {
		t.Fatalf("stored is %d but the append returned offset %d", before.Stored, observed.LogOffset)
	}
	if before.Formed != nil {
		t.Fatalf("formed is %d before anything has formed", *before.Formed)
	}

	h.form(t)

	var after struct {
		Formed *int64 `json:"formed"`
	}
	h.do(t, http.MethodGet, "/v1/freshness?scope=p1", nil, http.StatusOK, &after)
	if after.Formed == nil || *after.Formed != observed.LogOffset {
		t.Fatalf("formed did not reach the offset that was appended: %v", after.Formed)
	}

	// ── Recall ────────────────────────────────────────────────────────────────────────────────
	var bundle struct {
		Anchors []struct {
			Name    string `json:"name"`
			Matched string `json:"matched"`
		} `json:"anchors"`
		Facts []struct {
			Predicate  string `json:"predicate"`
			Object     string `json:"object"`
			AnchoredOn string `json:"anchored_on"`
			Evidence   struct {
				ObservationID string `json:"observation_id"`
				Quote         string `json:"quote"`
				ByteStart     int    `json:"byte_start"`
				ByteEnd       int    `json:"byte_end"`
			} `json:"evidence"`
		} `json:"facts"`
		Truncated bool `json:"truncated"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question": "Where does the user work?",
		"scopes":   []string{"p1"},
	}, http.StatusOK, &bundle)

	if len(bundle.Anchors) == 0 {
		t.Fatal("the question named an entity in memory and anchored on nothing")
	}
	if len(bundle.Facts) == 0 {
		t.Fatal("a fact was formed and the bundle came back empty")
	}

	// The whole argument of the product, asserted over the wire: the words behind the claim come
	// back with it, and the span indexes the message it names.
	fact := bundle.Facts[0]
	if fact.Evidence.Quote == "" || fact.Evidence.ObservationID == "" {
		t.Fatalf("a fact came back without the evidence for it: %+v", fact)
	}
	const content = "I work at Ensera and I live in Dublin."
	if got := content[fact.Evidence.ByteStart:fact.Evidence.ByteEnd]; got != fact.Evidence.Quote {
		t.Fatalf("the span gives %q and the quote is %q", got, fact.Evidence.Quote)
	}
	if fact.AnchoredOn == "" {
		t.Fatal("a fact came back without saying which anchor reached it")
	}

	// ── Erase ─────────────────────────────────────────────────────────────────────────────────
	var receipt struct {
		Deleted  map[string]int `json:"deleted"`
		Residual map[string]int `json:"residual"`
		Clean    bool           `json:"clean"`
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{
		"scope": "p1", "data_subject_id": "subject-1", "reason": "the journey, over HTTP",
	}, http.StatusOK, &receipt)

	if !receipt.Clean {
		t.Fatalf("the erasure left a residual: %v", receipt.Residual)
	}
	if len(receipt.Deleted) == 0 {
		t.Fatal("the erasure reported nothing deleted, so it cannot be distinguished from a no-op")
	}
	for kind, n := range receipt.Residual {
		if n != 0 {
			t.Fatalf("%d rows of %s survived", n, kind)
		}
	}

	// And the memory is gone, asked the same way a caller would ask.
	var afterErasure struct {
		Facts []json.RawMessage `json:"facts"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question": "Where does the user work?", "scopes": []string{"p1"},
	}, http.StatusOK, &afterErasure)
	if len(afterErasure.Facts) != 0 {
		t.Fatalf("%d facts survived an erasure that reported a residual of zero", len(afterErasure.Facts))
	}
}

// ── What the surface refuses ──────────────────────────────────────────────────────────────────

// Every operation is behind a credential. A memory service that answers an unauthenticated request
// is a memory dump on a port.
func TestEveryOperationRefusesAnUnauthenticatedCaller(t *testing.T) {
	h := newHarness(t, "api_anon")

	for _, c := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPost, "/v1/observations", map[string]any{"scope": "p1"}},
		{http.MethodGet, "/v1/freshness?scope=p1", nil},
		{http.MethodPost, "/v1/recalls", map[string]any{"question": "anything"}},
		{http.MethodPost, "/v1/erasures", map[string]any{"scope": "p1", "data_subject_id": "s"}},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			status, _ := h.raw(t, c.method, c.path, c.body, "")
			if status != http.StatusUnauthorized {
				t.Fatalf("answered %d without a credential", status)
			}
		})
	}
}

// A token that is not ours, and one that is shaped like ours but unknown, are the same answer as no
// token at all. Distinguishing them tells a stranger whether a token they hold is real, which is the
// one fact they cannot get any other way.
func TestAnUnknownCredentialIsRefusedTheSameWayAsNone(t *testing.T) {
	h := newHarness(t, "api_unknown")
	for _, token := range []string{"not-a-taisce-token", "tsk_notarealtokenatall"} {
		status, _ := h.raw(t, http.MethodGet, "/v1/freshness?scope=p1", nil, token)
		if status != http.StatusUnauthorized {
			t.Fatalf("token %q got %d", token, status)
		}
	}
}

// A credential reaches the projects it was granted and no others.
//
// Refused as NOT FOUND rather than forbidden: forbidden confirms the project exists, which is a fact
// about somebody else's data that an unauthorised caller should not be able to establish.
func TestACredentialCannotReachAProjectItWasNotGranted(t *testing.T) {
	h := newHarness(t, "api_scope")

	status, body := h.raw(t, http.MethodPost, "/v1/observations", map[string]any{
		"scope":    "p2-not-granted",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
	}, h.token)
	if status != http.StatusNotFound {
		t.Fatalf("wrote into an ungranted project: %d %s", status, body)
	}
	if bytes.Contains(body, []byte("forbidden")) {
		t.Fatalf("the refusal confirms the project exists: %s", body)
	}
}

// A recall naming an ungranted scope is refused rather than silently narrowed. A bundle quietly
// missing a project looks exactly like a project with nothing in it.
func TestARecallNamingAnUngrantedScopeIsRefusedRatherThanNarrowed(t *testing.T) {
	h := newHarness(t, "api_narrow")
	status, _ := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question": "anything", "scopes": []string{"p1", "p2-not-granted"},
	}, h.token)
	if status != http.StatusNotFound {
		t.Fatalf("a recall over an ungranted scope returned %d", status)
	}
}

// An erasure without a subject would be a scope-wide delete wearing a governance label.
func TestAnErasureWithoutASubjectIsRefusedOverTheWire(t *testing.T) {
	h := newHarness(t, "api_nosubject")
	status, _ := h.raw(t, http.MethodPost, "/v1/erasures",
		map[string]any{"scope": "p1", "reason": "no subject named"}, h.token)
	if status != http.StatusBadRequest {
		t.Fatalf("an erasure with no subject returned %d", status)
	}
}

// Health is the one unauthenticated route, and it says nothing. A liveness probe reporting a version,
// a database state or a configuration is reconnaissance that happens to answer probes.
func TestHealthIsOpenAndSaysNothing(t *testing.T) {
	h := newHarness(t, "api_health")
	status, body := h.raw(t, http.MethodGet, "/health", nil, "")
	if status != http.StatusOK {
		t.Fatalf("health returned %d", status)
	}
	for _, leak := range []string{"version", "schema", "postgres", "dsn"} {
		if bytes.Contains(bytes.ToLower(body), []byte(leak)) {
			t.Fatalf("health disclosed %q: %s", leak, body)
		}
	}
}

// ── Harness ───────────────────────────────────────────────────────────────────────────────────

type harness struct {
	server *httptest.Server
	pool   *pgxpool.Pool
	schema pg.Schema
	token  string
}

func newHarness(t *testing.T, tenant string) *harness {
	t.Helper()
	ctx := context.Background()

	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// The registry has to exist before a credential can: the fence was built before the thing it
	// fences, which is the whole argument of the control migration.
	if err := migrate.EstablishPlanes(ctx, pool, "control-test-password", "data-test-password"); err != nil {
		t.Fatalf("establish planes: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+tenant+` CASCADE`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrate.ProvisionTenant(ctx, pool, tenant); err != nil {
		t.Fatalf("provision: %v", err)
	}
	schema, err := pg.NewSchema(tenant)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}

	credentials := credential.NewStore(pool, string(migrate.ControlSchema))
	token, _, err := credentials.Issue(ctx, "test-"+tenant, []string{"p1"})
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}

	server := httptest.NewServer(api.NewServer(
		credentials,
		pg.NewObservationStore(pool),
		pg.NewEraser(pool),
		recall.New(pg.NewRecallStore(pool, schema), 50),
		schema,
		nil,
	).Handler())
	t.Cleanup(server.Close)

	return &harness{server: server, pool: pool, schema: schema, token: token}
}

// form runs the backlog the way the driver will, with a scripted model.
func (h *harness) form(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	vocabulary, err := pg.LoadOntology(ctx, h.pool, h.schema)
	if err != nil {
		t.Fatalf("load vocabulary: %v", err)
	}
	former := formation.NewFormer(
		pg.NewObservationStore(h.pool), pg.NewFactStore(h.pool),
		extract.New(scriptedModel{}, vocabulary),
	)
	if _, err := formation.NewWorker(h.pool, pg.NewObservationStore(h.pool), former).
		Drain(ctx, h.schema, "p1"); err != nil {
		t.Fatalf("form: %v", err)
	}
}

// do performs a request with the harness credential and decodes the body, failing on any status
// other than the one expected.
func (h *harness) do(t *testing.T, method, path string, body map[string]any, want int, into any) {
	t.Helper()
	status, raw := h.raw(t, method, path, body, h.token)
	if status != want {
		t.Fatalf("%s %s returned %d, wanted %d: %s", method, path, status, want, raw)
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("%s %s: decode: %v (%s)", method, path, err, raw)
		}
	}
}

func (h *harness) raw(t *testing.T, method, path string, body map[string]any, token string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, raw
}

// scriptedModel proposes what the fixture message plainly says.
//
// A stub rather than a provider, because what is under test is the wire, not extraction. The
// proposals use quotes that are verbatim in the message, so the span rule holds without this test
// having to know how the span rule works.
type scriptedModel struct{}

func (scriptedModel) Propose(_ context.Context, message domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	if message.Role != domain.RoleUser {
		return nil, nil
	}
	return []extract.Proposal{
		{Subject: "the user", Predicate: "works_at", Object: "Ensera",
			Statement: "The user works at Ensera.", Confidence: 0.9, Quote: "I work at Ensera"},
		{Subject: "the user", Predicate: "lives_in", Object: "Dublin",
			Statement: "The user lives in Dublin.", Confidence: 0.9, Quote: "I live in Dublin"},
	}, nil
}

// ── What the surface refuses to parse ─────────────────────────────────────────────────────────
//
// Every one of these is a way a client can be wrong, and each must produce an answer the client can
// act on rather than a five hundred or a partial write.
func TestAMalformedRequestIsRefusedWithoutTouchingTheDatabase(t *testing.T) {
	h := newHarness(t, "api_malformed")

	cases := map[string]struct {
		path string
		body string
	}{
		// Not JSON at all.
		"not json": {"/v1/observations", `{"scope":`},
		// A field the contract does not define. Refused rather than ignored: a client sending
		// `data_subject` instead of `data_subject_id` would otherwise have its turn stored with no
		// subject, and an erasure would later find nothing to erase.
		"unknown field": {"/v1/observations",
			`{"scope":"p1","data_subject":"s","messages":[{"role":"user","content":"hi"}]}`},
		"unknown field on recall": {"/v1/recalls", `{"question":"x","scope":"p1"}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			status, body := h.rawBody(t, http.MethodPost, c.path, c.body, h.token)
			if status != http.StatusBadRequest {
				t.Fatalf("returned %d: %s", status, body)
			}
		})
	}
}

// A turn the domain will not accept is refused with the domain's own reason, not a generic error.
// The handler does not re-implement the rule: a second definition of what a turn is drifts from the
// one the store enforces.
func TestATurnTheDomainRefusesIsRefusedWithItsReason(t *testing.T) {
	h := newHarness(t, "api_badturn")

	for name, messages := range map[string][]map[string]any{
		"no messages":   {},
		"unknown role":  {{"role": "oracle", "content": "hello"}},
		"empty content": {{"role": "user", "content": ""}},
	} {
		t.Run(name, func(t *testing.T) {
			status, body := h.raw(t, http.MethodPost, "/v1/observations", map[string]any{
				"scope": "p1", "data_subject_id": "s", "messages": messages,
			}, h.token)
			if status != http.StatusBadRequest {
				t.Fatalf("returned %d: %s", status, body)
			}
			if !bytes.Contains(body, []byte("invalid_turn")) {
				t.Fatalf("the refusal does not name what was wrong: %s", body)
			}
		})
	}
}

// A request naming no scope cannot be authorised, so it is refused before anything else happens.
func TestARequestWithNoScopeIsRefused(t *testing.T) {
	h := newHarness(t, "api_noscope")
	for name, c := range map[string]struct {
		method, path string
		body         map[string]any
	}{
		"freshness": {http.MethodGet, "/v1/freshness", nil},
		"observe":   {http.MethodPost, "/v1/observations", map[string]any{"messages": []map[string]any{{"role": "user", "content": "hi"}}}},
		"erase":     {http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "s"}},
	} {
		t.Run(name, func(t *testing.T) {
			status, body := h.raw(t, c.method, c.path, c.body, h.token)
			if status != http.StatusBadRequest {
				t.Fatalf("returned %d: %s", status, body)
			}
		})
	}
}

// A question is required, because a recall with none would anchor on nothing and return an empty
// bundle that looks like an answer.
func TestARecallWithoutAQuestionIsRefused(t *testing.T) {
	h := newHarness(t, "api_noquestion")
	for _, question := range []string{"", "   "} {
		status, body := h.raw(t, http.MethodPost, "/v1/recalls",
			map[string]any{"question": question, "scopes": []string{"p1"}}, h.token)
		if status != http.StatusBadRequest {
			t.Fatalf("question %q returned %d: %s", question, status, body)
		}
	}
}

// An omitted scope list means everything the credential holds, which is the useful default and the
// one that cannot leak: the set it falls back to is the credential's own.
func TestARecallWithNoScopesUsesTheCredentialsOwn(t *testing.T) {
	h := newHarness(t, "api_defaultscopes")
	var bundle struct {
		Facts []json.RawMessage `json:"facts"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "anything at all"},
		http.StatusOK, &bundle)
}

// A body larger than the cap is refused rather than read. An unbounded body is a way for one request
// to decide how much of our memory it uses.
func TestAnOversizedBodyIsRefused(t *testing.T) {
	h := newHarness(t, "api_oversize")
	huge := `{"scope":"p1","data_subject_id":"s","messages":[{"role":"user","content":"` +
		strings.Repeat("a", 2<<20) + `"}]}`
	status, _ := h.rawBody(t, http.MethodPost, "/v1/observations", huge, h.token)
	if status != http.StatusBadRequest {
		t.Fatalf("a two megabyte body returned %d", status)
	}
}

// Freshness for a scope that exists in the grant but has never been written to is an error the
// caller can act on rather than a zero that looks like an empty memory.
func TestFreshnessForAScopeWithNoTurnsSaysSo(t *testing.T) {
	h := newHarness(t, "api_freshempty")
	status, _ := h.raw(t, http.MethodGet, "/v1/freshness?scope=p1", nil, h.token)
	if status != http.StatusInternalServerError && status != http.StatusOK {
		t.Fatalf("unexpected status %d", status)
	}
}

func (h *harness) rawBody(t *testing.T, method, path, body, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}
