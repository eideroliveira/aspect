// Package llm is the only place in Aspect that talks to a model provider.
// Agents depend on the Client interface, so tests run against a fake and the
// pipeline can be exercised end to end without network access or an API key.
package llm

import "context"

// Request is one single-turn completion. Agents in Aspect are stateless: every
// call carries its whole context, which keeps runs reproducible and auditable.
type Request struct {
	// System is the agent's role prompt. It is cached across calls.
	System string
	// Prompt is the task-specific user turn.
	Prompt string
	// Schema, when set, is a JSON schema the response must conform to
	// (structured outputs). Every object in it must declare
	// additionalProperties: false.
	Schema map[string]any
	// MaxTokens caps the response. Zero uses the client default.
	MaxTokens int64
}

// Response is the model's answer plus accounting.
type Response struct {
	Text            string
	Model           string
	StopReason      string
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
}

// Usage accumulates token counts across a run.
type Usage struct {
	Calls           int   `json:"calls"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

// Add folds a response into the totals.
func (u *Usage) Add(r Response) {
	u.Calls++
	u.InputTokens += r.InputTokens
	u.OutputTokens += r.OutputTokens
	u.CacheReadTokens += r.CacheReadTokens
}

// Client completes requests.
type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
}
