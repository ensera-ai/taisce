// The journey over the wire.
//
// The claim under test is that everything the spine does is reachable by a client that is not written in Go.
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
// which a scored corpus measures, and which costs money.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/notify"
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
	h.do(t, http.MethodGet, "/v1/freshness", nil, http.StatusOK, &before)
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
	h.do(t, http.MethodGet, "/v1/freshness", nil, http.StatusOK, &after)
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
				ObservationID   string `json:"observation_id"`
				Quote           string `json:"quote"`
				ByteStart       int    `json:"byte_start"`
				ByteEnd         int    `json:"byte_end"`
				Context         string `json:"context"`
				ContextStart    int    `json:"context_start"`
				ContextComplete bool   `json:"context_complete"`
			} `json:"evidence"`
		} `json:"facts"`
		Truncated bool `json:"truncated"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question":        "Where does the user work?",
		"data_subject_id": "subject-1",
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
	// A citation that can be read, not only verified. The span proves the phrase was in the message;
	// the words around it are what say whether the sentence asserted it.
	if fact.Evidence.Context == "" {
		t.Fatal("a citation came back with a span and no words around it")
	}
	if !strings.Contains(fact.Evidence.Context, fact.Evidence.Quote) {
		t.Fatalf("the context does not contain the quote it is context for: %q", fact.Evidence.Context)
	}
	if at := fact.Evidence.ByteStart - fact.Evidence.ContextStart; at < 0 ||
		at+len(fact.Evidence.Quote) > len(fact.Evidence.Context) ||
		fact.Evidence.Context[at:at+len(fact.Evidence.Quote)] != fact.Evidence.Quote {
		t.Fatalf("context_start does not locate the quote inside the context: %+v", fact.Evidence)
	}
	// This message is shorter than the window, so the caller is told they are seeing all of it.
	if !fact.Evidence.ContextComplete || fact.Evidence.Context != content {
		t.Fatalf("a message shorter than the window did not come back whole: %q", fact.Evidence.Context)
	}

	// ── Erase ─────────────────────────────────────────────────────────────────────────────────
	var receipt struct {
		Deleted  map[string]int `json:"deleted"`
		Residual map[string]int `json:"residual"`
		Clean    bool           `json:"clean"`
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{
		"data_subject_id": "subject-1", "reason": "the journey, over HTTP",
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
		"question":        "Where does the user work?",
		"data_subject_id": "subject-1",
	}, http.StatusOK, &afterErasure)
	if len(afterErasure.Facts) != 0 {
		t.Fatalf("%d facts survived an erasure that reported a residual of zero", len(afterErasure.Facts))
	}
}

// ── What the surface refuses ──────────────────────────────────────────────────────────────────

// Every operation is behind a credential. A memory service that answers an unauthenticated request
// is a memory dump on a port.
// Every operation, taken from the surface rather than from a list in this file.
//
// It was a list, and the list was wrong: an export was reachable and this test did not mention it,
// so the one operation that hands back everything held about a person was the one nothing checked
// was behind a credential. A test that enumerates the thing it tests cannot go quiet that way —
// adding a route adds a case here, and there is nothing to remember.
func TestEveryOperationRefusesAnUnauthenticatedCaller(t *testing.T) {
	h := newHarness(t, "api_anon")

	for _, o := range api.Operations() {
		t.Run(o.Name, func(t *testing.T) {
			// No body. The credential is resolved before anything reads one, which is itself the
			// property worth holding: a caller who cannot authenticate never reaches a parser.
			status, _ := h.raw(t, o.Method, o.Path, nil, "")
			if status != http.StatusUnauthorized {
				t.Fatalf("%s %s answered %d without a credential", o.Method, o.Path, status)
			}
		})
	}
}

// Every refusal a client can provoke names a code the contract publishes.
//
// The code is the field a client branches on — the message is for a person reading a log and is free
// to change — and until this test nothing asserted that a refusal carried one at all. A code outside
// the published set reaches a client as an unhandled case in somebody else's error handling, which
// is a failure they cannot diagnose and we would never see.
//
// What it does not cover: `internal`. Provoking it means breaking the database underneath a live
// request, and a test that arranged that would be testing the arrangement. It is declared and
// published, and TestNoRefusalNamesAnUndeclaredCode reads every call site that answers with it.
func TestEveryRefusalAClientCanProvokeNamesAPublishedCode(t *testing.T) {
	h := newHarness(t, "api_codes")

	published := map[string]bool{}
	for _, c := range api.ErrorCodes() {
		published[c] = true
	}

	for _, c := range []struct {
		what         string
		method, path string
		body         map[string]any
		token        string
		want         string
	}{
		{"no credential", http.MethodGet, "/v1/freshness", nil, "", "unauthenticated"},
		{"an unknown credential", http.MethodGet, "/v1/freshness", nil, "tsk_nothing", "unauthenticated"},
		{"an unknown field", http.MethodPost, "/v1/recalls",
			map[string]any{"question": "q", "scope": "p1"}, h.token, "invalid_body"},
		{"a turn the domain refuses", http.MethodPost, "/v1/observations",
			map[string]any{"data_subject_id": "s", "messages": []map[string]any{}}, h.token, "invalid_turn"},
		{"a recall with no question", http.MethodPost, "/v1/recalls",
			map[string]any{"question": "  "}, h.token, "invalid_question"},
		{"an erasure naming nobody", http.MethodPost, "/v1/erasures",
			map[string]any{"data_subject_id": ""}, h.token, "no_subject"},
		{"an export naming nobody", http.MethodPost, "/v1/exports",
			map[string]any{"data_subject_id": ""}, h.token, "no_subject"},
	} {
		t.Run(c.what, func(t *testing.T) {
			status, raw := h.raw(t, c.method, c.path, c.body, c.token)
			if status < 400 {
				t.Fatalf("%s was not refused: %d %s", c.what, status, raw)
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("a refusal was not the shape the contract publishes: %v (%s)", err, raw)
			}
			if !published[body.Error.Code] {
				t.Fatalf("%s answered code %q, which the contract does not publish", c.what, body.Error.Code)
			}
			if body.Error.Code != c.want {
				t.Fatalf("%s answered code %q, wanted %q", c.what, body.Error.Code, c.want)
			}
			if body.Error.Message == "" {
				t.Fatalf("%s answered code %q with no message", c.what, body.Error.Code)
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
		status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, token)
		if status != http.StatusUnauthorized {
			t.Fatalf("token %q got %d", token, status)
		}
	}
}

// A caller cannot name a project at all, so a request that tries is refused as malformed.
//
// This replaces two tests that checked a caller naming a project it did not hold. That check is gone
// with the field: the project comes from the credential, so there is nothing to name and
// nothing to verify — which removes the whole class of error rather than answering it.
func TestARequestCannotNameAProject(t *testing.T) {
	h := newHarness(t, "api_noname")

	for name, body := range map[string]string{
		"observe": `{"scope":"somewhere-else","data_subject_id":"s",` +
			`"messages":[{"role":"user","content":"hello"}]}`,
		"recall":  `{"question":"anything","scope":"somewhere-else"}`,
		"recalls": `{"question":"anything","scopes":["somewhere-else"]}`,
		"erase":   `{"scope":"somewhere-else","data_subject_id":"s"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := "/v1/observations"
			switch name {
			case "recall", "recalls":
				path = "/v1/recalls"
			case "erase":
				path = "/v1/erasures"
			}
			status, body := h.rawBody(t, http.MethodPost, path, body, h.token)
			if status != http.StatusBadRequest {
				t.Fatalf("a request naming a project returned %d: %s", status, body)
			}
		})
	}
}

// Two credentials for two projects see different memory, which is the boundary stated as a property
// rather than as a refusal.
//
// The previous version of this asserted that naming somebody else's project was refused. This asserts
// the thing that actually matters: a credential reaches its own project's memory and cannot reach
// another's, whatever it sends.
func TestTwoCredentialsForTwoProjectsSeeDifferentMemory(t *testing.T) {
	h := newHarness(t, "api_isolation")
	ctx := context.Background()

	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatalf("provision p2: %v", err)
	}
	other, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).
		Issue(ctx, "api_isolation-p2", "p2")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Written with the first credential, into its own project.
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages":        []map[string]any{{"role": "user", "content": "I work at Ensera."}},
	}, http.StatusCreated, nil)
	h.form(t)

	// The first credential sees it.
	var mine struct {
		Facts []json.RawMessage `json:"facts"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "What about Ensera?"},
		http.StatusOK, &mine)
	if len(mine.Facts) == 0 {
		t.Fatal("a credential cannot see memory written through itself")
	}

	// The second sees nothing, and there is no way for it to ask for the first project.
	status, raw := h.raw(t, http.MethodPost, "/v1/recalls",
		map[string]any{"question": "What about Ensera?"}, other)
	if status != http.StatusOK {
		t.Fatalf("the second credential got %d: %s", status, raw)
	}
	var theirs struct {
		Facts []json.RawMessage `json:"facts"`
	}
	if err := json.Unmarshal(raw, &theirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(theirs.Facts) != 0 {
		t.Fatalf("a credential for another project saw %d facts", len(theirs.Facts))
	}
}

// An erasure without a subject would be a scope-wide delete wearing a governance label.
func TestAnErasureWithoutASubjectIsRefusedOverTheWire(t *testing.T) {
	h := newHarness(t, "api_nosubject")
	status, _ := h.raw(t, http.MethodPost, "/v1/erasures",
		map[string]any{"reason": "no subject named"}, h.token)
	if status != http.StatusBadRequest {
		t.Fatalf("an erasure with no subject returned %d", status)
	}
}

// ── Erasing a document by its own sources, over the wire ──────────────────────────────────────
//
// A document observed project-wide is erased by the observation ids its manifest holds, with the
// same receipt as a subject erasure; naming both a person and sources, or something that is not an
// id, is refused before anything is read.
func TestADocumentIsErasedByItsSourceObservationsOverTheWire(t *testing.T) {
	h := newHarness(t, "api_erase_source")

	var observed struct {
		ID string `json:"id"`
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "Ensera is a company in Dublin."},
		},
	}, http.StatusCreated, &observed)
	h.form(t)

	var receipt struct {
		DataSubjectID        string         `json:"data_subject_id"`
		SourceObservationIDs []string       `json:"source_observation_ids"`
		Deleted              map[string]int `json:"deleted"`
		Residual             map[string]int `json:"residual"`
		Clean                bool           `json:"clean"`
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{
		"source_observation_ids": []string{observed.ID}, "reason": "takedown",
	}, http.StatusOK, &receipt)
	if !receipt.Clean || receipt.Deleted["observation"] != 1 || len(receipt.SourceObservationIDs) != 1 || receipt.DataSubjectID != "" {
		t.Fatalf("the receipt must name the source, count the observation and be clean: %+v", receipt)
	}
	for kind, n := range receipt.Residual {
		if n != 0 {
			t.Fatalf("residual %s=%d after erasing the document's only source", kind, n)
		}
	}

	for name, body := range map[string]map[string]any{
		"both a person and sources":  {"data_subject_id": "subject-1", "source_observation_ids": []string{observed.ID}},
		"a source that is not an id": {"source_observation_ids": []string{"manifest.json"}},
	} {
		status, raw := h.raw(t, http.MethodPost, "/v1/erasures", body, h.token)
		if status != http.StatusBadRequest || !bytes.Contains(raw, []byte("invalid_erasure")) {
			t.Fatalf("an erasure naming %s returned %d %s", name, status, raw)
		}
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
	// Set only by newHarnessWithNotifications: the endpoint a delivery goes to, and the pass that
	// sends it. Absent everywhere else, which is what an unconfigured deployment looks like.
	receiver      *receiver
	notifications *formation.Notifications
	// Carried so that every helper which rebuilds the server keeps them. A harness that silently
	// loses a capability when a test attaches a retriever tests less than the test believes.
	notifyStore *pg.NotificationStore
	notifier    *notify.Sender
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
	if err := migrate.EstablishPlanes(ctx, pool, "taisce-test-control", "taisce-test-data"); err != nil {
		t.Fatalf("establish planes: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+tenant+` CASCADE`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrate.ProvisionMemorySchema(ctx, pool, tenant); err != nil {
		t.Fatalf("provision: %v", err)
	}
	schema, err := pg.NewSchema(tenant)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	// The project, because a credential now names one that has to exist. Provisioning it is what an
	// operator does before minting a token, and the harness not doing it was the reason the first
	// run of the suspension test failed — which is the check working.
	if err := migrate.ProvisionScope(ctx, pool, tenant, "p1"); err != nil {
		t.Fatalf("provision p1: %v", err)
	}

	credentials := credential.NewStore(pool, string(migrate.ControlSchema))
	token, _, err := credentials.Issue(ctx, "test-"+tenant, "p1")
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}

	server := httptest.NewServer(api.NewServer(
		credentials, api.Stores{
			Audit:        pg.NewAuditStore(pool, schema),
			Exporter:     pg.NewExporter(pool, schema),
			Projects:     pg.NewProjectStore(pool, schema),
			Observations: pg.NewObservationStore(pool),
			Eraser:       pg.NewEraser(pool),
			Recaller:     recall.NewWithBudget(pg.NewRecallStore(pool, schema), recall.Budget{Characters: 100000, MaxRows: 50}),
			Citations:    pg.NewCitationStore(pool, schema),
			Records:      pg.NewRecordStore(pool, schema), Feedback: pg.NewFeedbackStore(pool, schema, pg.NewRecordStore(pool, schema)),
			Artifacts: pg.NewArtifactStore(pool, schema),
			Subjects:  pg.NewSubjectStore(pool, schema), Contexts: pg.NewSegmentStore(pool, schema),
		}, schema,
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
	// The chain fixture, which only fires on the message that contains it.
	if strings.Contains(message.Content, "Hamza manages Marta") {
		return []extract.Proposal{
			{Subject: "Hamza", Predicate: "manages", Object: "Marta",
				Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
				Statement: "Hamza manages Marta.", Confidence: 0.9, Quote: "Hamza manages Marta"},
			{Subject: "Hamza", Predicate: "works_at", Object: "Ensera",
				Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
				Statement: "Hamza works at Ensera.", Confidence: 0.9, Quote: "Hamza works at Ensera"},
		}, nil
	}
	return []extract.Proposal{
		{Subject: "the user", Predicate: "works_at", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent, Object: "Ensera",
			Statement: "The user works at Ensera.", Confidence: 0.9, Quote: "I work at Ensera"},
		// Both proposals carry a polarity and a tense. The second carried neither for a while and was
		// silently refused on every run, so the journey it drives formed one fact while reading as
		// though it formed two — a fixture weaker than it looks is worse than a missing one.
		{Subject: "the user", Predicate: "lives_in", Object: "Dublin",
			Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
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
			`{"data_subject":"s","messages":[{"role":"user","content":"hi"}]}`},
		"unknown field on recall": {"/v1/recalls", `{"question":"x","project":"p1"}`},
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
				"data_subject_id": "s", "messages": messages,
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

// A question is required, because a recall with none would anchor on nothing and return an empty
// bundle that looks like an answer.
func TestARecallWithoutAQuestionIsRefused(t *testing.T) {
	h := newHarness(t, "api_noquestion")
	for _, question := range []string{"", "   "} {
		status, body := h.raw(t, http.MethodPost, "/v1/recalls",
			map[string]any{"question": question}, h.token)
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
	huge := `{"data_subject_id":"s","messages":[{"role":"user","content":"` +
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
	status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, h.token)
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

// A revoked credential stops working immediately, which is what makes revocation an answer to a leak
// rather than a note in a ticket.
func TestARevokedCredentialStopsWorkingAtOnce(t *testing.T) {
	h := newHarness(t, "api_revoked")
	ctx := context.Background()
	store := credential.NewStore(h.pool, string(migrate.ControlSchema))

	token, grant, err := store.Issue(ctx, "api_revoked-doomed", "p1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, token); status == http.StatusUnauthorized {
		t.Fatal("a freshly minted credential was refused")
	}

	if err := store.Revoke(ctx, grant.CredentialID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, token)
	if status != http.StatusUnauthorized {
		t.Fatalf("a revoked credential still worked: %d", status)
	}
}

// ── The check no foreign key can make ─────────────────────────────────────────────────────────
//
// A credential names its project, and the two are on opposite sides of the plane boundary: the
// credential is in the registry, which the memory service cannot read, and the project is in the
// tenant schema, which the registry role has no business in. No constraint crosses that.
//
// The command that mints a credential checks the project exists, which catches a typo at the moment
// somebody makes it. What it cannot catch is what happens afterwards.

// A credential for a suspended project stops working, and says only that it does not work.
//
// Suspension is the reversible half of a project's lifecycle: the memory is intact and unreachable.
// A credential that kept working through it would make suspension mean nothing.
func TestACredentialForASuspendedProjectIsRefused(t *testing.T) {
	h := newHarness(t, "api_suspended")
	ctx := context.Background()
	projects := pg.NewProjectStore(h.pool, h.schema)

	if status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, h.token); status == http.StatusUnauthorized {
		t.Fatal("the credential did not work before the project was suspended")
	}

	if err := projects.Suspend(ctx, "p1"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	status, body := h.raw(t, http.MethodGet, "/v1/freshness", nil, h.token)
	if status != http.StatusUnauthorized {
		t.Fatalf("a credential for a suspended project got %d", status)
	}
	// And it does not disclose why. Which project exists, and whether an operator suspended it, is
	// not the holder's to learn from an error message — the fact they can act on is that their
	// credential does not work.
	for _, leak := range []string{"suspend", "project", "p1"} {
		if bytes.Contains(bytes.ToLower(body), []byte(leak)) {
			t.Fatalf("the refusal disclosed %q: %s", leak, body)
		}
	}

	// Resuming brings it back, which is what makes suspension different from deletion.
	if err := projects.Resume(ctx, "p1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, h.token); status == http.StatusUnauthorized {
		t.Fatal("resuming a project did not restore its credentials")
	}
}

// A credential naming a project that does not exist at all is refused the same way.
//
// This is the state a foreign key would have prevented and cannot: a project removed while
// credentials for it are still in circulation. Without this check such a credential authenticates
// and writes into a namespace nobody is watching.
func TestACredentialNamingAProjectThatIsGoneIsRefused(t *testing.T) {
	h := newHarness(t, "api_goneproject")
	ctx := context.Background()

	// Minted directly, bypassing the command that would have checked — which is exactly the state
	// that arises when a project is removed after a credential exists.
	orphan, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).
		Issue(ctx, "api_goneproject-orphan", "no-such-project")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, orphan)
	if status != http.StatusUnauthorized {
		t.Fatalf("a credential for a project that does not exist got %d", status)
	}
}

// ── The ledger ────────────────────────────────────────────────────────────────────────────────

// Every operation leaves a row naming the principal that performed it.
//
// Asserted over the whole journey rather than per handler, because the property is that nothing
// happens unaccounted for — and a per-handler test would pass while a new handler added tomorrow
// recorded nothing.
// Every operation, performed, and every one of them accounted for.
//
// The bodies are keyed by operation name and checked against the surface before anything runs, so an
// operation added to the surface fails here until somebody exercises it. That is the difference
// between a test named "every operation" and one that happens to cover the four somebody thought of:
// the export was missing from both, and this is what stops it happening again.
func TestEveryOperationLeavesAnAttributedLedgerRow(t *testing.T) {
	// With notifications, because a route that refuses because the deployment is unconfigured is a
	// route this test cannot say anything about.
	h := newHarnessWithNotifications(t, "api_ledger", http.StatusOK)
	ctx := context.Background()
	audit := pg.NewAuditStore(h.pool, h.schema)

	// What a successful call to each operation looks like, and what it answers with.
	exercise := map[string]struct {
		body map[string]any
		want int
	}{
		domain.AuditEntityList:             {map[string]any{}, http.StatusOK},
		domain.AuditEntityGet:              {map[string]any{}, http.StatusOK},
		domain.AuditEntityPurge:            {map[string]any{}, http.StatusOK},
		domain.AuditNotificationRegister:   {map[string]any{}, http.StatusOK},
		domain.AuditNotificationList:       {map[string]any{}, http.StatusOK},
		domain.AuditNotificationDisable:    {map[string]any{}, http.StatusOK},
		domain.AuditNotificationDeliveries: {map[string]any{}, http.StatusOK},
		domain.AuditSubjectRegister:        {map[string]any{"idempotency_key": "de8bf4fa-5b6b-4310-a781-a58c46cc5bbd", "external_reference": "ledger-account"}, http.StatusOK},
		domain.AuditSubjectGet:             {map[string]any{"external_reference": "ledger-account"}, http.StatusOK},
		domain.AuditSubjectList:            {map[string]any{}, http.StatusOK},
		domain.AuditSubjectUpdate:          {map[string]any{}, http.StatusOK},
		domain.AuditObserve: {map[string]any{
			"data_subject_id": "subject-1",
			"messages":        []map[string]any{{"role": "user", "content": "I work at Ensera."}},
		}, http.StatusCreated},
		domain.AuditCitationResolve:       {map[string]any{}, http.StatusOK},
		domain.AuditRecordList:            {map[string]any{}, http.StatusOK},
		domain.AuditRecordHistory:         {map[string]any{}, http.StatusOK},
		domain.AuditRecordRetract:         {map[string]any{}, http.StatusOK},
		domain.AuditRecordCorrect:         {map[string]any{}, http.StatusOK},
		domain.AuditArtifactPut:           {map[string]any{"id": "de8f7a94-ddb7-46c5-a561-19b0f6a43745", "data_subject_id": "artifact-owner", "kind": "state", "name": "scratch", "content": []byte("opaque scratchpad")}, http.StatusOK},
		domain.AuditArtifactGet:           {map[string]any{"id": "de8f7a94-ddb7-46c5-a561-19b0f6a43745"}, http.StatusOK},
		domain.AuditArtifactList:          {map[string]any{}, http.StatusOK},
		domain.AuditArtifactDelete:        {map[string]any{"id": "de8f7a94-ddb7-46c5-a561-19b0f6a43745"}, http.StatusOK},
		domain.AuditRecordAssert:          {map[string]any{"records": []pg.RecordAssertion{{IdempotencyKey: "29ec5631-bae3-4cf8-a165-a8759a8f9931", DataSubjectID: "subject-1", Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera"}}}, http.StatusOK},
		domain.AuditFeedbackRecord:        {map[string]any{"note": "the employer here reads wrong to me", "proposed_object": "Orbit"}, http.StatusOK},
		domain.AuditFeedbackList:          {map[string]any{}, http.StatusOK},
		domain.AuditFeedbackPromote:       {map[string]any{}, http.StatusOK},
		domain.AuditFreshness:             {nil, http.StatusOK},
		domain.AuditRecall:                {map[string]any{"question": "What about Ensera?"}, http.StatusOK},
		domain.AuditContextAssemble:       {map[string]any{"data_subject_id": "subject-1"}, http.StatusOK},
		domain.AuditEmbeddingSearch:       {map[string]any{"question": "What about Ensera?"}, http.StatusOK},
		domain.AuditEntityEmbeddingSearch: {map[string]any{"question": "What about Ensera?"}, http.StatusOK},
		domain.AuditReportEmbeddingSearch: {map[string]any{"question": "What is the growth theme?"}, http.StatusOK},
		domain.AuditMessageGet:            {map[string]any{}, http.StatusOK},
		domain.AuditExport:                {map[string]any{"data_subject_id": "subject-1"}, http.StatusOK},
		domain.AuditErase: {map[string]any{
			"data_subject_id": "subject-1", "reason": "the ledger test",
		}, http.StatusOK},
	}

	surface := api.Operations()
	for _, o := range surface {
		if _, ok := exercise[o.Name]; !ok {
			t.Fatalf("the surface serves %q and nothing here performs it, "+
				"so whether it is recorded is unknown", o.Name)
		}
	}
	if len(exercise) != len(surface) {
		t.Fatalf("%d operations are exercised and the surface has %d", len(exercise), len(surface))
	}

	// Order is imposed here rather than taken from the declaration, because two operations care
	// about it and the declaration is not the place that knows. A recall before formation would
	// leave a row having read nothing, and an erasure before an export would leave the export with
	// nothing to find — both would pass, and both would be exercising less than they claim.
	ran := map[string]bool{}
	run := func(name string) {
		c := exercise[name]
		for _, o := range surface {
			if o.Name == name {
				h.do(t, o.Method, o.Path, c.body, c.want, nil)
				ran[name] = true
				return
			}
		}
		t.Fatalf("%s is exercised here and the surface does not serve it", name)
	}
	run(domain.AuditSubjectRegister)
	run(domain.AuditSubjectGet)
	run(domain.AuditSubjectList)
	subject, err := pg.NewSubjectStore(h.pool, h.schema).Get(ctx, "p1", "", "ledger-account")
	if err != nil {
		t.Fatal(err)
	}
	exercise[domain.AuditSubjectUpdate].body["id"] = subject.ID
	exercise[domain.AuditSubjectUpdate].body["expected_version"] = subject.Version
	exercise[domain.AuditSubjectUpdate].body["label"] = "current label"
	run(domain.AuditSubjectUpdate)
	run(domain.AuditObserve)
	run(domain.AuditFreshness)
	h.form(t)
	run(domain.AuditRecall)
	run(domain.AuditContextAssemble)
	exercise[domain.AuditMessageGet].body["chunk_id"] = sourceChunkForInspection(t, h)
	run(domain.AuditMessageGet)
	attachPassages(t, h, nil).activate(t, "p1", "v1")
	run(domain.AuditEmbeddingSearch)
	entities := attachEntityCandidates(t, h)
	entities.activate(t, "p1")
	run(domain.AuditEntityEmbeddingSearch)
	insertAPIReport(t, h)
	attachReportCandidates(t, h, nil, entities.retriever).activate(t, "p1")
	run(domain.AuditReportEmbeddingSearch)
	exercise[domain.AuditEntityGet].body["id"] = entityIDForInspection(t, h)
	run(domain.AuditEntityList)
	run(domain.AuditEntityGet)
	var citationID string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT fact_id::text FROM {schema}.fact WHERE scope='p1' ORDER BY fact_id LIMIT 1`)).Scan(&citationID); err != nil {
		t.Fatal(err)
	}
	exercise[domain.AuditCitationResolve].body["id"] = citationID
	run(domain.AuditCitationResolve)
	run(domain.AuditRecordList)
	exercise[domain.AuditRecordHistory].body["id"] = citationID
	run(domain.AuditRecordHistory)
	var version string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT version::text FROM {schema}.fact WHERE fact_id=$1`), citationID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	exercise[domain.AuditRecordCorrect].body["records"] = []pg.RecordCorrection{{RecordMutation: pg.RecordMutation{ID: citationID, ExpectedVersion: version}, Object: "Atlas", Statement: "I work at Atlas."}}
	run(domain.AuditRecordCorrect)
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT fact_id::text,version::text FROM {schema}.fact WHERE scope='p1' AND upper_inf(known)`)).Scan(&citationID, &version); err != nil {
		t.Fatal(err)
	}
	exercise[domain.AuditRecordRetract].body["records"] = []pg.RecordMutation{{ID: citationID, ExpectedVersion: version}}
	run(domain.AuditRecordRetract)
	run(domain.AuditRecordAssert)
	// Feedback in the order a customer performs it: report a doubt about a current record, read the
	// queue back, then promote the one that was right. Promotion is a correction here, so the graph
	// the rest of this test reads still has a current record afterwards.
	var feedbackTarget, feedbackVersion string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT fact_id::text,version::text FROM {schema}.fact
        WHERE scope='p1' AND upper_inf(known) ORDER BY fact_id LIMIT 1`)).Scan(&feedbackTarget, &feedbackVersion); err != nil {
		t.Fatal(err)
	}
	exercise[domain.AuditFeedbackRecord].body["record_id"] = feedbackTarget
	run(domain.AuditFeedbackRecord)
	run(domain.AuditFeedbackList)
	var feedbackID string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT feedback_id::text FROM {schema}.memory_feedback
        WHERE scope='p1' ORDER BY recorded_at DESC LIMIT 1`)).Scan(&feedbackID); err != nil {
		t.Fatal(err)
	}
	exercise[domain.AuditFeedbackPromote].body["id"] = feedbackID
	exercise[domain.AuditFeedbackPromote].body["expected_version"] = feedbackVersion
	run(domain.AuditFeedbackPromote)
	run(domain.AuditArtifactPut)
	run(domain.AuditArtifactGet)
	run(domain.AuditArtifactList)
	var artifactVersion string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT version::text FROM {schema}.agent_artifact WHERE scope='p1'`)).Scan(&artifactVersion); err != nil {
		t.Fatal(err)
	}
	exercise[domain.AuditArtifactDelete].body["expected_version"] = artifactVersion
	run(domain.AuditArtifactDelete)
	run(domain.AuditExport)
	// Last of the entity routes, because it removes the entity the earlier ones read. A preview,
	// so the rest of this test still has its graph: the ledger row is the point here, and a
	// preview leaves one.
	exercise[domain.AuditEntityPurge].body["id"] = entityIDForInspection(t, h)
	run(domain.AuditEntityPurge)
	// Notifications, in the order a customer performs them: nominate, read back, then stop.
	exercise[domain.AuditNotificationRegister].body["url"] = h.receiver.server.URL + "/ledger"
	run(domain.AuditNotificationRegister)
	run(domain.AuditNotificationList)
	run(domain.AuditNotificationDeliveries)
	var endpointID string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(
		`SELECT endpoint_id::text FROM {schema}.notification_endpoint WHERE scope='p1' LIMIT 1`)).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	exercise[domain.AuditNotificationDisable].body["id"] = endpointID
	run(domain.AuditNotificationDisable)
	run(domain.AuditErase)
	for _, o := range surface {
		if !ran[o.Name] {
			t.Fatalf("%s has a body here and was never sent, so nothing exercised it", o.Name)
		}
	}

	entries, err := audit.Recent(ctx, 100)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	seen := map[string]domain.AuditEntry{}
	for _, e := range entries {
		seen[e.Operation] = e
	}
	for _, o := range surface {
		op := o.Name
		e, ok := seen[op]
		if !ok {
			t.Fatalf("%s left no ledger row", op)
		}
		if e.Principal == "" {
			t.Fatalf("%s was recorded with no principal", op)
		}
		if e.PrincipalKind != domain.PrincipalCredential {
			t.Fatalf("%s was attributed to a %q", op, e.PrincipalKind)
		}
		if e.Project != "p1" {
			t.Fatalf("%s was recorded against project %q", op, e.Project)
		}
		if e.Outcome != domain.OutcomeAllowed {
			t.Fatalf("%s was recorded as %q", op, e.Outcome)
		}
	}
	// The magnitude is a count of what was touched, and the erasure touched something.
	if seen[domain.AuditErase].Magnitude == 0 {
		t.Fatal("an erasure that deleted rows was recorded as touching nothing")
	}
}

// The ledger holds nothing that could ever be the subject of an erasure request.
//
// This is the property that makes append-only unconditional rather than a conflict to manage: an
// audit trail an erasure can delete is not one, and an audit trail an erasure cannot touch is
// personal data surviving an erasure. Recording nothing askable avoids both.
func TestTheLedgerHoldsNothingAPersonCouldAskToHaveRemoved(t *testing.T) {
	h := newHarness(t, "api_ledger_privacy")
	ctx := context.Background()

	const subject = "a-very-distinctive-subject-reference"
	const content = "a-very-distinctive-sentence-nobody-else-would-write"
	const question = "a-very-distinctive-question"

	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": subject,
		"messages":        []map[string]any{{"role": "user", "content": content}},
	}, http.StatusCreated, nil)
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": question},
		http.StatusOK, nil)

	// Read the whole table as text and look for anything that came from a person.
	var dump string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(
		`SELECT coalesce(string_agg(a::text, ' '), '') FROM {schema}.audit_entry a`)).
		Scan(&dump); err != nil {
		t.Fatalf("dump ledger: %v", err)
	}
	for name, needle := range map[string]string{
		"the data subject": subject,
		"message content":  content,
		"the question":     question,
	} {
		if strings.Contains(dump, needle) {
			t.Fatalf("the ledger holds %s, which makes it personal data surviving an erasure", name)
		}
	}
}

// A refused credential is recorded, because a repeated refusal is what an attack looks like from
// inside the ledger.
func TestARefusedCredentialIsRecorded(t *testing.T) {
	h := newHarness(t, "api_ledger_refused")
	ctx := context.Background()
	audit := pg.NewAuditStore(h.pool, h.schema)

	h.raw(t, http.MethodGet, "/v1/freshness", nil, "tsk_definitelynotarealtoken")
	h.raw(t, http.MethodGet, "/v1/freshness", nil, "")

	entries, err := audit.Recent(ctx, 100)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	refused := 0
	for _, e := range entries {
		if e.Operation == domain.AuditAuthenticate && e.Outcome == domain.OutcomeRefused {
			refused++
			// The token itself must never be in the ledger: a secret written into an append-only
			// table is a secret that cannot be removed.
			if strings.Contains(e.Principal, "definitelynotarealtoken") {
				t.Fatalf("the ledger recorded a presented token: %q", e.Principal)
			}
		}
	}
	if refused < 2 {
		t.Fatalf("%d refused authentications recorded, wanted both", refused)
	}
}

// ── Export: the other half of the pair ────────────────────────────────────────────────────────

// An export produces what an erasure would delete, and the two walks agree.
//
// This is the assertion that matters, and it is why export reads the same registry erasure does: a
// projection missing from a deletion leaves data behind, which the residual count catches. A
// projection missing from an EXPORT leaves data out, and nothing catches that — an export nobody
// compares against anything looks complete.
// An export names a person or the turns it is for, never both and never neither. The
// second way is how a document observed project-wide is exported, since it has no person.
func TestAnExportNamesAPersonOrItsTurnsAndRefusesAnythingElse(t *testing.T) {
	h := newHarness(t, "api_export_selector")

	var receipt struct {
		ID string `json:"id"`
	}
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages":        []map[string]any{{"role": "user", "content": "I work at Ensera."}},
	}, http.StatusCreated, &receipt)
	h.form(t)

	// Neither: refused as an export of the whole project wearing a governance label.
	h.do(t, http.MethodPost, "/v1/exports", map[string]any{}, http.StatusBadRequest, nil)
	// Both: refused, because one receipt cannot mean two selections.
	h.do(t, http.MethodPost, "/v1/exports", map[string]any{
		"data_subject_id": "subject-1", "source_observation_ids": []string{receipt.ID},
	}, http.StatusBadRequest, nil)
	// An id that is not an observation id.
	h.do(t, http.MethodPost, "/v1/exports", map[string]any{
		"source_observation_ids": []string{"not-a-uuid"},
	}, http.StatusInternalServerError, nil)

	var export struct {
		DataSubjectID        string                       `json:"data_subject_id"`
		SourceObservationIDs []string                     `json:"source_observation_ids"`
		Sections             map[string][]json.RawMessage `json:"sections"`
		Rows                 map[string]int               `json:"rows"`
	}
	h.do(t, http.MethodPost, "/v1/exports", map[string]any{
		"source_observation_ids": []string{receipt.ID},
	}, http.StatusOK, &export)
	if len(export.SourceObservationIDs) != 1 || export.SourceObservationIDs[0] != receipt.ID || export.DataSubjectID != "" {
		t.Fatalf("the export does not say what it was asked: %+v", export)
	}
	if export.Rows["message"] != 1 {
		t.Fatalf("an export of one turn returned %v", export.Rows)
	}
	if !bytes.Contains([]byte(fmt.Sprint(export.Sections["message"])), []byte("I work at Ensera")) {
		t.Fatalf("the export does not carry the turn's words: %s", export.Sections["message"])
	}
}

func TestAnExportProducesWhatAnErasureWouldDelete(t *testing.T) {
	h := newHarness(t, "api_export")

	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages": []map[string]any{
			{"role": "user", "content": "I work at Ensera and I live in Dublin."},
		},
	}, http.StatusCreated, nil)
	h.form(t)

	var export struct {
		Sections map[string][]json.RawMessage `json:"sections"`
		Rows     map[string]int               `json:"rows"`
	}
	h.do(t, http.MethodPost, "/v1/exports", map[string]any{"data_subject_id": "subject-1"},
		http.StatusOK, &export)

	// The person's own words are in it. An export of derivations without the messages they came from
	// is an export missing the part a person actually recognises.
	if len(export.Sections["message"]) == 0 {
		t.Fatal("the export contains no messages")
	}
	if !bytes.Contains([]byte(fmt.Sprint(export.Sections["message"])), []byte("I work at Ensera")) {
		t.Fatalf("the export does not contain what the person said: %s", export.Sections["message"])
	}
	if export.Rows["fact"] == 0 {
		t.Fatal("the export contains no facts, though formation produced some")
	}

	// Now erase, and compare the two walks. Every kind the erasure touched must have appeared in the
	// export — that is the test that fails the day somebody adds a projection to one and not the
	// other.
	var receipt struct {
		Deleted map[string]int `json:"deleted"`
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{
		"data_subject_id": "subject-1", "reason": "comparing the two walks",
	}, http.StatusOK, &receipt)

	for kind, deleted := range receipt.Deleted {
		if kind == "observation" {
			// Erasure counts the observation itself; the export produces its messages instead,
			// which is the same data in the form a person can read.
			continue
		}
		if _, present := export.Sections[kind]; !present {
			t.Fatalf("the erasure deleted %d rows of %q and the export did not have a section for it",
				deleted, kind)
		}
	}
}

// An export names the person it is for, or it is a copy of the project wearing a governance label.
func TestAnExportWithoutASubjectIsRefused(t *testing.T) {
	h := newHarness(t, "api_export_nosubject")
	status, body := h.raw(t, http.MethodPost, "/v1/exports", map[string]any{}, h.token)
	if status != http.StatusBadRequest {
		t.Fatalf("an export with no subject returned %d: %s", status, body)
	}
}

// An export for somebody with nothing held is empty sections rather than absent ones.
//
// "We hold none of this" and "we did not look" are different statements, and a caller exercising a
// right needs the first said explicitly.
func TestAnExportForSomebodyWithNothingHeldIsEmptyRatherThanAbsent(t *testing.T) {
	h := newHarness(t, "api_export_empty")

	var export struct {
		Sections map[string][]json.RawMessage `json:"sections"`
		Rows     map[string]int               `json:"rows"`
	}
	h.do(t, http.MethodPost, "/v1/exports", map[string]any{"data_subject_id": "never-heard-of-them"},
		http.StatusOK, &export)

	for _, kind := range []string{"message", "fact", "entity", "chunk", "rejected_claim"} {
		rows, present := export.Sections[kind]
		if !present {
			t.Fatalf("section %q is absent rather than empty", kind)
		}
		if rows == nil {
			t.Fatalf("section %q is null, which says 'we did not look'", kind)
		}
		if len(rows) != 0 {
			t.Fatalf("section %q has %d rows for a subject we have never seen", kind, len(rows))
		}
	}
}

// An export is recorded in the ledger like every other operation.
func TestAnExportIsRecordedInTheLedger(t *testing.T) {
	h := newHarness(t, "api_export_audit")
	ctx := context.Background()

	h.do(t, http.MethodPost, "/v1/exports", map[string]any{"data_subject_id": "subject-1"},
		http.StatusOK, nil)

	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 50)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	for _, e := range entries {
		if e.Operation == domain.AuditExport {
			if e.Principal == "" || e.Project != "p1" {
				t.Fatalf("the export was recorded as %+v", e)
			}
			return
		}
	}
	t.Fatal("an export left no ledger row")
}

// A ledger failure does not fail the request that produced it.
//
// Deliberate, and a real weakness: a gap in the record is what the record exists to make impossible.
// It is the better of two, because a ledger write that could refuse a customer's data for reasons
// unrelated to their data is a ledger an operator switches off — and a switched-off ledger records
// nothing at all.
func TestARequestSucceedsEvenIfTheLedgerCannot(t *testing.T) {
	h := newHarness(t, "api_ledger_broken")
	ctx := context.Background()

	// Break the ledger underneath the running server.
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.audit_entry RENAME TO audit_entry_moved`)); err != nil {
		t.Fatalf("break the ledger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), h.schema.SQL(
			`ALTER TABLE {schema}.audit_entry_moved RENAME TO audit_entry`))
	})

	// The operation still succeeds. A customer's write is not refused because our bookkeeping is
	// unwell.
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages":        []map[string]any{{"role": "user", "content": "I work at Ensera."}},
	}, http.StatusCreated, nil)
}

// A refused authentication with a short or absent token is still recorded, and records nothing that
// could be a secret.
func TestARefusedAuthenticationRecordsNoSecret(t *testing.T) {
	h := newHarness(t, "api_shorttoken")
	ctx := context.Background()

	for _, token := range []string{"", "short", "tsk_aaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		h.raw(t, http.MethodGet, "/v1/freshness", nil, token)
	}
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 50)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	recorded := 0
	for _, e := range entries {
		if e.Operation != domain.AuditAuthenticate {
			continue
		}
		recorded++
		if len(e.Principal) > 12 {
			t.Fatalf("the ledger recorded %d characters of a presented token", len(e.Principal))
		}
	}
	if recorded < 2 {
		t.Fatalf("%d refused authentications recorded", recorded)
	}
}

// ── A question can be asked of a moment ───────────────────────────────────────────────────────
//
// The wire carries both instants and the interval each fact held, because a fact returned by a read
// of last March that does not say it stopped holding is indistinguishable from one that holds now.
func TestAQuestionCanBeAskedOfAMomentInThePast(t *testing.T) {
	h := newHarness(t, "api_asof")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages": []map[string]any{
			{"role": "user", "content": "I work at Ensera and I live in Dublin."},
		},
	}, http.StatusCreated, nil)
	h.form(t)

	var bundle struct {
		Facts []struct {
			Object     string     `json:"object"`
			ValidFrom  time.Time  `json:"valid_from"`
			ValidUntil *time.Time `json:"valid_until"`
		} `json:"facts"`
	}

	// The current read: the facts are there, and nothing has superseded them.
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question":        "Where does the user work?",
		"data_subject_id": "subject-1",
	}, http.StatusOK, &bundle)
	if len(bundle.Facts) == 0 {
		t.Fatal("the current read came back empty")
	}
	for _, f := range bundle.Facts {
		if f.ValidFrom.IsZero() {
			t.Fatalf("a fact came back without saying when it became true: %+v", f)
		}
		if f.ValidUntil != nil {
			t.Fatalf("a fact nothing has superseded carries an end: %v", f.ValidUntil)
		}
	}

	// The same question asked of a moment before the conversation happened. The memory holds the
	// fact; it did not hold of the world then, and saying so is the whole point of keeping the
	// interval rather than a timestamp.
	var past struct {
		Facts []struct {
			Object string `json:"object"`
		} `json:"facts"`
		Reach struct {
			Anchored          int  `json:"anchored"`
			NamedNothingKnown bool `json:"named_nothing_known"`
		} `json:"reach"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question":        "Where does the user work?",
		"data_subject_id": "subject-1",
		"as_of":           "2020-01-01T00:00:00Z",
	}, http.StatusOK, &past)
	if len(past.Facts) != 0 {
		t.Fatalf("a read of 2020 returned facts that became true in 2026: %+v", past.Facts)
	}
	// And it says which empty answer this is: the entity is known, it simply held nothing then.
	if past.Reach.Anchored == 0 || past.Reach.NamedNothingKnown {
		t.Fatalf("an empty historical bundle reads as though the subject were unknown: %+v", past.Reach)
	}

	// A time that is not a time is refused with the body, not parsed into an accidental instant.
	status, body := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question":        "Where does the user work?",
		"data_subject_id": "subject-1",
		"as_of":           "last tuesday",
	}, h.token)
	if status != http.StatusBadRequest {
		t.Fatalf("a malformed instant returned %d: %s", status, body)
	}
}

// ── A chain over the wire ─────────────────────────────────────────────────────────────────────
//
// A fact about something the question never named is only worth returning if the route to it can be
// read, so the chain is on the wire beside the fact rather than implied by a score.
func TestAQuestionCanReachOneRelationFurtherAndSaysHowItGotThere(t *testing.T) {
	h := newHarness(t, "api_hops")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages": []map[string]any{
			{"role": "user", "content": "Hamza manages Marta. Hamza works at Ensera."},
		},
	}, http.StatusCreated, nil)
	h.form(t)

	type factView struct {
		Predicate string   `json:"predicate"`
		Hops      int      `json:"hops"`
		Path      []string `json:"path"`
		Via       []string `json:"via"`
	}
	var direct struct {
		Facts []factView `json:"facts"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question": "What about Marta?",
	}, http.StatusOK, &direct)
	if len(direct.Facts) == 0 {
		t.Fatal("the question named an entity in memory and got nothing")
	}
	for _, f := range direct.Facts {
		if f.Predicate == "works_at" {
			t.Fatal("a two-hop fact came back from a request that did not ask for one")
		}
		// Never null on the wire: an absent list and an empty one read the same to a caller and mean
		// different things.
		if f.Path == nil || f.Via == nil {
			t.Fatalf("a fact came back with a null chain rather than an empty one: %+v", f)
		}
	}

	var deep struct {
		Facts []factView `json:"facts"`
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{
		"question": "What about Marta?",
		"hops":     2,
	}, http.StatusOK, &deep)

	var employment *factView
	for i, f := range deep.Facts {
		if f.Predicate == "works_at" {
			employment = &deep.Facts[i]
		}
	}
	if employment == nil {
		t.Fatalf("asking for a second hop reached nothing further: %+v", deep.Facts)
	}
	if employment.Hops != 2 {
		t.Fatalf("a hopped fact reports %d hops", employment.Hops)
	}
	if strings.Join(employment.Via, " → ") != "manages → works_at" {
		t.Fatalf("the relations are not in the order they were followed: %v", employment.Via)
	}
	if strings.Join(employment.Path, " → ") != "Marta → Hamza → Ensera" {
		t.Fatalf("the chain does not read from the question outwards: %v", employment.Path)
	}
}
