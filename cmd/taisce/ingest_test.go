// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/ingest"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ingestHarness is a live API over the test database with one project and one credential, which
// is what the command talks to in a deployment: nothing here reaches around the contract.
type ingestHarness struct {
	pool   *pgxpool.Pool
	schema pg.Schema
	server *httptest.Server
	token  string
}

func newIngestHarness(t *testing.T) *ingestHarness {
	t.Helper()
	ctx := context.Background()
	env := complete(t)
	const name = "cmd_ingest"
	pool, err := pgxpool.New(ctx, env[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.EstablishPlanes(ctx, pool, "taisce-test-control", "taisce-test-data"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE") })
	if err := migrate.ProvisionMemorySchema(ctx, pool, name); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, name, "p1"); err != nil {
		t.Fatal(err)
	}
	schema, err := pg.NewSchema(name)
	if err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(pool, string(migrate.ControlSchema))
	token, _, err := credentials.IssueWithAccess(ctx, "ingest-test", "p1", credential.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM `+string(migrate.ControlSchema)+`.credential WHERE name='ingest-test'`)
	})
	server := httptest.NewServer(api.NewServer(credentials, api.Stores{
		Audit: pg.NewAuditStore(pool, schema), Exporter: pg.NewExporter(pool, schema),
		Projects: pg.NewProjectStore(pool, schema), Observations: pg.NewObservationStore(pool),
		Eraser: pg.NewEraser(pool), Recaller: recall.NewWithBudget(pg.NewRecallStore(pool, schema), recall.Budget{Characters: 100000, MaxRows: 50}),
		Citations: pg.NewCitationStore(pool, schema), Records: pg.NewRecordStore(pool, schema), Feedback: pg.NewFeedbackStore(pool, schema, pg.NewRecordStore(pool, schema)),
		Artifacts: pg.NewArtifactStore(pool, schema), Subjects: pg.NewSubjectStore(pool, schema),
	}, schema, nil).Handler())
	t.Cleanup(server.Close)
	return &ingestHarness{pool: pool, schema: schema, server: server, token: token}
}

func (h *ingestHarness) observations(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE scope='p1'`)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (h *ingestHarness) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := ingestCommand(context.Background(), append([]string{"--api", h.server.URL}, args...), &out)
	return out.String(), err
}

func readManifest(t *testing.T, dir, document string) manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, document+".manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A document becomes one observation per segment under the tool role with the time it says it
// happened; the stored messages, read back in order, are the document byte for byte; a refused
// file stores nothing and the run says so; running again stores nothing new and answers with
// the same observations, which is what resumable and duplicate-safe mean here.
func TestIngestStoresADocumentAsSegmentsExactlyOnceAndRefusesWhole(t *testing.T) {
	h := newIngestHarness(t)
	t.Setenv(envToken, h.token)
	dir := t.TempDir()
	manifests := filepath.Join(dir, "m")
	var long strings.Builder
	for i := 0; i < 30; i++ {
		long.WriteString("Marta works at Ensera. She lives in Dublin and prefers written documentation to calls.\n\n")
		long.WriteString("قالت مرتا إنها تعمل في إنسيرا وتعيش في دبلن.\n\n")
	}
	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	longPath := write("long.txt", []byte(long.String()))
	notePath := write("note.json", []byte(`{"title": "عقد الإيجار", "text": "تم توقيع عقد الإيجار في آذار.", "occurred_at": "2024-03-04T05:06:07Z"}`))
	brokenPath := write("broken.json", []byte(`{"text": `))
	hugePath := write("huge.txt", []byte(strings.Repeat("x", 9000)))
	zipPath := write("archive.zip", []byte("PK\x03\x04 not a document"))

	before := h.observations(t)
	out, err := h.run(t, "--segment-bytes", "512", "--max-file-bytes", "8192", "--manifest-dir", manifests,
		longPath, notePath, brokenPath, hugePath, zipPath)
	if err == nil || !strings.Contains(err.Error(), "broken.json") || !strings.Contains(err.Error(), "huge.txt") || !strings.Contains(err.Error(), "archive.zip") {
		t.Fatalf("three refusals must be reported by name, got %v\n%s", err, out)
	}
	doc, err := ingest.Read(longPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	segments, err := ingest.Split(doc.Text, 512)
	if err != nil {
		t.Fatal(err)
	}
	m := readManifest(t, manifests, "long.txt")
	if len(m.Segments) != len(segments) || m.Refused != "" || m.Role != "tool" || m.Bytes != len(doc.Text) {
		t.Fatalf("manifest does not describe the document: %+v", m)
	}
	note := readManifest(t, manifests, "note.json")
	if len(note.Segments) != 1 || note.OccurredAt == nil || note.OccurredAt.Year() != 2024 {
		t.Fatalf("the note must be one segment with its own time: %+v", note)
	}
	if after := h.observations(t); after-before != len(segments)+1 {
		t.Fatalf("expected %d observations stored, got %d", len(segments)+1, after-before)
	}
	for _, refused := range []string{"broken.json", "huge.txt", "archive.zip"} {
		if r := readManifest(t, manifests, refused); r.Refused == "" || len(r.Segments) != 0 {
			t.Fatalf("a refused document must have a reason and no receipts: %+v", r)
		}
	}
	// The stored messages are the document, in order, under the tool role, project-wide.
	var rebuilt strings.Builder
	for _, s := range m.Segments {
		var content, role string
		var subject *string
		var occurred time.Time
		if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT m.content, o.source_role, o.data_subject_id, o.occurred_at
 FROM {schema}.turn_message m JOIN {schema}.observation o ON o.observation_id=m.observation_id
 WHERE m.observation_id=$1::uuid AND m.ordinal=0`), s.ObservationID).Scan(&content, &role, &subject, &occurred); err != nil {
			t.Fatalf("segment %d: %v", s.Ordinal, err)
		}
		if role != "tool" || subject != nil || content != doc.Text[s.ByteStart:s.ByteEnd] {
			t.Fatalf("segment %d stored wrongly: role=%s subject=%v", s.Ordinal, role, subject)
		}
		rebuilt.WriteString(content)
	}
	if rebuilt.String() != doc.Text {
		t.Fatal("the stored segments read back in order are not the document")
	}
	var noteTime time.Time
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT occurred_at FROM {schema}.observation WHERE observation_id=$1::uuid`), note.Segments[0].ObservationID).Scan(&noteTime); err != nil {
		t.Fatal(err)
	}
	if !noteTime.Equal(time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)) {
		t.Fatalf("the note's own time must be stored, got %v", noteTime)
	}

	// Again: nothing new is stored and the receipts are the same observations.
	if _, err := h.run(t, "--segment-bytes", "512", "--manifest-dir", manifests, longPath, notePath); err != nil {
		t.Fatalf("a rerun must succeed as replays: %v", err)
	}
	if again := h.observations(t); again-before != len(segments)+1 {
		t.Fatalf("a rerun stored %d new observations", again-before-len(segments)-1)
	}
	if replay := readManifest(t, manifests, "long.txt"); replay.Segments[len(replay.Segments)-1].ObservationID != m.Segments[len(m.Segments)-1].ObservationID {
		t.Fatal("a replay must answer with the observation already stored")
	}
	// The same document as the person's own words is a different observation, as the API stores it.
	if _, err := h.run(t, "--segment-bytes", "512", "--manifest-dir", manifests, "--role", "user", "--data-subject", "subject-1", notePath); err != nil {
		t.Fatal(err)
	}
	if asUser := h.observations(t); asUser-before != len(segments)+2 {
		t.Fatal("the note as the subject's own words must be one more observation")
	}
}

// A full backlog stops a document with the receipts it has, and the next run carries on from the
// API's own idempotency: the segment already received is a replay, the rest are stored, and no
// segment is stored twice. Waiting for formation without a worker reports that it did not happen.
func TestIngestWaitsForCapacityThenResumesFromTheReceiptsItHolds(t *testing.T) {
	h := newIngestHarness(t)
	t.Setenv(envToken, h.token)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "three.txt")
	var three strings.Builder
	for i := 0; i < 3; i++ {
		three.WriteString(strings.Repeat("A sentence about the office move in March. ", 8))
		three.WriteString("\n\n")
	}
	if err := os.WriteFile(path, []byte(three.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=1, max_pending_per_project=1`)); err != nil {
		t.Fatal(err)
	}
	before := h.observations(t)
	out, err := h.run(t, "--segment-bytes", "400", "--capacity-wait", "2s", "--manifest-dir", dir, path)
	if err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("a full backlog must stop the document by name, got %v\n%s", err, out)
	}
	partial := readManifest(t, dir, "three.txt")
	if len(partial.Segments) != 1 || partial.Refused == "" || h.observations(t)-before != 1 {
		t.Fatalf("expected one receipt and one stored observation, got %+v", partial)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.ingestion_budget SET max_pending=4096, max_pending_per_project=512`)); err != nil {
		t.Fatal(err)
	}
	out, err = h.run(t, "--segment-bytes", "400", "--wait", "1500ms", "--manifest-dir", dir, path)
	if err == nil || !strings.Contains(err.Error(), "formation did not reach") {
		t.Fatalf("without a worker the wait must report that formation did not happen, got %v\n%s", err, out)
	}
	resumed := readManifest(t, dir, "three.txt")
	if len(resumed.Segments) != 3 || resumed.Refused != "" || resumed.Segments[0].ObservationID != partial.Segments[0].ObservationID {
		t.Fatalf("expected the first receipt kept and two more stored: %+v", resumed)
	}
	if h.observations(t)-before != 3 {
		t.Fatalf("expected three observations in all, got %d", h.observations(t)-before)
	}
	if !strings.Contains(out, `"parked":0`) || !strings.Contains(out, `"segments":3`) {
		t.Fatalf("the summary must report parked turns and segments: %s", out)
	}
}

// The command refuses to start without an API address, without a credential in the environment,
// with a role that is not a speaker a document can have, or with a time it cannot parse.
func TestIngestRefusesAMisconfiguredRunBeforeReadingAnything(t *testing.T) {
	var out bytes.Buffer
	t.Setenv(envAPI, "")
	t.Setenv(envToken, "")
	if err := ingestCommand(context.Background(), []string{"a.txt"}, &out); err == nil || !strings.Contains(err.Error(), "API address") {
		t.Fatalf("expected the address refusal, got %v", err)
	}
	if err := ingestCommand(context.Background(), []string{"--api", "http://127.0.0.1:1", "a.txt"}, &out); err == nil || !strings.Contains(err.Error(), envToken) {
		t.Fatalf("expected the token refusal, got %v", err)
	}
	t.Setenv(envToken, "secret")
	if err := ingestCommand(context.Background(), []string{"--api", "http://127.0.0.1:1", "--role", "assistant", "a.txt"}, &out); err == nil || !strings.Contains(err.Error(), "--role") {
		t.Fatalf("expected the role refusal, got %v", err)
	}
	if err := ingestCommand(context.Background(), []string{"--api", "http://127.0.0.1:1", "--occurred-at", "yesterday", "a.txt"}, &out); err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Fatalf("expected the time refusal, got %v", err)
	}
	if err := ingestCommand(context.Background(), []string{"--api", "http://127.0.0.1:1"}, &out); err == nil || !strings.Contains(err.Error(), "at least one document") {
		t.Fatalf("expected the no-document refusal, got %v", err)
	}
}

// The API's refusals and the transport's failures reach the operator by name and store nothing:
// a credential the API does not know, an address nothing answers at, a manifest directory that
// cannot be written, a document whose whitespace cannot be segmented. A time given on the command
// line is stored for a document that says none, and a formed watermark that reaches the highest
// offset sent ends the wait with the count of parked turns.
func TestIngestReportsRefusalsByNameAndEndsTheWaitWhenFormed(t *testing.T) {
	h := newIngestHarness(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(path, []byte("Marta works at Ensera."), 0o600); err != nil {
		t.Fatal(err)
	}
	blank := filepath.Join(dir, "blank.txt")
	if err := os.WriteFile(blank, []byte("a"+strings.Repeat(" ", 600)+"b"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := h.observations(t)
	t.Setenv(envToken, "not-a-credential")
	if _, err := h.run(t, "--manifest-dir", dir, path); err == nil || !strings.Contains(err.Error(), "refused (401") {
		t.Fatalf("an unknown credential must be reported as the API's refusal, got %v", err)
	}
	t.Setenv(envToken, h.token)
	var out bytes.Buffer
	if err := ingestCommand(ctx, []string{"--api", "http://127.0.0.1:1", "--manifest-dir", dir, path}, &out); err == nil || !strings.Contains(err.Error(), "plain.txt") {
		t.Fatalf("an unreachable API must be reported against the document, got %v", err)
	}
	if err := ingestCommand(ctx, []string{"--api", h.server.URL, "--no-such-flag", path}, &out); err == nil {
		t.Fatal("an unknown flag must be refused")
	}
	if err := ingestCommand(ctx, []string{"--api", h.server.URL, "--manifest-dir", path, path}, &out); err == nil || !strings.Contains(err.Error(), "manifest directory") {
		t.Fatalf("a manifest directory that is a file must be refused, got %v", err)
	}
	// Unwritable for every user, root included: a directory stands where the manifest file must
	// be written, so the write fails after the API has already accepted the turn. A permission
	// bit would not do, because a suite that runs as root (the qualification node's test
	// container does) is not refused by one.
	sealed := filepath.Join(dir, "sealed")
	if err := os.MkdirAll(filepath.Join(sealed, filepath.Base(path)+".manifest.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(t, "--manifest-dir", sealed, path); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("an unwritable manifest must fail the run, got %v", err)
	}
	if h.observations(t)-before != 1 {
		t.Fatalf("only the sealed run reached the API; expected one observation, got %d", h.observations(t)-before)
	}
	if _, err := h.run(t, "--segment-bytes", "256", "--manifest-dir", dir, blank); err == nil || !strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("an unsegmentable document must be refused by reason, got %v", err)
	}
	// The same words sent again with a different time are the same document under the same key:
	// time is part of neither the key nor the retry fingerprint, so the rerun replays the
	// observation already held, whose first time stands, and stores nothing new.
	held := h.observations(t)
	if _, err := h.run(t, "--manifest-dir", dir, "--occurred-at", "2023-01-02T03:04:05Z", path); err != nil {
		t.Fatalf("a rerun with a different time must replay the document already held, got %v", err)
	}
	if h.observations(t) != held {
		t.Fatalf("a rerun with a different time stored %d new observations", h.observations(t)-held)
	}
	// A time from the command line is stored for a document that says none, and the wait ends when
	// the watermark says the offset formed.
	dated := filepath.Join(dir, "dated.txt")
	if err := os.WriteFile(dated, []byte("Marta lives in Dublin."), 0o600); err != nil {
		t.Fatal(err)
	}
	summaryOut, err := h.run(t, "--manifest-dir", dir, "--occurred-at", "2023-01-02T03:04:05Z", "--wait", "1s", dated)
	if err == nil || !strings.Contains(err.Error(), "formation did not reach") {
		t.Fatalf("expected the unformed wait to be reported, got %v", err)
	}
	m := readManifest(t, dir, "dated.txt")
	var stored time.Time
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT occurred_at FROM {schema}.observation WHERE observation_id=$1::uuid`), m.Segments[0].ObservationID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if m.OccurredAt == nil || !stored.Equal(time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("the command-line time must be stored, got %v (manifest %v)", stored, m.OccurredAt)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.watermark SET formed_offset=$1 WHERE scope='p1'`), m.Segments[0].LogOffset); err != nil {
		t.Fatal(err)
	}
	summaryOut, err = h.run(t, "--manifest-dir", dir, "--occurred-at", "2023-01-02T03:04:05Z", "--wait", "5s", dated)
	if err != nil || !strings.Contains(summaryOut, `"formed_up_to":`) || !strings.Contains(summaryOut, `"parked":0`) {
		t.Fatalf("a formed watermark must end the wait with the parked count, got %v\n%s", err, summaryOut)
	}
}

// A manifest is named after the whole file name, so a.txt and a.md keep their own. Two inputs with
// one file name are refused before anything is sent, since one would replace the other's manifest.
func TestIngestKeepsOneManifestPerFileNameAndRefusesTwoWithTheSameName(t *testing.T) {
	h := newIngestHarness(t)
	t.Setenv(envToken, h.token)
	dir := t.TempDir()
	manifests := filepath.Join(dir, "m")
	write := func(rel, content string) string {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if _, err := h.run(t, "--manifest-dir", manifests, write("a.txt", "Marta works at Ensera."), write("a.md", "Marta lives in Dublin.")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "a.md"} {
		if m := readManifest(t, manifests, name); m.Document != name || len(m.Segments) != 1 {
			t.Fatalf("the manifest for %s: %+v", name, m)
		}
	}
	before := h.observations(t)
	_, err := h.run(t, "--manifest-dir", manifests, write("x/b.txt", "One."), write("y/b.txt", "Two."))
	if err == nil || !strings.Contains(err.Error(), "share the file name") {
		t.Fatalf("two inputs named b.txt must be refused, got %v", err)
	}
	if h.observations(t) != before {
		t.Fatal("a refused run sent something")
	}
}
