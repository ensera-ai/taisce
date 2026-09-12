// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"encoding/base64"
	"encoding/json"
)

// ArtifactContent uses one wire representation: a canonical base64 JSON string. encoding/json's
// default []byte decoder also accepts integer arrays, which would contradict the declared contract.
type ArtifactContent []byte

func (b *ArtifactContent) UnmarshalJSON(raw []byte) error {
	if len(raw) == 0 || raw[0] != '"' {
		return ErrInvalidArtifact
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return err
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxArtifactBytes) {
		return ErrInvalidArtifact
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != encoded {
		return ErrInvalidArtifact
	}
	*b = decoded
	return nil
}
