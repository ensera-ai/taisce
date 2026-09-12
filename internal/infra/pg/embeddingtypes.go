// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxStoredEmbeddingDimensions = 4000
const MaxRetainedEmbeddingGenerations = 2
const MaxEmbeddingPage = 64
const MaxEmbeddingPageBytes = 1 << 20

var (
	ErrInvalidEmbedding    = errors.New("invalid embedding request or model contract")
	ErrEmbeddingConflict   = errors.New("embedding generation, source or model contract conflicts")
	ErrEmbeddingNotFound   = errors.New("embedding generation not found")
	ErrEmbeddingBusy       = errors.New("embedding generation is already being built")
	ErrEmbeddingIncomplete = errors.New("embedding generation is incomplete or stale")
	ErrEmbeddingCapacity   = errors.New("prune a non-active embedding generation before starting another")
)

// EndpointHash identifies the configured embedding endpoint without publishing its URL or key.
// Revision is operator-declared: an OpenAI-compatible endpoint cannot attest unchanged model weights.
type EmbeddingModel struct {
	Name         string `json:"name"`
	Revision     string `json:"revision"`
	EndpointHash string `json:"endpoint_hash"`
	Dimensions   int    `json:"dimensions"`
}

func (m EmbeddingModel) Validate() error {
	for _, v := range []struct {
		s string
		n int
	}{{m.Name, 256}, {m.Revision, 128}} {
		if len(v.s) > v.n || strings.TrimSpace(v.s) == "" || !utf8.ValidString(v.s) || strings.ContainsRune(v.s, 0) {
			return ErrInvalidEmbedding
		}
	}
	digest, err := hex.DecodeString(m.EndpointHash)
	if err != nil || len(digest) != 32 || m.EndpointHash != strings.ToLower(m.EndpointHash) || m.Dimensions < 1 || m.Dimensions > MaxStoredEmbeddingDimensions {
		return ErrInvalidEmbedding
	}
	return nil
}
func (m EmbeddingModel) Identity() string {
	data, _ := json.Marshal(struct {
		Model        EmbeddingModel
		Metric       string
		InputVersion string
	}{m, "cosine", "message-text/v1"})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type EmbeddingGeneration struct {
	ID                   string              `json:"id"`
	Project              string              `json:"project"`
	Key                  string              `json:"operation_key"`
	Model                EmbeddingModel      `json:"model"`
	ThroughOffset        int64               `json:"through_offset"`
	InitialThroughOffset int64               `json:"initial_through_offset"`
	CoveredThroughOffset int64               `json:"covered_through_offset"`
	After                ChunkRecoveryCursor `json:"after"`
	State                string              `json:"state"`
	Active               bool                `json:"active"`
	Examined             int64               `json:"examined"`
	Stored               int64               `json:"stored"`
	CreatedAt            time.Time           `json:"created_at"`
}

type MessageEmbeddingStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewMessageEmbeddingStore(pool *pgxpool.Pool, schema Schema) *MessageEmbeddingStore {
	return &MessageEmbeddingStore{pool: pool, schema: schema}
}
func validEmbeddingIDs(scope string, ids ...string) error {
	if _, err := NewSchema(scope); err != nil {
		return ErrInvalidEmbedding
	}
	for _, value := range ids {
		if id, err := uuid.Parse(value); err != nil || id == uuid.Nil {
			return ErrInvalidEmbedding
		}
	}
	return nil
}
