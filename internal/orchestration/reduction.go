package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/benjaminwestern/agentic-control/pkg/contract"
	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	sigmaevals "github.com/benjaminwestern/sigma-evals"
)

type ReductionMode string

const (
	ReductionModeCompare   ReductionMode = "compare"
	ReductionModeSummarize ReductionMode = "summarize"
	ReductionModeBestOfN   ReductionMode = "best_of_n"
	ReductionModeEvaluate  ReductionMode = "evaluate"
	ReductionModeGEval     ReductionMode = "g_eval"
)

type ReductionResult struct {
	Mode            ReductionMode            `json:"mode"`
	Target          FanoutTarget             `json:"target"`
	Session         *contract.TrackedSession `json:"session,omitempty"`
	Text            string                   `json:"text,omitempty"`
	JSON            string                   `json:"json,omitempty"`
	Error           string                   `json:"error,omitempty"`
	Stopped         bool                     `json:"stopped,omitempty"`
	StopError       string                   `json:"stop_error,omitempty"`
	RecordedUsage   contract.TokenUsage      `json:"recorded_usage,omitempty"`
	RecordedCostUSD float64                  `json:"recorded_cost_usd,omitempty"`
	Logprobs        []contract.TokenLogprob  `json:"logprobs,omitempty"`
}

type ReviewedFanoutResult struct {
	Fanout       FanoutResult        `json:"fanout"`
	Reduction    ReductionResult     `json:"reduction"`
	TotalUsage   contract.TokenUsage `json:"total_usage,omitempty"`
	TotalCostUSD float64             `json:"total_cost_usd,omitempty"`
}

type ReviewedFanoutOptions struct {
	Fanout          FanoutOptions
	Mode            ReductionMode
	ReductionTarget FanoutTarget
}

func RunReviewedFanout(ctx context.Context, controller FanoutController, options ReviewedFanoutOptions) (ReviewedFanoutResult, error) {
	fanout, err := RunFanout(ctx, controller, options.Fanout)
	if err != nil {
		return ReviewedFanoutResult{}, err
	}
	reduction, err := RunReduction(ctx, controller, options.Mode, fanout, options.ReductionTarget, options.Fanout.KeepSessions)
	if err != nil {
		return ReviewedFanoutResult{}, err
	}
	return ReviewedFanoutResult{
		Fanout:       fanout,
		Reduction:    reduction,
		TotalUsage:   addUsage(fanout.TotalUsage, reduction.RecordedUsage),
		TotalCostUSD: fanout.TotalCostUSD + reduction.RecordedCostUSD,
	}, nil
}

func RunReduction(ctx context.Context, controller FanoutController, mode ReductionMode, fanout FanoutResult, target FanoutTarget, keepSession bool) (ReductionResult, error) {
	if mode == "" {
		return ReductionResult{}, fmt.Errorf("reduction mode is required")
	}
	resolved, err := resolveReductionTarget(controller.Describe().Runtimes, target)
	if err != nil {
		return ReductionResult{}, err
	}
	var responseSchema map[string]any
	switch mode {
	case ReductionModeCompare:
		responseSchema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary":        map[string]any{"type": "string"},
				"comparison":     map[string]any{"type": "string"},
				"recommendation": map[string]any{"type": "string"},
				"ranked_labels":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required":             []string{"summary", "comparison", "recommendation", "ranked_labels"},
			"additionalProperties": false,
		}
	case ReductionModeSummarize:
		responseSchema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary":    map[string]any{"type": "string"},
				"synthesis":  map[string]any{"type": "string"},
				"highlights": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required":             []string{"summary", "synthesis", "highlights"},
			"additionalProperties": false,
		}
	case ReductionModeBestOfN:
		responseSchema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary":               map[string]any{"type": "string"},
				"winner_label":          map[string]any{"type": "string"},
				"rationale":             map[string]any{"type": "string"},
				"recommended_next_step": map[string]any{"type": "string"},
			},
			"required":             []string{"summary", "winner_label", "rationale", "recommended_next_step"},
			"additionalProperties": false,
		}
	case ReductionModeEvaluate:
		responseSchema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"score":     map[string]any{"type": "number"},
				"rationale": map[string]any{"type": "string"},
				"passed":    map[string]any{"type": "boolean"},
			},
			"required":             []string{"score", "rationale", "passed"},
			"additionalProperties": false,
		}
	}

	if mode == ReductionModeGEval {
		resolved.Options.Logprobs = true
		resolved.Options.TopLogprobs = 5
	}

	result, err := api.RunStructuredSession(ctx, controller, resolved.Backend, api.StartSessionRequest{
		SessionID:    "reduce-" + randomFanoutID(),
		Model:        resolved.Model,
		ModelOptions: resolved.Options,
		Prompt:       reductionPrompt(mode, fanout),
		Metadata: map[string]any{
			"thread_name":    "reducer",
			"thread_kind":    "orchestration_reducer",
			"workflow":       "fanout_reduce",
			"workflow_mode":  string(mode),
			"reduction_mode": string(mode),
		},
		ResponseSchema: responseSchema,
	}, api.StructuredSessionOptions{
		Extract:        reductionExtractor(mode),
		RepairPrompt:   reductionRepairPrompt(mode),
		RepairMetadata: api.MetadataForNoToolTurn(map[string]any{"workflow": "fanout_reduce", "reduction_mode": string(mode)}),
		MaxRepairTurns: 1,
	})
	output := ReductionResult{Mode: mode, Target: resolved}
	if err != nil {
		output.Error = err.Error()
		return output, nil
	}
	output.Text = result.Text
	output.JSON = result.JSON
	output.Logprobs = result.Logprobs
	if tracked, err := controller.GetTrackedSession(ctx, result.Session.SessionID, result.Session.ProviderSessionID); err == nil {
		output.Session = tracked
		output.RecordedUsage = tracked.Session.Usage
		output.RecordedCostUSD = tracked.Session.CostUSD
	}

	if mode == ReductionModeGEval {
		score, ok := gEvalScoreFromLogprobs(output.Logprobs)
		if !ok {
			output.Error = "g-eval requires provider logprobs for score tokens 1-5"
			output.JSON = ""
		} else {
			syntheticJSON := map[string]any{
				"score":     score,
				"rationale": "G-Eval logarithmic probability evaluation",
				"passed":    score >= 3.0,
			}
			b, _ := json.Marshal(syntheticJSON)
			output.JSON = string(b)
		}
	}

	if keepSession {
		return output, nil
	}
	if _, err := controller.StopSession(ctx, result.Session.SessionID); err != nil {
		output.StopError = err.Error()
		return output, nil
	}
	output.Stopped = true
	if tracked, err := controller.GetTrackedSession(ctx, result.Session.SessionID, result.Session.ProviderSessionID); err == nil {
		output.Session = tracked
		output.RecordedUsage = tracked.Session.Usage
		output.RecordedCostUSD = tracked.Session.CostUSD
	}
	return output, nil
}

func gEvalScoreFromLogprobs(logprobs []contract.TokenLogprob) (float64, bool) {
	return sigmaevals.GEvalScore(sigmaEvalTokenLogprobs(logprobs))
}

func resolveReductionTarget(descriptors []contract.RuntimeDescriptor, requested FanoutTarget) (FanoutTarget, error) {
	if strings.TrimSpace(requested.Backend) == "" {
		for _, runtime := range descriptors {
			if runtime.Runtime == "opencode" && runtime.Capabilities.StartSession && runtime.Capabilities.StreamEvents && (runtime.Probe == nil || runtime.Probe.Installed) {
				return FanoutTarget{Backend: runtime.Runtime, Model: defaultRuntimeModel(runtime), Options: requested.Options, Label: "reducer"}, nil
			}
		}
	}
	targets, err := ResolveFanoutTargets(descriptors, []FanoutTarget{requested})
	if err != nil {
		return FanoutTarget{}, err
	}
	resolved := targets[0]
	if resolved.Label == "" {
		resolved.Label = "reducer"
	}
	return resolved, nil
}

func reductionPrompt(mode ReductionMode, fanout FanoutResult) string {
	var builder strings.Builder
	builder.WriteString("You are synthesizing multiple candidate outputs for the same task.\n")
	builder.WriteString("Return exactly one JSON object and no surrounding prose.\n\n")
	builder.WriteString("Task:\n")
	builder.WriteString(fanout.Prompt)
	builder.WriteString("\n\n")
	builder.WriteString("Candidates as JSON:\n")
	encoded, _ := json.MarshalIndent(fanout.Targets, "", "  ")
	builder.Write(encoded)
	builder.WriteString("\n\n")
	switch mode {
	case ReductionModeCompare:
		builder.WriteString("Return this shape:\n")
		builder.WriteString(`{"summary":"...","comparison":"...","recommendation":"...","ranked_labels":["candidate-1","candidate-2"]}`)
	case ReductionModeSummarize:
		builder.WriteString("Return this shape:\n")
		builder.WriteString(`{"summary":"...","synthesis":"...","highlights":["..."]}`)
	case ReductionModeBestOfN:
		builder.WriteString("Return this shape:\n")
		builder.WriteString(`{"summary":"...","winner_label":"candidate-1","rationale":"...","recommended_next_step":"..."}`)
	case ReductionModeEvaluate:
		builder.WriteString("Evaluate the target output against the expected ground truth or rubric.\n")
		builder.WriteString("Return this shape:\n")
		builder.WriteString(`{"score": 1.0, "rationale":"...","passed":true}`)
	case ReductionModeGEval:
		builder.WriteString("Evaluate the target output against the expected ground truth or rubric.\n")
		builder.WriteString("Output ONLY a single integer score between 1 and 5. Do not output anything else. No explanation, no JSON, just the number.")
	}
	return builder.String()
}

func reductionRepairPrompt(mode ReductionMode) string {
	return "Your previous turn did not return the required JSON object. Reply again with exactly one valid JSON object matching the requested shape and no extra prose."
}

func reductionExtractor(mode ReductionMode) api.StructuredResultExtractor {
	return func(values ...string) (string, string, error) {
		if mode == ReductionModeGEval {
			text := strings.TrimSpace(firstNonEmptyValue(values...))
			if text == "" {
				return "", "", fmt.Errorf("no output for g-eval")
			}
			return text, text, nil
		}
		return api.ExtractStructuredJSON(strings.Join(values, "\n"), func(candidate string) (string, string, error) {
			return parseReductionResult(mode, candidate)
		})
	}
}

func parseReductionResult(mode ReductionMode, candidate string) (string, string, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(candidate), &raw); err != nil {
		return "", "", fmt.Errorf("invalid json: %w", err)
	}
	switch mode {
	case ReductionModeCompare:
		if strings.TrimSpace(stringValue(raw["summary"])) == "" || strings.TrimSpace(stringValue(raw["comparison"])) == "" {
			return "", "", fmt.Errorf("missing summary or comparison")
		}
		return renderCompareResult(raw), normaliseJSON(raw), nil
	case ReductionModeSummarize:
		if strings.TrimSpace(stringValue(raw["summary"])) == "" || strings.TrimSpace(stringValue(raw["synthesis"])) == "" {
			return "", "", fmt.Errorf("missing summary or synthesis")
		}
		return renderSummarizeResult(raw), normaliseJSON(raw), nil
	case ReductionModeBestOfN:
		if strings.TrimSpace(stringValue(raw["winner_label"])) == "" || strings.TrimSpace(stringValue(raw["rationale"])) == "" {
			return "", "", fmt.Errorf("missing winner_label or rationale")
		}
		return renderBestOfNResult(raw), normaliseJSON(raw), nil
	case ReductionModeEvaluate:
		parsed, err := sigmaevals.ParseJSONJudgeResult(candidate)
		if err != nil {
			return "", "", err
		}
		values := map[string]any{
			"score":     parsed.Score,
			"rationale": parsed.Rationale,
			"passed":    parsed.Passed,
		}
		return renderEvaluateResult(values), parsed.JSON, nil
	default:
		return "", "", fmt.Errorf("unknown reduction mode")
	}
}

func renderEvaluateResult(values map[string]any) string {
	var b strings.Builder
	b.WriteString("# Evaluation Result\n\n")
	fmt.Fprintf(&b, "**Score:** %v\n", values["score"])
	fmt.Fprintf(&b, "**Passed:** %v\n\n", values["passed"])
	b.WriteString("## Rationale\n\n")
	b.WriteString(stringValue(values["rationale"]))
	return strings.TrimSpace(b.String())
}

func renderCompareResult(values map[string]any) string {
	var b strings.Builder
	b.WriteString("# Comparison\n\n")
	b.WriteString(stringValue(values["summary"]))
	b.WriteString("\n\n## Comparison\n\n")
	b.WriteString(stringValue(values["comparison"]))
	if recommendation := strings.TrimSpace(stringValue(values["recommendation"])); recommendation != "" {
		b.WriteString("\n\n## Recommendation\n\n")
		b.WriteString(recommendation)
	}
	return strings.TrimSpace(b.String())
}

func renderSummarizeResult(values map[string]any) string {
	var b strings.Builder
	b.WriteString("# Summary\n\n")
	b.WriteString(stringValue(values["summary"]))
	b.WriteString("\n\n## Synthesis\n\n")
	b.WriteString(stringValue(values["synthesis"]))
	if highlights, ok := values["highlights"].([]any); ok && len(highlights) > 0 {
		b.WriteString("\n\n## Highlights\n")
		for _, highlight := range highlights {
			text := strings.TrimSpace(stringValue(highlight))
			if text == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(text)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func renderBestOfNResult(values map[string]any) string {
	var b strings.Builder
	b.WriteString("# Best Of N\n\n")
	if summary := strings.TrimSpace(stringValue(values["summary"])); summary != "" {
		b.WriteString(summary)
		b.WriteString("\n\n")
	}
	b.WriteString("## Winner\n\n")
	b.WriteString(stringValue(values["winner_label"]))
	b.WriteString("\n\n## Rationale\n\n")
	b.WriteString(stringValue(values["rationale"]))
	if next := strings.TrimSpace(stringValue(values["recommended_next_step"])); next != "" {
		b.WriteString("\n\n## Recommended Next Step\n\n")
		b.WriteString(next)
	}
	return strings.TrimSpace(b.String())
}

func normaliseJSON(values map[string]any) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
