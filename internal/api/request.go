// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxRequestBytes = 1 << 20

// decode validates the complete wire representation before populating a request. The standard
// decoder alone accepts duplicate keys, repairs invalid UTF-8 and stops after the first value;
// those behaviours let the same bytes mean different things to the caller and the service.
func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err == nil && utf8.Valid(body) && validSurrogates(body) {
		err = unambiguousObject(body)
		if err == nil {
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			err = decoder.Decode(into)
			if err == nil {
				return true
			}
		}
	}
	// Parser errors can contain supplied keys or content. Keep them off the response and log paths.
	writeError(w, http.StatusBadRequest, codeInvalidBody, "the request body could not be read")
	return false
}

func unambiguousObject(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return fmt.Errorf("request must be an object")
	}
	if err := uniqueMembers(d, '{', 1); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("request must end after its object")
	}
	return nil
}

// uniqueMembers walks tokens rather than materialising a second request tree. Nesting is bounded
// independently of byte size, and keys are scoped to each object. Folded keys also refuse aliases
// such as question/QUESTION, which encoding/json would otherwise assign to the same Go field.
func uniqueMembers(d *json.Decoder, opening json.Delim, depth int) error {
	if depth > 128 {
		return fmt.Errorf("request nesting exceeds limit")
	}
	keys := map[string]bool{}
	for d.More() {
		if opening == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("object key must be a string")
			}
			name = foldJSONKey(name)
			if keys[name] {
				return fmt.Errorf("duplicate object key")
			}
			keys[name] = true
		}
		value, err := d.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := value.(json.Delim); ok {
			if err := uniqueMembers(d, delimiter, depth+1); err != nil {
				return err
			}
		}
	}
	_, err := d.Token() // The decoder checks that the closing delimiter matches.
	return err
}

func foldJSONKey(key string) string {
	return strings.Map(func(r rune) rune {
		least := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < least {
				least = next
			}
		}
		return least
	}, key)
}

// JSON's UTF-16 escapes must name Unicode scalar values. encoding/json repairs an unpaired
// surrogate to U+FFFD; doing that to evidence would change the caller's content before storage.
func validSurrogates(body []byte) bool {
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		i++
		if i >= len(body) || body[i] != 'u' {
			continue
		}
		if i+4 >= len(body) {
			return false
		}
		value, err := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
