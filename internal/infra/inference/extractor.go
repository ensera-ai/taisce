package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/taisce/taisce/internal/domain"
	"github.com/taisce/taisce/internal/extract"
)

// Model proposes claims by asking a chat model.
//
// It proposes and nothing more. It does not decide whether a relation is admitted, does not locate a
// quote, and does not build a byte span — all of that is in the extractor, deliberately, because
// those are the decisions with a correctness argument and they must not vary with the vendor.
type Model struct {
	config Config
	client *http.Client
}

// NewModel builds a proposer against an OpenAI-compatible endpoint.
func NewModel(config Config) *Model {
	return &Model{
		config: config,
		// A timeout on the client as well as the context. A request with no deadline at either
		// level is one hung connection away from a formation worker that stops making progress and
		// reports nothing wrong.
		client: &http.Client{Timeout: 2 * time.Minute},
	}
}

// Propose asks the model what one message asserts.
//
// # No retry here
//
// A rate limit and a transient failure are both real and both are the caller's to handle: the caller
// is the one that knows whether this message is being formed in a batch that can be paused or on a
// path a customer is waiting on. A retry buried here would make the two indistinguishable and would
// hold a connection open through a backoff nobody chose.
func (m *Model) Propose(ctx context.Context, message domain.Message, vocabulary domain.Ontology) ([]extract.Proposal, error) {
	body, err := json.Marshal(chatRequest{
		Model: m.config.Model,
		// Zero, because extraction is not a creative task. Two extractions of one message should
		// agree, and every disagreement is either a fact that appears once or one that disappears —
		// neither of which anybody can debug.
		Temperature: 0,
		// The model is asked for JSON by the interface as well as by the prompt. A model told only
		// in prose returns prose about a third of the time, and every one of those is a parse
		// failure standing in for an extraction failure.
		ResponseFormat: &responseFormat{Type: "json_object"},
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt(vocabulary)},
			{Role: "user", Content: userPrompt(message)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.config.Endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.config.APIKey)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call model: %w", err)
	}
	defer resp.Body.Close()

	// Read before checking the status: a provider's error message is in the body, and a bare "429"
	// with the explanation discarded is the least useful thing a log can contain.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read model response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode model response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("model returned no choices")
	}
	return parseProposals(decoded.Choices[0].Message.Content)
}

// parseProposals reads the model's JSON.
//
// A malformed body is an ERROR rather than an empty result. An empty result means "this message
// asserts nothing", which is the ordinary case for most messages — so returning it for a broken
// response would make a misconfigured model indistinguishable from a quiet conversation, and memory
// would simply stop forming with nothing reporting a problem.
func parseProposals(content string) ([]extract.Proposal, error) {
	content = strings.TrimSpace(content)
	// Fenced output survives JSON mode often enough to be worth handling: the alternative is
	// discarding a correct extraction over three backticks.
	if fenced := strings.Index(content, "```"); fenced >= 0 {
		rest := content[fenced+3:]
		if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
			rest = rest[newline+1:]
		}
		if closing := strings.Index(rest, "```"); closing >= 0 {
			content = strings.TrimSpace(rest[:closing])
		}
	}

	var envelope struct {
		Claims []struct {
			Subject     string  `json:"subject"`
			Predicate   string  `json:"predicate"`
			Object      string  `json:"object"`
			Statement   string  `json:"statement"`
			Confidence  float32 `json:"confidence"`
			Quote       string  `json:"quote"`
			SubjectType string  `json:"subject_type"`
			ObjectType  string  `json:"object_type"`
		} `json:"claims"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return nil, fmt.Errorf("model did not return the requested JSON: %w (%.200s)", err, content)
	}

	proposals := make([]extract.Proposal, 0, len(envelope.Claims))
	for _, c := range envelope.Claims {
		proposals = append(proposals, extract.Proposal{
			Subject:     c.Subject,
			Predicate:   c.Predicate,
			Object:      c.Object,
			Statement:   c.Statement,
			Confidence:  c.Confidence,
			Quote:       c.Quote,
			SubjectType: c.SubjectType,
			ObjectType:  c.ObjectType,
		})
	}
	return proposals, nil
}

// systemPrompt states the vocabulary and the one rule that cannot be recovered from afterwards.
//
// The vocabulary is written out in full, with each relation's description and the kind of thing its
// object should be. Naming the relations without saying what they mean produces confident misuse —
// `part_of` for employment, `has_status` for anything — and a wrong relation is worse than an
// unmapped one, because it is admitted and nothing downstream can tell it apart from a right one.
//
// The quote rule is stated in the strongest terms available, and the extractor still verifies it.
// Both are needed: the instruction is what makes most quotes copyable, and the verification is what
// makes the ones that are not harmless.
func systemPrompt(vocabulary domain.Ontology) string {
	var b strings.Builder
	b.WriteString(`You read one message from a conversation and report what it asserts, as relations.

RELATIONS. Use only these. There is no other relation available to you, and a relation that is not
on this list will be discarded:

`)
	for _, p := range vocabulary.All() {
		fmt.Fprintf(&b, "  %-16s object is a %-13s %s\n", p.Name, p.ObjectKind, p.Description)
	}
	b.WriteString(`
THE QUOTE IS THE POINT. For every relation you report, give the exact stretch of the message that
says it — copied character for character from the message, not retyped, not tidied, not shortened
past the words that carry the claim. It is checked against the message, and anything that is not
found there is discarded along with the relation it was evidence for. A short exact quote is
always better than a long approximate one.

WHAT NOT TO REPORT. Do not report what you infer, only what the message says. Do not report a
relation because it is probably true. Most messages assert nothing at all, and an empty list is the
correct answer far more often than not.

Reply with JSON only, in this shape:

{"claims": [{"subject": "...", "predicate": "...", "object": "...", "statement": "...",
             "confidence": 0.0, "quote": "...", "subject_type": "...", "object_type": "..."}]}

  subject, object   the two ends, named as the message names them
  statement         the claim as one plain sentence a person would read
  confidence        0 to 1, how certain you are the message says this
  quote             the exact stretch of the message, copied
  subject_type      person, organisation, place, time, value or thing; omit if unsure

If the message asserts nothing, reply {"claims": []}.`)
	return b.String()
}

// userPrompt carries the message and its speaker.
//
// The speaker is included because who is talking changes what a sentence asserts: "you moved to
// Dublin last month" from an assistant is a claim about the assistant's belief, not the user's
// history. What is done with the speaker afterwards is the role policy's business, on the road to
// the fact table — this is only so the model is not reading the sentence blind.
func userPrompt(message domain.Message) string {
	return fmt.Sprintf("Speaker: %s\n\nMessage:\n%s", message.Role, message.Content)
}

type chatRequest struct {
	Model          string          `json:"model"`
	Temperature    float32         `json:"temperature"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}
