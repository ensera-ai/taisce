// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"crypto/sha256"
	"encoding/json"
	"strconv"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
)

// Exact diagnostic text is identity: different rejected interpretations remain inspectable, while
// replay of the same proposal cannot inflate the corpus. The hash is not anonymization; source
// registration owns its erasure. No identity mapping outlives the rejected projection.
func rejectedIdentity(scope, observationID string, rejected domain.RejectedClaim, version string) (uuid.UUID, error) {
	source, err := uuid.Parse(observationID)
	if err != nil {
		return uuid.Nil, err
	}
	encoded, _ := json.Marshal([9]string{"rejected/v1", scope, source.String(), strconv.Itoa(rejected.SourceOrdinal), rejected.Predicate, rejected.Statement, rejected.Quote, rejected.Reason, version})
	return uuid.NewHash(sha256.New(), uuid.NameSpaceOID, encoded, 8), nil
}
