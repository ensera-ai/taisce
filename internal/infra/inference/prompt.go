// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference

import (
	_ "embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ensera-ai/taisce/internal/domain"
)

// The extraction prompt is a file because it is prose, and prose is reviewed as prose. It is
// compiled in because it decides what this system extracts, and extraction feeds the write path: a
// prompt the filesystem can change is a path by which whoever can write next to the binary changes
// what ends up in a customer's memory. Embedding keeps the review benefit and closes that.
//
//go:embed prompts/extraction.yaml
var extractionPromptFile []byte

// relationsPlaceholder is where the vocabulary is rendered.
//
// The predicate table is not written in the file: it is enforced by a foreign key, and a second
// copy of a closed set is a second place for it to drift. The file marks the position, the code
// renders the rows.
const relationsPlaceholder = "{{relations}}"

// promptFile is the wording, and only the wording.
//
// Every field is required. A section that may be absent is a prompt that can be silently shortened
// by an edit, and the section most worth removing — the one telling the model the message is data —
// is the one whose absence produces no error and no visible change until somebody plants a fact.
type promptFile struct {
	ModelContract struct {
		Temperature    float32 `yaml:"temperature"`
		ResponseFormat string  `yaml:"response_format"`
	} `yaml:"model_contract"`

	Task         string `yaml:"task"`
	Relations    string `yaml:"relations"`
	QuoteRule    string `yaml:"quote_rule"`
	Polarity     string `yaml:"polarity"`
	Tense        string `yaml:"tense"`
	ReplyShape   string `yaml:"reply_shape"`
	DataBoundary string `yaml:"data_boundary"`
}

// extractionPrompt is parsed once, at startup.
//
// A panic is right here. The file is embedded, so a failure is not a runtime condition that might
// clear — it is a binary that must not start, and it is caught by any test run before it is ever
// caught by a deployment.
var extractionPrompt = mustLoadPrompt(extractionPromptFile)

func mustLoadPrompt(raw []byte) promptFile {
	p, err := loadPrompt(raw)
	if err != nil {
		panic("inference: the embedded extraction prompt is unusable: " + err.Error())
	}
	return p
}

// loadPrompt parses the prompt file and refuses one that could not compose a whole prompt.
func loadPrompt(raw []byte) (promptFile, error) {
	var p promptFile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return promptFile{}, fmt.Errorf("parse: %w", err)
	}

	for _, section := range []struct {
		name string
		text string
	}{
		{"task", p.Task},
		{"relations", p.Relations},
		{"quote_rule", p.QuoteRule},
		{"polarity", p.Polarity},
		{"tense", p.Tense},
		{"reply_shape", p.ReplyShape},
		{"data_boundary", p.DataBoundary},
	} {
		if strings.TrimSpace(section.text) == "" {
			return promptFile{}, fmt.Errorf("section %q is missing or empty", section.name)
		}
	}

	// Exactly one. None means the model is told the vocabulary is closed and never shown it, which
	// makes every extraction unmapped. More than one means the table is stated twice, and a
	// vocabulary that appears twice is one an edit can make disagree with itself.
	if n := strings.Count(p.Relations, relationsPlaceholder); n != 1 {
		return promptFile{}, fmt.Errorf("section \"relations\" holds %s %d times, wanted once",
			relationsPlaceholder, n)
	}

	if p.ModelContract.ResponseFormat == "" {
		return promptFile{}, fmt.Errorf("model_contract.response_format is missing")
	}
	return p, nil
}

// systemPrompt composes the prompt: the file's wording, in an order the file does not choose.
//
// The order is fixed here, and data_boundary is last, because that section's job is to be the final
// word before somebody else's text arrives. A file that could reorder the sections could put the
// injection boundary in the middle, where an instruction after it appears to supersede it.
func systemPrompt(vocabulary domain.Ontology) string {
	relations := strings.Replace(
		extractionPrompt.Relations, relationsPlaceholder, renderVocabulary(vocabulary), 1)

	return strings.Join([]string{
		extractionPrompt.Task,
		relations,
		extractionPrompt.QuoteRule,
		extractionPrompt.Polarity,
		extractionPrompt.Tense,
		extractionPrompt.ReplyShape,
		extractionPrompt.DataBoundary,
	}, "\n\n")
}

// renderVocabulary writes the relations out in full, one per line.
//
// Each relation carries its description and the kind of thing its object should be. Naming the
// relations without saying what they mean produces confident misuse — `part_of` for employment,
// `has_status` for anything — and a wrong relation is worse than an unmapped one, because it is
// admitted and nothing downstream can tell it apart from a right one.
//
// The order is the vocabulary's own, which is the order the database holds it in. Stable, so that
// two extractions of one message are given the same list: a prompt that reorders produces different
// output for the same input, and extraction quality stops being measurable.
func renderVocabulary(vocabulary domain.Ontology) string {
	rows := make([]string, 0, vocabulary.Len())
	for _, p := range vocabulary.All() {
		rows = append(rows, fmt.Sprintf("  %-16s object is a %-13s %s", p.Name, p.ObjectKind, p.Description))
	}
	return strings.Join(rows, "\n")
}

// reportPromptFileShape is the report prompt's wording, and only the wording.
//
// Every field is required, for the reason the extraction prompt's are: a section that may be absent is
// a prompt that can be silently shortened by an edit, and the section most worth removing — the one
// telling the model the material is data — is the one whose absence produces no error and no visible
// change until somebody plants an instruction in a quote.
type reportPromptFileShape struct {
	ModelContract struct {
		Temperature    float32 `yaml:"temperature"`
		ResponseFormat string  `yaml:"response_format"`
	} `yaml:"model_contract"`

	Task         string `yaml:"task"`
	Grounding    string `yaml:"grounding"`
	Shape        string `yaml:"shape"`
	ReplyShape   string `yaml:"reply_shape"`
	DataBoundary string `yaml:"data_boundary"`
}

// loadReportPrompt parses the report prompt and refuses one that could not compose a whole prompt.
func loadReportPrompt(raw []byte) (reportPromptFileShape, error) {
	var p reportPromptFileShape
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return reportPromptFileShape{}, fmt.Errorf("parse: %w", err)
	}
	for _, section := range []struct {
		name string
		text string
	}{
		{"task", p.Task},
		{"grounding", p.Grounding},
		{"shape", p.Shape},
		{"reply_shape", p.ReplyShape},
		{"data_boundary", p.DataBoundary},
	} {
		if strings.TrimSpace(section.text) == "" {
			return reportPromptFileShape{}, fmt.Errorf("section %q is missing or empty", section.name)
		}
	}
	if p.ModelContract.ResponseFormat == "" {
		return reportPromptFileShape{}, fmt.Errorf("model_contract.response_format is missing")
	}
	return p, nil
}
