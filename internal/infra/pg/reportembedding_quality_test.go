//go:build inference && qualification

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

const reportQualityCorpus = "report-themes-v1"

type reportQualityTopic struct {
	title, summary, question string
}

func reportQualityTopics() []reportQualityTopic {
	return []reportQualityTopic{
		{"Database migration", "The PostgreSQL cutover is blocked by a schema lock and needs coordinated migration sequencing.", "What theme is holding up the database work?"},
		{"Payment migration", "The card-payment move to Stripe is waiting for the card vault and contract approval.", "What is going on with card payments?"},
		{"Weekend cycling", "The cycling club meets on Saturday mornings and rides together along the coast road.", "What do I usually do at weekends?"},
		{"Hiring expansion", "Recruiting, interview capacity and new office space form one plan for increasing headcount.", "What broad effort explains the extra interviews and office planning?"},
		{"Security audit", "Credential rotation, access reviews and remediation evidence are being coordinated for a security audit.", "Why are credentials and access permissions being reviewed together?"},
		{"Customer renewal", "Pricing, legal terms and product commitments are converging around a major customer contract renewal.", "What theme connects pricing talks, lawyers and customer commitments?"},
		{"Office relocation", "The team is arranging a new lease, furniture and network installation for an office move.", "What larger activity connects the lease, desks and network installation?"},
		{"Mobile release", "Crash fixes, store approval and staged rollout are the remaining parts of the mobile application release.", "What effort ties together crash fixes and app-store approval?"},
		{"Model evaluation", "Benchmark labels, drift analysis and human review form the evaluation of a new machine-learning model.", "Why are benchmark labels and drift reviews happening?"},
		{"Backup recovery", "Snapshot validation, restore drills and recovery objectives are part of the backup recovery program.", "What theme includes restore drills and recovery objectives?"},
		{"Supply chain delay", "Warehouse capacity, customs clearance and delayed shipments are creating one supply-chain constraint.", "What broad problem connects customs, warehouses and late shipments?"},
		{"Marketing launch", "Audience research, campaign creative and launch scheduling support the upcoming marketing campaign.", "What effort brings together audience research and campaign creative?"},
		{"Budget planning", "Cost forecasts, department requests and finance review are being reconciled for annual budget planning.", "Why are forecasts and department spending requests being reconciled?"},
		{"Support incident", "A growing support queue, escalations and missed response targets are being handled as one service incident.", "What theme explains the queue, escalations and missed response targets?"},
		{"Training program", "Curriculum design, instructor workshops and attendance planning form a staff training program.", "What larger activity connects curriculum, workshops and attendance?"},
		{"Privacy program", "Consent records, retention rules and erasure handling are coordinated in the data privacy program.", "Why are consent, retention and deletion being discussed together?"},
		{"API performance", "Latency profiling, cache tuning and throughput tests form the API performance improvement effort.", "What effort connects latency measurements, caches and throughput?"},
		{"Product roadmap", "Customer feedback, priority decisions and milestone planning are shaping the next product roadmap.", "What theme ties user feedback to priorities and milestones?"},
		{"Team offsite", "Travel bookings, a venue and workshop agendas are being organized for the team offsite.", "What activity explains the travel, venue and workshop agenda?"},
		{"Hardware purchase", "Server specifications, supplier quotes and delivery schedules form the hardware procurement process.", "Why are server specifications and vendor quotes being compared?"},
		{"Compliance review", "Control evidence, auditor questions and remediation tasks support the compliance certification review.", "What effort links control evidence, auditors and remediation?"},
		{"Localization rollout", "Translations, locale testing and right-to-left layout fixes are part of the localization rollout.", "What broad effort includes translations and right-to-left layout work?"},
		{"Search relevance", "Ranking evaluation, synonym behavior and click analysis form the search relevance investigation.", "Why are ranking, synonyms and click data being studied together?"},
		{"Vendor transition", "Data export, import validation and a coordinated cutoff form the transition between software vendors.", "What theme connects data export, import checks and a cutoff date?"},
	}
}

type durationSummary struct {
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

func summarizeDurations(values []time.Duration) durationSummary {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	at := func(percent int) float64 {
		index := (len(values)*percent + 99) / 100
		if index < 1 {
			index = 1
		}
		return float64(values[index-1].Microseconds()) / 1000
	}
	return durationSummary{P50MS: at(50), P95MS: at(95), P99MS: at(99)}
}

func TestReportEmbeddingNamedCorpusQualityAndANNRecall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.TrimSpace(os.Getenv(inference.EnvEmbeddingRevision))
	if revision == "" {
		t.Fatalf("%s requires an explicit %s", reportQualityCorpus, inference.EnvEmbeddingRevision)
	}
	embedder := inference.NewEmbedder(config)
	topics := reportQualityTopics()
	questions := make([]string, len(topics))
	for i := range topics {
		questions[i] = topics[i].question
	}
	queryVectors, err := embedder.Embed(ctx, questions)
	if err != nil || len(queryVectors) != len(questions) {
		t.Fatalf("query vectors=%d err=%v", len(queryVectors), err)
	}
	endpoint, _ := config.EmbeddingsAt()
	digest := sha256.Sum256([]byte(endpoint))
	model := pg.EmbeddingModel{Name: config.EmbeddingModel, Revision: revision,
		EndpointHash: hex.EncodeToString(digest[:]), Dimensions: len(queryVectors[0])}
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}

	pool := testPool(t)
	schema := tenant(t, pool, "report_embedding_quality")
	defer pool.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE")
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{
		Role: domain.RoleUser, Content: "Qualification corpus source; every generated report remains source-owned.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	const variantsPerTopic = 48
	total := len(topics) * variantsPerTopic
	communityIDs := make([]string, 0, total)
	reportIDs := make([]string, 0, total)
	titles := make([]string, 0, total)
	summaries := make([]string, 0, total)
	for _, topic := range topics {
		for variant := 0; variant < variantsPerTopic; variant++ {
			communityIDs = append(communityIDs, uuid.NewString())
			reportIDs = append(reportIDs, uuid.NewString())
			titles = append(titles, topic.title)
			summaries = append(summaries, fmt.Sprintf("%s Workstream %d records checkpoint %d and owner group %d.",
				topic.summary, variant+1, (variant*7)%31+1, (variant*11)%17+1))
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.community(scope,community_id,level)
 SELECT 'p1',id,0 FROM unnest($1::uuid[]) AS id`), communityIDs); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.community_report
 (scope,report_id,community_id,title,summary,importance,importance_reason,findings)
 SELECT 'p1',u.report_id,u.community_id,u.title,u.summary,5,'qualification corpus','[]'::jsonb
 FROM unnest($1::uuid[],$2::uuid[],$3::text[],$4::text[]) AS u(report_id,community_id,title,summary)`),
		reportIDs, communityIDs, titles, summaries); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency
 (source_observation_id,scope,projection_kind,projection_id,data_subject_id,report_source_revision)
 SELECT o.observation_id,o.scope,'community_report',r.report_id::text,o.data_subject_id,o.fact_revision
 FROM {schema}.observation o CROSS JOIN unnest($2::uuid[]) AS r(report_id)
 WHERE o.scope='p1' AND o.observation_id=$1::uuid`), source.ID, reportIDs); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	store := pg.NewReportEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	started := time.Now()
	generation, err := store.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	for {
		page, err := store.BuildPage(ctx, "p1", generation.ID, actor, model, pg.MaxEmbeddingPage, embedder.Embed)
		if err != nil {
			t.Fatal(err)
		}
		if page.Complete {
			break
		}
	}
	if err := store.Activate(ctx, "p1", generation.ID, actor); err != nil {
		t.Fatal(err)
	}
	buildDuration := time.Since(started)

	const limit = 10
	annBounds := []int{32, 64, 128}
	_, _ = store.Search(ctx, "p1", actor, model, queryVectors[0], pg.ReportCandidateOptions{Limit: limit})
	for _, bound := range annBounds {
		_, _ = store.Search(ctx, "p1", actor, model, queryVectors[0], pg.ReportCandidateOptions{Limit: limit, Candidates: bound})
	}
	exactLatency := make([]time.Duration, 0, len(topics))
	annLatency := make(map[int][]time.Duration, len(annBounds))
	annOverlap := make(map[int]int, len(annBounds))
	topicHits := 0
	for i, topic := range topics {
		began := time.Now()
		exact, err := store.Search(ctx, "p1", actor, model, queryVectors[i], pg.ReportCandidateOptions{Limit: limit})
		exactLatency = append(exactLatency, time.Since(began))
		if err != nil || len(exact.Candidates) != limit {
			t.Fatalf("exact query %d candidates=%d err=%v", i, len(exact.Candidates), err)
		}
		if exact.Candidates[0].TitlePreview == topic.title {
			topicHits++
		}
		truth := make(map[string]struct{}, limit)
		for _, candidate := range exact.Candidates {
			truth[candidate.ReportID] = struct{}{}
		}
		for _, bound := range annBounds {
			began = time.Now()
			approximate, err := store.Search(ctx, "p1", actor, model, queryVectors[i], pg.ReportCandidateOptions{
				Limit: limit, Candidates: bound,
			})
			annLatency[bound] = append(annLatency[bound], time.Since(began))
			if err != nil || !approximate.Approximate || approximate.Generation.Project != "p1" || len(approximate.Candidates) != limit {
				t.Fatalf("ANN query %d bound=%d result=%+v err=%v", i, bound, approximate, err)
			}
			for _, candidate := range approximate.Candidates {
				if _, exists := truth[candidate.ReportID]; exists {
					annOverlap[bound]++
				}
			}
		}
	}
	type annMeasurement struct {
		Candidates int             `json:"candidates"`
		RecallAt10 float64         `json:"recall_at_10"`
		Latency    durationSummary `json:"latency"`
	}
	ann := make([]annMeasurement, 0, len(annBounds))
	for _, bound := range annBounds {
		ann = append(ann, annMeasurement{Candidates: bound,
			RecallAt10: float64(annOverlap[bound]) / float64(len(topics)*limit),
			Latency:    summarizeDurations(annLatency[bound])})
	}
	measurement := struct {
		Corpus              string            `json:"corpus"`
		Model               pg.EmbeddingModel `json:"model"`
		Reports             int               `json:"reports"`
		Queries             int               `json:"queries"`
		CacheState          string            `json:"cache_state"`
		WarmupQueries       int               `json:"warmup_queries"`
		BuildSeconds        float64           `json:"build_seconds"`
		ExactTopicRecallAt1 float64           `json:"exact_topic_recall_at_1"`
		ExactLatency        durationSummary   `json:"exact_latency"`
		ANN                 []annMeasurement  `json:"ann"`
	}{reportQualityCorpus, model, total, len(topics), "warm_after_build", 1, buildDuration.Seconds(),
		float64(topicHits) / float64(len(topics)), summarizeDurations(exactLatency), ann}
	encoded, err := json.Marshal(measurement)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("REPORT_QUALITY_JSON %s", encoded)
}
