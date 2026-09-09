package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// DefaultModel is used when none is configured. Override with Option or the
// ASPECT_MODEL environment variable handled by the CLI.
const DefaultModel = "claude-opus-5"

// DefaultMaxTokens is generous because agents emit whole files. Streaming
// keeps large responses clear of HTTP timeouts.
const DefaultMaxTokens int64 = 64000

// Anthropic is a Client backed by the official SDK.
type Anthropic struct {
	api       anthropic.Client
	model     string
	effort    anthropic.BetaOutputConfigEffort
	fallbacks bool
}

// Option configures the Anthropic client.
type Option func(*Anthropic)

// WithModel selects the model id (for example "claude-opus-5").
func WithModel(model string) Option { return func(a *Anthropic) { a.model = model } }

// WithEffort sets output_config.effort: low, medium, high, xhigh or max.
func WithEffort(effort string) Option {
	return func(a *Anthropic) { a.effort = anthropic.BetaOutputConfigEffort(effort) }
}

// WithFallbacks toggles server-side refusal fallbacks. Enabled by default so a
// safety-classifier refusal is re-served by a fallback model in the same call
// instead of aborting a long pipeline run.
func WithFallbacks(on bool) Option { return func(a *Anthropic) { a.fallbacks = on } }

// WithAPIKey overrides credential discovery (ANTHROPIC_API_KEY, ant auth profiles).
func WithAPIKey(key string) Option {
	return func(a *Anthropic) { a.api = anthropic.NewClient(option.WithAPIKey(key)) }
}

// NewAnthropic builds a client. Credentials come from the SDK's normal chain.
func NewAnthropic(opts ...Option) *Anthropic {
	a := &Anthropic{
		api:       anthropic.NewClient(),
		model:     DefaultModel,
		effort:    anthropic.BetaOutputConfigEffortHigh,
		fallbacks: true,
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Model reports the configured model id.
func (a *Anthropic) Model() string { return a.model }

// Complete streams one completion and returns the accumulated text.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: maxTokens,
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.Prompt)),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: a.effort},
	}
	if req.System != "" {
		params.System = []anthropic.BetaTextBlockParam{{
			Text:         req.System,
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}}
	}
	if req.Schema != nil {
		params.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: req.Schema}
	}
	if a.fallbacks {
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}

	stream := a.api.Beta.Messages.NewStreaming(ctx, params)
	msg := anthropic.BetaMessage{}
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return Response{}, fmt.Errorf("llm: accumulate stream: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, classify(err)
	}

	resp := Response{
		Model:           string(msg.Model),
		StopReason:      string(msg.StopReason),
		InputTokens:     msg.Usage.InputTokens,
		OutputTokens:    msg.Usage.OutputTokens,
		CacheReadTokens: msg.Usage.CacheReadInputTokens,
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	resp.Text = text.String()

	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return resp, fmt.Errorf("llm: model refused (category %q): %s",
			msg.StopDetails.Category, msg.StopDetails.Explanation)
	case anthropic.BetaStopReasonMaxTokens:
		return resp, fmt.Errorf("llm: response truncated at %d tokens; raise MaxTokens", maxTokens)
	}
	return resp, nil
}

// classify turns SDK errors into messages that say what to do next.
func classify(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return fmt.Errorf("llm: authentication failed; set ANTHROPIC_API_KEY or run `ant auth login`: %w", err)
		case 404:
			return fmt.Errorf("llm: model not found; check the -model flag: %w", err)
		case 429:
			return fmt.Errorf("llm: rate limited after SDK retries; slow down or retry later: %w", err)
		default:
			return fmt.Errorf("llm: API error %d (request %s): %w", apiErr.StatusCode, apiErr.RequestID, err)
		}
	}
	return fmt.Errorf("llm: transport error: %w", err)
}
