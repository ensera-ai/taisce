// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

// managed is a memory harness with the management surface served beside it, the way the manage
// role serves it: on its own listener, with the administrative connection for provisioning.
type managed struct {
	*harness
	manage       *httptest.Server
	manageServer *api.ManagementServer
	operator     string
}

func newManaged(t *testing.T, tenant string) *managed {
	t.Helper()
	h := newHarness(t, tenant)
	ctx := context.Background()
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	operator, _, err := credentials.IssueOperator(ctx, "ops-"+tenant)
	if err != nil {
		t.Fatal(err)
	}
	server := api.NewManagementServer(credentials, api.ManagementStores{
		Projects: pg.NewProjectStore(h.pool, h.schema),
		Provision: func(ctx context.Context, scope string) error {
			return migrate.ProvisionScope(ctx, h.pool, h.schema.String(), scope)
		},
		Observations: pg.NewObservationStore(h.pool),
		Audit:        pg.NewAuditStore(h.pool, h.schema),
		Refusals:     pg.NewRefusalStore(h.pool, h.schema),
		Eraser:       pg.NewEraser(h.pool),
		Health: func(ctx context.Context) (pg.OperationalHealth, error) {
			return pg.ReadOperationalHealth(ctx, h.pool, h.schema)
		},
	}, h.schema, nil)
	m := &managed{harness: h, manage: httptest.NewServer(server.Handler()), manageServer: server, operator: operator}
	t.Cleanup(m.manage.Close)
	return m
}

// manageDo sends one management request as the operator, or as whoever token names.
func (m *managed) manageDo(t *testing.T, path string, body map[string]any, token string, want int, into any) []byte {
	t.Helper()
	status, raw := rawAgainst(t, m.manage.URL, http.MethodPost, api.ManagementPrefix+path, body, token)
	if status != want {
		t.Fatalf("%s returned %d, want %d: %s", path, status, want, raw)
	}
	if into != nil {
		decodeJSON(t, raw, into)
	}
	return raw
}

// The management door opens for an operator credential and nothing else, and the memory door does
// not open for an operator credential: two kinds, each refused at the other's door with the answer
// a stranger gets, and a revoked operator is a stranger.
func TestTheManagementDoorOpensForAnOperatorCredentialAndNothingElse(t *testing.T) {
	m := newManaged(t, "api_manage_door")
	ctx := context.Background()
	for _, o := range api.ManagementOperations() {
		for _, stranger := range []string{"", "tsk_notatoken", m.token} {
			if status, _ := rawAgainst(t, m.manage.URL, o.Method, api.ManagementPrefix+o.Path, map[string]any{}, stranger); status != http.StatusUnauthorized {
				t.Fatalf("%s with token %q returned %d; a project credential or a stranger is refused at the management door", o.Name, stranger, status)
			}
		}
	}
	m.manageDo(t, "/projects/list", nil, m.operator, http.StatusOK, nil)
	if status, _ := m.raw(t, http.MethodGet, "/v1/freshness", nil, m.operator); status != http.StatusUnauthorized {
		t.Fatalf("an operator credential must not open a memory route, got %d", status)
	}
	credentials := credential.NewStore(m.pool, string(migrate.ControlSchema))
	if _, err := credentials.Resolve(ctx, m.operator); err == nil {
		t.Fatal("the memory resolver must refuse an operator credential")
	}
	if _, err := credentials.ResolveOperator(ctx, m.token); err == nil {
		t.Fatal("the operator resolver must refuse a project credential")
	}
	listed, err := credentials.List(ctx, "", 10)
	if err != nil || len(listed) == 0 || listed[0].Kind != credential.KindOperator {
		t.Fatalf("operator credentials list under no project, got %+v %v", listed, err)
	}
	if err := credentials.Revoke(ctx, listed[0].CredentialID); err != nil {
		t.Fatal(err)
	}
	m.manageDo(t, "/projects/list", nil, m.operator, http.StatusUnauthorized, nil)
	if _, _, err := credentials.IssueOperator(ctx, " "); err == nil {
		t.Fatal("an operator credential needs a name")
	}
}

// An operator creates a project, mints a credential for it that works on the memory routes,
// suspends the project so the credential stops working, resumes it, lists and revokes the
// credential; a project that does not exist mints nothing and a name that is not an identifier
// is refused before anything is touched.
func TestAnOperatorRunsTheProjectAndCredentialLifecycleOverTheAPI(t *testing.T) {
	m := newManaged(t, "api_manage_lifecycle")
	// The registry outlives a test schema, so the project is named afresh each run and its
	// credentials are only this run's.
	project := "lc_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	m.manageDo(t, "/projects/create", map[string]any{"name": "not a name!"}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/projects/create", map[string]any{"name": ""}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/projects/create", map[string]any{"name": project}, m.operator, http.StatusCreated, nil)
	// Retention: days, or null for indefinite, and nothing else.
	var retention struct {
		Retention string `json:"retention"`
	}
	m.manageDo(t, "/projects/retention", map[string]any{"name": project, "retention_days": 30}, m.operator, http.StatusOK, &retention)
	if retention.Retention != "30 days" {
		t.Fatalf("the surface said %q", retention.Retention)
	}
	m.manageDo(t, "/projects/retention", map[string]any{"name": project, "retention_days": nil}, m.operator, http.StatusOK, &retention)
	if retention.Retention != "indefinite" {
		t.Fatalf("the surface said %q", retention.Retention)
	}
	m.manageDo(t, "/projects/retention", map[string]any{"name": project, "retention_days": 0}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/projects/retention", map[string]any{"name": "no_such_project", "retention_days": 30}, m.operator, http.StatusNotFound, nil)
	var projects struct {
		Projects []struct {
			Project   string `json:"project"`
			Suspended bool   `json:"suspended"`
		} `json:"projects"`
	}
	m.manageDo(t, "/projects/list", nil, m.operator, http.StatusOK, &projects)
	names := []string{}
	for _, p := range projects.Projects {
		names = append(names, p.Project)
	}
	if !slices.Contains(names, project) || !slices.Contains(names, "p1") {
		t.Fatalf("expected %s and p1, got %v", project, names)
	}
	var issued struct {
		ID, Token, Project, Access string
	}
	m.manageDo(t, "/credentials/issue", map[string]any{"name": "app", "project": "nope"}, m.operator, http.StatusNotFound, nil)
	m.manageDo(t, "/credentials/issue", map[string]any{"name": "app", "project": project, "access": "admin"}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/credentials/issue", map[string]any{"name": "app", "project": project, "access": "read_only"}, m.operator, http.StatusCreated, &issued)
	if issued.Token == "" || issued.Project != project || issued.Access != "read_only" {
		t.Fatalf("expected a read-only token for p2, got %+v", issued)
	}
	if status, _ := m.raw(t, http.MethodGet, "/v1/freshness", nil, issued.Token); status != http.StatusOK {
		t.Fatalf("the minted credential must open the project's memory routes, got %d", status)
	}
	m.manageDo(t, "/projects/suspend", map[string]any{"name": project}, m.operator, http.StatusOK, nil)
	m.manageDo(t, "/projects/suspend", map[string]any{"name": project}, m.operator, http.StatusNotFound, nil)
	if status, _ := m.raw(t, http.MethodGet, "/v1/freshness", nil, issued.Token); status == http.StatusOK {
		t.Fatal("a suspended project's credential must not open its memory routes")
	}
	m.manageDo(t, "/credentials/issue", map[string]any{"name": "app2", "project": project}, m.operator, http.StatusNotFound, nil)
	m.manageDo(t, "/projects/resume", map[string]any{"name": project}, m.operator, http.StatusOK, nil)
	if status, _ := m.raw(t, http.MethodGet, "/v1/freshness", nil, issued.Token); status != http.StatusOK {
		t.Fatalf("a resumed project's credential must open its memory routes again, got %d", status)
	}
	var listed struct {
		Credentials []credential.Listed `json:"credentials"`
	}
	m.manageDo(t, "/credentials/list", map[string]any{"project": project}, m.operator, http.StatusOK, &listed)
	if len(listed.Credentials) != 1 || listed.Credentials[0].CredentialID != issued.ID || listed.Credentials[0].RevokedAt != nil {
		t.Fatalf("expected the one live credential of manage_lc, got %+v", listed.Credentials)
	}
	m.manageDo(t, "/credentials/list", map[string]any{"project": project, "limit": 501}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/credentials/revoke", map[string]any{"id": issued.ID}, m.operator, http.StatusOK, nil)
	m.manageDo(t, "/credentials/revoke", map[string]any{"id": issued.ID}, m.operator, http.StatusNotFound, nil)
	m.manageDo(t, "/credentials/list", map[string]any{"project": project}, m.operator, http.StatusOK, &listed)
	if len(listed.Credentials) != 1 || listed.Credentials[0].RevokedAt == nil {
		t.Fatalf("a revoked credential is listed as revoked, got %+v", listed.Credentials)
	}
	if status, _ := m.raw(t, http.MethodGet, "/v1/freshness", nil, issued.Token); status != http.StatusUnauthorized {
		t.Fatalf("a revoked credential is a stranger, got %d", status)
	}
}

// An operator reads what the instance refused, erased and formed without a database connection
// and without reading a word of anybody's: counts, receipts, watermarks and parked turns; unparks
// one; seals and verifies the ledger. Every operation leaves a ledger row with the operator as its
// principal.
func TestAnOperatorReadsWhatTheInstanceRefusedErasedAndFormedAndEveryReadIsOnTheLedger(t *testing.T) {
	m := newManaged(t, "api_manage_reads")
	ctx := context.Background()
	m.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, http.StatusCreated, nil)
	m.form(t)
	var summary struct {
		Project string `json:"project"`
		Total   int64  `json:"total"`
		Counts  []struct {
			Reason    string `json:"reason"`
			Predicate string `json:"predicate"`
			Count     int64  `json:"count"`
		} `json:"counts"`
	}
	// One refusal written the way extraction writes it, so the counts have something to count.
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`WITH refused AS (
		INSERT INTO {schema}.rejected_claim (rejected_claim_id, scope, source_observation_id, source_ordinal, predicate, statement, quote, reason, extractor_version, data_subject_id)
		SELECT gen_random_uuid(), 'p1', observation_id, 0, 'commutes_by', 'The user cycles.', 'I cycle', 'unmapped_relation', 'test', 'subject-1' FROM {schema}.observation WHERE scope='p1' LIMIT 1
		RETURNING rejected_claim_id, source_observation_id)
		INSERT INTO {schema}.projection_dependency (source_observation_id, scope, projection_kind, projection_id, data_subject_id)
		SELECT source_observation_id, 'p1', 'rejected_claim', rejected_claim_id::text, 'subject-1' FROM refused`)); err != nil {
		t.Fatal(err)
	}
	m.manageDo(t, "/refusals/summary", map[string]any{"project": "p1"}, m.operator, http.StatusOK, &summary)
	if len(summary.Counts) == 0 || summary.Counts[0].Reason != "unmapped_relation" || summary.Counts[0].Predicate != "commutes_by" {
		t.Fatalf("the summary must count the refusal by reason and predicate, got %+v", summary)
	}
	var refused int64
	if err := m.pool.QueryRow(ctx, m.schema.SQL(`SELECT count(*) FROM {schema}.rejected_claim WHERE scope='p1'`)).Scan(&refused); err != nil {
		t.Fatal(err)
	}
	if summary.Total != refused || summary.Project != "p1" {
		t.Fatalf("the summary must count what the table holds (%d), got %+v", refused, summary)
	}
	for _, c := range summary.Counts {
		if c.Count <= 0 || c.Reason == "" {
			t.Fatalf("a count names a reason and is positive, got %+v", c)
		}
	}
	raw := m.manageDo(t, "/refusals/summary", map[string]any{"project": "p1"}, m.operator, http.StatusOK, nil)
	if strings.Contains(string(raw), "Ensera") || strings.Contains(string(raw), "Dublin") {
		t.Fatal("a refusal summary carries no words")
	}
	m.manageDo(t, "/refusals/summary", map[string]any{"project": ""}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/erasures/list", map[string]any{"project": "p1", "limit": 9999}, m.operator, http.StatusBadRequest, nil)
	// An erasure through the memory routes is a receipt here.
	m.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "subject-1", "reason": "the operator test"}, http.StatusOK, nil)
	var erasures struct {
		Erasures []struct {
			ID        string         `json:"id"`
			Reason    string         `json:"reason"`
			Completed *string        `json:"completed_at"`
			Residual  map[string]any `json:"residual"`
		} `json:"erasures"`
	}
	m.manageDo(t, "/erasures/list", map[string]any{"project": "p1"}, m.operator, http.StatusOK, &erasures)
	if len(erasures.Erasures) != 1 || erasures.Erasures[0].Completed == nil || erasures.Erasures[0].Residual == nil || erasures.Erasures[0].Reason != "the operator test" {
		t.Fatalf("expected one completed receipt with its residual, got %+v", erasures)
	}
	// Formation: the watermark per project, a parked turn listed and unparked.
	m.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-2", "messages": []map[string]any{{"role": "user", "content": "I cycle to work."}}}, http.StatusCreated, nil)
	var parkedID string
	if err := m.pool.QueryRow(ctx, m.schema.SQL(`UPDATE {schema}.observation SET parked_at=now(), formation_attempts=1 WHERE scope='p1' AND formed_at IS NULL RETURNING observation_id::text`)).Scan(&parkedID); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Scopes []struct {
			Project string `json:"project"`
			Stored  *int64 `json:"stored"`
			Formed  *int64 `json:"formed"`
			Parked  int    `json:"parked"`
		} `json:"scopes"`
	}
	m.manageDo(t, "/formation/status", nil, m.operator, http.StatusOK, &status)
	if len(status.Scopes) != 1 || status.Scopes[0].Project != "p1" || status.Scopes[0].Stored == nil || status.Scopes[0].Parked != 1 {
		t.Fatalf("expected p1 with a stored offset and one parked turn, got %+v", status.Scopes)
	}
	var parked struct {
		Items []map[string]any `json:"items"`
	}
	m.manageDo(t, "/formation/parked", map[string]any{"project": "p1"}, m.operator, http.StatusOK, &parked)
	if len(parked.Items) != 1 {
		t.Fatalf("expected the parked turn listed, got %+v", parked)
	}
	m.manageDo(t, "/formation/parked", map[string]any{"project": "p1", "limit": 999}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/formation/parked", map[string]any{"project": "p1", "after": -1, "limit": 10}, m.operator, http.StatusOK, &parked)
	m.manageDo(t, "/formation/unpark", map[string]any{"project": "not valid!", "observation_id": parkedID}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/formation/unpark", map[string]any{"project": "p1"}, m.operator, http.StatusBadRequest, nil)
	m.manageDo(t, "/formation/unpark", map[string]any{"project": "p1", "observation_id": "7b0e1c52-2f51-4d0a-9d8e-5f0a3c1b2e44"}, m.operator, http.StatusNotFound, nil)
	m.manageDo(t, "/formation/unpark", map[string]any{"project": "p1", "observation_id": parkedID}, m.operator, http.StatusOK, nil)
	m.manageDo(t, "/formation/unpark", map[string]any{"project": "p1", "observation_id": parkedID}, m.operator, http.StatusNotFound, nil)
	m.manageDo(t, "/formation/parked", map[string]any{"project": "p1"}, m.operator, http.StatusOK, &parked)
	if len(parked.Items) != 0 {
		t.Fatalf("an unparked turn is no longer parked, got %+v", parked)
	}
	// The ledger: sealed and verified over the API.
	var seal map[string]any
	m.manageDo(t, "/audit/seal", nil, m.operator, http.StatusOK, &seal)
	var verification map[string]any
	m.manageDo(t, "/audit/verify", nil, m.operator, http.StatusOK, &verification)
	if len(seal) == 0 || len(verification) == 0 {
		t.Fatalf("a seal and a verification are documents, got %v %v", seal, verification)
	}
	// Every management operation above is on the ledger with the operator as its principal.
	entries, err := pg.NewAuditStore(m.pool, m.schema).Recent(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	recorded := map[string]bool{}
	for _, e := range entries {
		if e.PrincipalKind == domain.PrincipalOperator {
			recorded[e.Operation] = true
		}
	}
	for _, op := range []string{domain.AuditRefusalSummary, domain.AuditErasureList, domain.AuditFormationStatus, domain.AuditFormationParked, domain.AuditFormationUnpark, domain.AuditAuditSeal, domain.AuditAuditVerify} {
		if !recorded[op] {
			t.Fatalf("%s left no ledger row with an operator principal; recorded %v", op, recorded)
		}
	}
}

// rawAgainst sends one request to any server, the harness's or the management one.
func rawAgainst(t *testing.T, base, method, path string, body map[string]any, token string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func decodeJSON(t *testing.T, raw []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

// A database that cannot answer is an internal error from every management operation, never an
// empty answer that reads as "nothing to report": the tables are moved away one at a time under
// the running server, and each operation that reads them says so.
func TestTheManagementSurfaceAnswersInternalWhenTheDatabaseCannot(t *testing.T) {
	m := newManaged(t, "api_manage_broken")
	ctx := context.Background()
	moved := func(table string, calls func()) {
		t.Helper()
		if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.`+table+` RENAME TO `+table+`_moved`)); err != nil {
			t.Fatal(err)
		}
		calls()
		if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.`+table+`_moved RENAME TO `+table)); err != nil {
			t.Fatal(err)
		}
	}
	moved("project", func() {
		m.manageDo(t, "/projects/list", nil, m.operator, http.StatusInternalServerError, nil)
		m.manageDo(t, "/projects/suspend", map[string]any{"name": "p1"}, m.operator, http.StatusInternalServerError, nil)
		m.manageDo(t, "/credentials/issue", map[string]any{"name": "app", "project": "p1"}, m.operator, http.StatusInternalServerError, nil)
		m.manageDo(t, "/formation/status", nil, m.operator, http.StatusInternalServerError, nil)
	})
	moved("rejected_claim", func() {
		m.manageDo(t, "/refusals/summary", map[string]any{"project": "p1"}, m.operator, http.StatusInternalServerError, nil)
	})
	moved("erasure_request", func() {
		m.manageDo(t, "/erasures/list", map[string]any{"project": "p1"}, m.operator, http.StatusInternalServerError, nil)
	})
	moved("observation", func() {
		m.manageDo(t, "/formation/parked", map[string]any{"project": "p1"}, m.operator, http.StatusInternalServerError, nil)
		m.manageDo(t, "/formation/unpark", map[string]any{"project": "p1", "observation_id": "0d2ab8b4-4c1b-4b4e-9d1c-2a1d5b3e6f70"}, m.operator, http.StatusInternalServerError, nil)
		m.manageDo(t, "/formation/status", nil, m.operator, http.StatusInternalServerError, nil)
	})
	moved("audit_entry", func() {
		// The ledger gone: the operation still answers (a ledger failure never refuses the person),
		// and the seal and the verification, which are the ledger, cannot.
		m.manageDo(t, "/projects/list", nil, m.operator, http.StatusOK, nil)
		m.manageDo(t, "/audit/seal", nil, m.operator, http.StatusInternalServerError, nil)
		m.manageDo(t, "/audit/verify", nil, m.operator, http.StatusInternalServerError, nil)
		m.manageDo(t, "/projects/list", nil, "tsk_stranger", http.StatusUnauthorized, nil)
	})
	// A project whose storage cannot be created is an internal error, not a project.
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.chunk RENAME TO chunk_moved`)); err != nil {
		t.Fatal(err)
	}
	m.manageDo(t, "/projects/create", map[string]any{"name": "p_broken"}, m.operator, http.StatusInternalServerError, nil)
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.chunk_moved RENAME TO chunk`)); err != nil {
		t.Fatal(err)
	}
	// Bodies that are not the operation's are refused before anything is read.
	for _, path := range []string{"/projects/create", "/projects/suspend", "/credentials/issue", "/credentials/list", "/credentials/revoke", "/refusals/summary", "/erasures/list", "/formation/parked", "/formation/unpark", "/projects/retention"} {
		if status, _ := rawAgainst(t, m.manage.URL, http.MethodPost, api.ManagementPrefix+path, map[string]any{"nonsense": true}, m.operator); status != http.StatusBadRequest {
			t.Fatalf("%s must refuse an unknown field, got %d", path, status)
		}
	}
}
