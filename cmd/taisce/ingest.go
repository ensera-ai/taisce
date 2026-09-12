// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// `taisce ingest` sends documents through the public write contract, as any client would, and
// writes a manifest saying what became of each one. It is the only command in this binary that
// speaks to the API rather than to the database, and deliberately so: a document that entered
// memory by a private door would carry no credential, no audit row and no admission check, and
// the citations it produced would be ones no client could have made.
//
// # What it protects, and from whom
//
// The token comes from the environment and never from a flag, because a flag is visible to every
// process on the machine through the process list. The document's bytes go to the API and nowhere
// else; the manifest holds names, digests, offsets and receipts, never text, so it can sit beside
// the documents or be shipped to whoever asked for the import without shipping the documents.
//
// # Why the command is resumable by construction rather than by state
//
// Every segment's idempotency key is a function of the document, its position, the speaker and
// the subject (internal/ingest). Running the command again therefore sends the same keys, and the
// API answers each with the receipt it already holds; the manifest is written fresh from those
// receipts. There is no resume file to lose or to trust: the API's own idempotency is the resume.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/ingest"
)

const (
	envAPI   = "TAISCE_API"
	envToken = "TAISCE_TOKEN"
)

// manifest is what the command writes for one document. Version names the shape so a reader of a
// manifest written by an older binary knows what it is reading.
type manifest struct {
	Version       int              `json:"version"`
	Document      string           `json:"document"`
	Digest        string           `json:"digest"`
	Bytes         int              `json:"bytes"`
	Role          string           `json:"role"`
	DataSubjectID string           `json:"data_subject_id,omitempty"`
	OccurredAt    *time.Time       `json:"occurred_at,omitempty"`
	SegmentBytes  int              `json:"segment_bytes"`
	Segments      []segmentReceipt `json:"segments"`
	// Refused says why the document stopped, if it did; a manifest with it set lists the segments
	// that were received before the refusal, and a rerun sends them all again as replays.
	Refused string `json:"refused,omitempty"`
}

type segmentReceipt struct {
	Ordinal        int    `json:"ordinal"`
	ByteStart      int    `json:"byte_start"`
	ByteEnd        int    `json:"byte_end"`
	IdempotencyKey string `json:"idempotency_key"`
	ObservationID  string `json:"observation_id"`
	LogOffset      int64  `json:"log_offset"`
}

// ingestOptions are the flags, parsed once and passed rather than read from globals.
type ingestOptions struct {
	api           string
	role          string
	dataSubjectID string
	occurredAt    string
	segmentBytes  int
	maxFileBytes  int
	manifestDir   string
	capacityWait  time.Duration
	wait          time.Duration
}

func ingestCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("ingest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var o ingestOptions
	flags.StringVar(&o.api, "api", os.Getenv(envAPI), "base URL of the API, e.g. http://127.0.0.1:8080 (or "+envAPI+")")
	flags.StringVar(&o.role, "role", string(domain.RoleTool), "the speaker a document is observed as: tool for third-party material, user for the person's own writing")
	flags.StringVar(&o.dataSubjectID, "data-subject", "", "observe under this data subject; without one the document is project-wide")
	flags.StringVar(&o.occurredAt, "occurred-at", "", "RFC 3339 time the documents happened, for those that do not say")
	flags.IntVar(&o.segmentBytes, "segment-bytes", ingest.DefaultSegmentBytes, "ceiling of one segment in UTF-8 bytes; one segment is one model call")
	flags.IntVar(&o.maxFileBytes, "max-file-bytes", ingest.DefaultMaxFileBytes, "refuse a file larger than this before reading it")
	flags.StringVar(&o.manifestDir, "manifest-dir", "taisce-ingest", "where one manifest per document is written")
	flags.DurationVar(&o.capacityWait, "capacity-wait", 5*time.Minute, "how long to wait for backlog capacity before giving up on a document")
	flags.DurationVar(&o.wait, "wait", 0, "after sending, wait up to this long for formation and report turns that parked")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("ingest: %w", err)
	}
	if flags.NArg() == 0 {
		return errors.New("ingest: name at least one document (.txt, .md or .json)")
	}
	// A manifest is named after its document's file name. Two inputs with one name would write one
	// manifest and the second would silently replace the first, so they are refused before
	// anything is sent.
	named := map[string]string{}
	for _, path := range flags.Args() {
		base := filepath.Base(path)
		if other, taken := named[base]; taken {
			return fmt.Errorf("ingest: %s and %s share the file name %q, and a run writes one manifest per name; ingest them in separate runs or with separate --manifest-dir", other, path, base)
		}
		named[base] = path
	}
	if o.api == "" {
		return errors.New("ingest: the API address is required (--api or " + envAPI + ")")
	}
	token := strings.TrimSpace(os.Getenv(envToken))
	if token == "" {
		return errors.New("ingest: " + envToken + " must hold a credential; it is never taken from a flag")
	}
	if o.role != string(domain.RoleTool) && o.role != string(domain.RoleUser) {
		return errors.New("ingest: --role must be tool or user")
	}
	var defaultTime *time.Time
	if o.occurredAt != "" {
		at, err := time.Parse(time.RFC3339, o.occurredAt)
		if err != nil {
			return fmt.Errorf("ingest: --occurred-at must be RFC 3339: %w", err)
		}
		defaultTime = &at
	}
	if err := os.MkdirAll(o.manifestDir, 0o700); err != nil {
		return fmt.Errorf("ingest: manifest directory: %w", err)
	}
	client := &apiClient{base: strings.TrimRight(o.api, "/"), token: token,
		http: &http.Client{Timeout: 30 * time.Second}}

	summary := struct {
		Documents  int    `json:"documents"`
		Refused    int    `json:"refused"`
		Segments   int    `json:"segments"`
		MaxOffset  int64  `json:"max_log_offset"`
		Parked     *int   `json:"parked,omitempty"`
		FormedUpTo *int64 `json:"formed_up_to,omitempty"`
	}{}
	var failures []string
	for _, path := range flags.Args() {
		m, err := ingestOne(ctx, client, path, o, defaultTime)
		summary.Documents++
		summary.Segments += len(m.Segments)
		for _, s := range m.Segments {
			if s.LogOffset > summary.MaxOffset {
				summary.MaxOffset = s.LogOffset
			}
		}
		if err != nil {
			m.Refused = err.Error()
			summary.Refused++
			failures = append(failures, m.Document+": "+err.Error())
		}
		if werr := writeManifest(o.manifestDir, m); werr != nil {
			return fmt.Errorf("ingest: %w", werr)
		}
		line := struct {
			Document string `json:"document"`
			Segments int    `json:"segments"`
			Refused  string `json:"refused,omitempty"`
		}{m.Document, len(m.Segments), m.Refused}
		if err := json.NewEncoder(out).Encode(line); err != nil {
			return err
		}
	}
	if o.wait > 0 && summary.Segments > 0 {
		formed, parked, err := client.awaitFormation(ctx, summary.MaxOffset, o.wait)
		if err != nil {
			return fmt.Errorf("ingest: waiting for formation: %w", err)
		}
		summary.FormedUpTo, summary.Parked = formed, &parked
		if formed == nil || *formed < summary.MaxOffset {
			failures = append(failures, fmt.Sprintf("formation did not reach offset %d within %s", summary.MaxOffset, o.wait))
		}
	}
	if err := json.NewEncoder(out).Encode(summary); err != nil {
		return err
	}
	if len(failures) > 0 {
		return errors.New("ingest: " + strings.Join(failures, "; "))
	}
	return nil
}

// ingestOne reads, cuts and sends one document, and returns the manifest so far with the error
// that stopped it, if one did. A refusal by the package leaves an empty segment list; a refusal by
// the API leaves the receipts received before it.
func ingestOne(ctx context.Context, client *apiClient, path string, o ingestOptions, defaultTime *time.Time) (manifest, error) {
	m := manifest{Version: 1, Document: filepath.Base(path), Role: o.role, DataSubjectID: o.dataSubjectID, SegmentBytes: o.segmentBytes}
	doc, err := ingest.Read(path, o.maxFileBytes)
	if err != nil {
		return m, err
	}
	m.Digest, m.Bytes = hex.EncodeToString(doc.Digest[:]), len(doc.Text)
	occurred := defaultTime
	if !doc.OccurredAt.IsZero() {
		at := doc.OccurredAt
		occurred = &at
	}
	m.OccurredAt = occurred
	segments, err := ingest.Split(doc.Text, o.segmentBytes)
	if err != nil {
		return m, err
	}
	for _, s := range segments {
		key := ingest.Key(doc.Digest, s.Ordinal, o.role, o.dataSubjectID).String()
		receipt, err := client.observe(ctx, observeBody{
			IdempotencyKey: key, DataSubjectID: o.dataSubjectID, OccurredAt: occurred,
			Messages: []observeMessage{{GroupOrdinal: 0, Role: o.role, Content: s.Text}},
		}, o.capacityWait)
		if err != nil {
			return m, fmt.Errorf("segment %d: %w", s.Ordinal, err)
		}
		m.Segments = append(m.Segments, segmentReceipt{Ordinal: s.Ordinal, ByteStart: s.ByteStart, ByteEnd: s.ByteEnd,
			IdempotencyKey: key, ObservationID: receipt.ID, LogOffset: receipt.LogOffset})
	}
	return m, nil
}

func writeManifest(dir string, m manifest) error {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// The whole file name, extension included: a.txt and a.md are two documents.
	return os.WriteFile(filepath.Join(dir, m.Document+".manifest.json"), append(body, '\n'), 0o600)
}

// The wire shapes this command uses, written here rather than imported from the API package: a
// client that shares the server's types cannot notice the server changing them.
type observeBody struct {
	IdempotencyKey string           `json:"idempotency_key"`
	DataSubjectID  string           `json:"data_subject_id,omitempty"`
	OccurredAt     *time.Time       `json:"occurred_at,omitempty"`
	Messages       []observeMessage `json:"messages"`
}

type observeMessage struct {
	GroupOrdinal int    `json:"group_ordinal"`
	Role         string `json:"role"`
	Content      string `json:"content"`
}

type observeReceipt struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	LogOffset int64  `json:"log_offset"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

// observe sends one segment. A full backlog (429) is waited out with bounded backoff up to
// capacityWait, because a bulk import is exactly the caller that should retry with the same key;
// a conflict (409) is a document that changed under a key it had already used, which cannot
// happen with keys derived from the digest and is refused rather than papered over.
func (c *apiClient) observe(ctx context.Context, body observeBody, capacityWait time.Duration) (observeReceipt, error) {
	deadline := time.Now().Add(capacityWait)
	backoff := time.Second
	for {
		status, raw, err := c.post(ctx, "/v1/observations", body)
		if err != nil {
			return observeReceipt{}, err
		}
		switch status {
		case http.StatusCreated:
			var r observeReceipt
			if err := json.Unmarshal(raw, &r); err != nil {
				return observeReceipt{}, fmt.Errorf("receipt: %w", err)
			}
			return r, nil
		case http.StatusTooManyRequests:
			if time.Now().Add(backoff).After(deadline) {
				return observeReceipt{}, errors.New("backlog capacity stayed full for " + capacityWait.String())
			}
			select {
			case <-ctx.Done():
				return observeReceipt{}, ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		default:
			var e apiError
			_ = json.Unmarshal(raw, &e)
			if e.Code == "" {
				e.Code = http.StatusText(status)
			}
			return observeReceipt{}, fmt.Errorf("refused (%d %s): %s", status, e.Code, e.Message)
		}
	}
}

// awaitFormation polls freshness until the formed watermark reaches the offset or the wait ends,
// and returns the watermark and the parked count as they stood.
func (c *apiClient) awaitFormation(ctx context.Context, offset int64, wait time.Duration) (*int64, int, error) {
	deadline := time.Now().Add(wait)
	for {
		status, raw, err := c.get(ctx, "/v1/freshness")
		if err != nil {
			return nil, 0, err
		}
		if status != http.StatusOK {
			return nil, 0, fmt.Errorf("freshness answered %d", status)
		}
		var f struct {
			Formed *int64 `json:"formed"`
			Parked int    `json:"parked"`
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, 0, err
		}
		if (f.Formed != nil && *f.Formed >= offset) || time.Now().After(deadline) {
			return f.Formed, f.Parked, nil
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (c *apiClient) post(ctx context.Context, path string, body any) (int, []byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *apiClient) get(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, nil, err
	}
	return c.do(req)
}

func (c *apiClient) do(req *http.Request) (int, []byte, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	// A refusal body is small; a receipt is small. Anything larger is not this API.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, raw, nil
}
