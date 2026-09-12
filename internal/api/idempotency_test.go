// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

type countedRetryModel struct{ calls atomic.Int32 }

func (m *countedRetryModel) Propose(ctx context.Context, message domain.Message, vocabulary domain.Ontology) ([]extract.Proposal, error) {
	m.calls.Add(1)
	return (scriptedModel{}).Propose(ctx, message, vocabulary)
}

type retryReceipt struct {
	ID     string `json:"id"`
	Offset int64  `json:"log_offset"`
}

func TestConcurrentObservationRetriesShareOneReceiptAndOneFormation(t *testing.T) {
	h := newHarness(t, "api_retries")
	ctx := context.Background()
	body := map[string]any{"idempotency_key": "7fd4c819-dc01-445a-9ea7-e322e76cd2e8", "data_subject_id": "retry-subject",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}
	wire, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		receipt retryReceipt
		err     error
	}
	receipts := make(chan result, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result result
			var err error
			for attempt := 0; attempt < 20; attempt++ {
				req, requestErr := http.NewRequest(http.MethodPost, h.server.URL+"/v1/observations", bytes.NewReader(wire))
				if requestErr != nil {
					err = requestErr
					break
				}
				req.Header.Set("Authorization", "Bearer "+h.token)
				resp, requestErr := h.server.Client().Do(req)
				err = requestErr
				if err == nil {
					if resp.StatusCode == http.StatusTooManyRequests {
						retryAfter := resp.Header.Get("Retry-After")
						resp.Body.Close()
						if retryAfter != "1" {
							err = fmt.Errorf("missing retry contract: %q", retryAfter)
							break
						}
						err = fmt.Errorf("admission retries exhausted")
						time.Sleep(time.Second + time.Duration(i*7+attempt*11)*time.Millisecond)
						continue
					}
					if resp.StatusCode != http.StatusCreated {
						err = fmt.Errorf("retry returned %d", resp.StatusCode)
					} else {
						err = json.NewDecoder(resp.Body).Decode(&result.receipt)
					}
					resp.Body.Close()
				}
				break
			}
			result.err = err
			receipts <- result
		}()
	}
	wg.Wait()
	close(receipts)
	var first retryReceipt
	for result := range receipts {
		if result.err != nil {
			t.Error(result.err)
			continue
		}
		if first.ID == "" {
			first = result.receipt
		}
		if result.receipt != first {
			t.Errorf("retry got a different receipt: %+v %+v", first, result.receipt)
		}
	}
	if first.ID == "" || first.Offset != 0 {
		t.Fatalf("missing first receipt: %+v", first)
	}
	for table, want := range map[string]int{"observation": 1, "turn_message": 1, "chunk": 1, "observation_retry": 1, "projection_dependency": 1} {
		var count int
		if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.`+table)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("retries produced %d %s rows, want %d", count, table, want)
		}
	}
	// A caller that lost its response after commit recovers the exact receipt without a timestamp.
	var recovered retryReceipt
	h.do(t, http.MethodPost, "/v1/observations", body, http.StatusCreated, &recovered)
	if recovered != first {
		t.Fatal("lost-response retry did not recover the receipt")
	}
	vocabulary, err := pg.LoadOntology(ctx, h.pool, h.schema)
	if err != nil {
		t.Fatal(err)
	}
	model := &countedRetryModel{}
	observations := pg.NewObservationStore(h.pool)
	worker := formation.NewWorker(h.pool, observations, formation.NewFormer(observations, pg.NewFactStore(h.pool), extract.New(model, vocabulary)))
	if _, err := worker.Drain(ctx, h.schema, "p1"); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/observations", body, http.StatusCreated, nil)
	if _, err := worker.Drain(ctx, h.schema, "p1"); err != nil {
		t.Fatal(err)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("retries multiplied extraction calls: %d", model.calls.Load())
	}
	// A key cannot silently change its subject or content.
	body["data_subject_id"] = "different-subject"
	status, response := h.raw(t, http.MethodPost, "/v1/observations", body, h.token)
	if status != http.StatusConflict {
		t.Fatalf("changed payload returned %d: %s", status, response)
	}
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response, &refusal); err != nil {
		t.Fatal(err)
	}
	if refusal.Error.Code != "idempotency_conflict" {
		t.Fatalf("unstable conflict code: %s", response)
	}
	// The same UUID in another authorized project must not disclose the first project's receipt.
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	other, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "retry-other-project", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, response = h.raw(t, http.MethodPost, "/v1/observations", body, other)
	if status != http.StatusCreated {
		t.Fatalf("other project key returned %d: %s", status, response)
	}
	var separate retryReceipt
	if err := json.Unmarshal(response, &separate); err != nil {
		t.Fatal(err)
	}
	if separate.ID == first.ID || separate.Offset != 0 {
		t.Fatalf("project key scopes collided: %+v", separate)
	}
	// Without a key, repeated turns are intentional independent observations.
	delete(body, "idempotency_key")
	h.do(t, http.MethodPost, "/v1/observations", body, http.StatusCreated, &separate)
	if separate.Offset != 1 {
		t.Fatalf("retry/conflict consumed offsets: %+v", separate)
	}
	h.do(t, http.MethodPost, "/v1/observations", body, http.StatusCreated, &separate)
	if separate.Offset != 2 {
		t.Fatalf("content-only deduplication changed an intentional repeat: %+v", separate)
	}
}

func TestErasureAndRetentionPreventStaleRetriesFromRestoringContent(t *testing.T) {
	for _, mode := range []string{"erasure", "retention"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, "api_retry_"+mode)
			ctx := context.Background()
			body := map[string]any{"idempotency_key": "a99d8051-89cd-491f-a2e7-8da216903986", "data_subject_id": "forget-retries",
				"messages": []map[string]any{{"role": "user", "content": "do not restore this content"}}}
			var original retryReceipt
			h.do(t, http.MethodPost, "/v1/observations", body, http.StatusCreated, &original)
			if mode == "erasure" {
				h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "forget-retries"}, http.StatusOK, nil)
			} else {
				if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.observation SET retention_until=$1`), time.Now().Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.NewRetentionStore(h.pool, h.schema).Sweep(ctx, "p1", 100); err != nil {
					t.Fatal(err)
				}
			}
			status, response := h.raw(t, http.MethodPost, "/v1/observations", body, h.token)
			if status != http.StatusConflict {
				t.Fatalf("stale retry restored content: %d %s", status, response)
			}
			var tombstones, linked, observations int
			if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*),count(request_digest)+count(observation_id) FROM {schema}.observation_retry`)).Scan(&tombstones, &linked); err != nil {
				t.Fatal(err)
			}
			if tombstones != 1 || linked != 0 {
				t.Fatalf("erasure retained retry payload linkage: %d %d", tombstones, linked)
			}
			if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&observations); err != nil {
				t.Fatal(err)
			}
			if observations != 0 {
				t.Fatalf("stale retry recreated %d observations", observations)
			}
		})
	}
}

func TestInvalidRetryKeysAreRefusedBeforeAllocatingAnOffset(t *testing.T) {
	h := newHarness(t, "api_retry_invalid")
	for _, key := range []string{"email@example.invalid", "7fd4c819dc01445a9ea7e322e76cd2e8", "not-a-uuid"} {
		h.do(t, http.MethodPost, "/v1/observations", map[string]any{"idempotency_key": key, "messages": []map[string]any{{"role": "user", "content": "hi"}}}, http.StatusBadRequest, nil)
	}
	var count int
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT count(*) FROM {schema}.watermark`)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid retry key allocated an offset")
	}
}

func TestRetryReceiptFailureRollsBackTheObservation(t *testing.T) {
	h := newHarness(t, "api_retry_rollback")
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.observation_retry ADD CONSTRAINT refuse_test_receipt CHECK (false)`)); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"idempotency_key": "901e7647-a283-4d62-9322-2db54b0f22a7", "messages": []map[string]any{{"role": "user", "content": "transactional receipt"}}}
	h.do(t, http.MethodPost, "/v1/observations", body, http.StatusInternalServerError, nil)
	for _, table := range []string{"observation", "watermark", "turn_message", "chunk", "projection_dependency", "observation_retry"} {
		var count int
		if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.`+table)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed receipt left %d rows in %s", count, table)
		}
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.observation_retry DROP CONSTRAINT refuse_test_receipt`)); err != nil {
		t.Fatal(err)
	}
	var receipt retryReceipt
	h.do(t, http.MethodPost, "/v1/observations", body, http.StatusCreated, &receipt)
	if receipt.Offset != 0 {
		t.Fatalf("failed receipt consumed offset: %+v", receipt)
	}
}

func TestRetryFingerprintIsStableAcrossDomainRefactors(t *testing.T) {
	h := newHarness(t, "api_retry_format")
	body := map[string]any{"idempotency_key": "a30671f0-35b4-44f6-80b8-d28f43ba081f", "data_subject_id": "subject-1",
		"occurred_at": "2026-03-01T00:00:00Z", "messages": []map[string]any{{"role": "user", "content": "remember me"}}}
	var original retryReceipt
	h.do(t, http.MethodPost, "/v1/observations", body, http.StatusCreated, &original)
	var fingerprint string
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT encode(request_digest,'hex') FROM {schema}.observation_retry`)).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	// Pinned to the persisted representation, so reordering unrelated domain fields cannot silently
	// make receipts written by an earlier binary unrecoverable. The turn's time is not part of it,
	// so the retry below carries a different instant and still replays.
	if fingerprint != "2efc655148afb88df72c09b4deebe77ad19318264cee75913154815fce2dc357" {
		t.Fatalf("persisted fingerprint changed: %s", fingerprint)
	}
	body["occurred_at"] = "2026-03-01T00:00:03Z"
	body["idempotency_key"] = "A30671F0-35B4-44F6-80B8-D28F43BA081F"
	var replay retryReceipt
	h.do(t, http.MethodPost, "/v1/observations", body, http.StatusCreated, &replay)
	if replay != original {
		t.Fatal("a retry carrying another time, or the key in another case, changed identity")
	}
}
