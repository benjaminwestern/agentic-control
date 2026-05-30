package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/benjaminwestern/agentic-control/pkg/contract"
	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	sigmaevals "github.com/benjaminwestern/sigma-evals"
	"github.com/wintermi/sigma"
)

type controlPlaneSigmaCompleter struct {
	controller FanoutController
}

func (c controlPlaneSigmaCompleter) CompleteTarget(ctx context.Context, request sigmaevals.TargetRequest) (sigmaevals.TargetResult, error) {
	result := sigmaevals.TargetResult{
		Target:   request.Target,
		Request:  request.Request,
		Repeat:   request.Repeat,
		Metadata: request.Metadata,
	}
	if c.controller == nil {
		result.Error = "control plane is required"
		return result, fmt.Errorf("%s", result.Error)
	}
	provider, model := sigmaEvalTargetProviderModel(request.Target)
	if provider == "" || model == "" {
		result.Error = "target provider and model are required"
		return result, fmt.Errorf("%s", result.Error)
	}
	prompt := sigmaRequestPrompt(request.Request)
	if strings.TrimSpace(prompt) == "" {
		result.Error = "sigma request rendered an empty prompt"
		return result, fmt.Errorf("%s", result.Error)
	}

	modelOptions := modelOptionsFromSigmaOptions(appliedSigmaOptions(request.Options))

	fanout, err := RunFanout(ctx, c.controller, FanoutOptions{
		Prompt: prompt,
		Targets: []FanoutTarget{{
			Backend: provider,
			Model:   model,
			Label:   request.Target.Label,
			Options: modelOptions,
		}},
		Metadata: map[string]any{
			"workflow":      "sigma_eval",
			"workflow_mode": "model_call",
			"thread_kind":   "evaluation_model_call",
		},
		EventBuffer: 1024,
	})
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	if len(fanout.Targets) == 0 {
		result.Error = "sigma eval model call produced no target result"
		return result, fmt.Errorf("%s", result.Error)
	}
	target := fanout.Targets[0]
	metadata := map[string]any{
		"session_id":           targetSessionID(target),
		"cost_usd":             target.RecordedCostUSD,
		"event_count":          target.EventCount,
		"runtime_backend":      target.Target.Backend,
		"runtime_model":        target.Target.Model,
		"runtime_target_label": target.Target.Label,
	}
	if target.Session != nil {
		metadata["provider_session_id"] = target.Session.Session.ProviderSessionID
	}
	logprobs := sigmaEvalTokenLogprobs(target.Logprobs)
	if len(logprobs) > 0 {
		metadata["logprobs"] = logprobs
	}
	message := sigma.AssistantMessage{
		Content:          []sigma.ContentBlock{sigma.Text(target.Text)},
		Model:            sigma.ModelID(target.Target.Model),
		Provider:         sigma.ProviderID(target.Target.Backend),
		Usage:            sigmaUsage(target.RecordedUsage),
		Cost:             sigmaCost(target.RecordedCostUSD),
		ProviderMetadata: metadata,
	}
	result.Output = target.Text
	result.Message = message
	result.Usage = message.Usage
	result.Cost = message.Cost
	result.ProviderMetadata = metadata
	result.Logprobs = logprobs
	if target.Error != "" {
		result.Error = target.Error
		return result, fmt.Errorf("%s", target.Error)
	}
	return result, nil
}

func sigmaRequestPrompt(request sigma.Request) string {
	if strings.TrimSpace(request.SystemPrompt) == "" && len(request.Messages) == 1 && request.Messages[0].Role == sigma.RoleUser {
		return strings.TrimSpace(sigmaMessagePlainText(request.Messages[0]))
	}
	var b strings.Builder
	if strings.TrimSpace(request.SystemPrompt) != "" {
		b.WriteString("System:\n")
		b.WriteString(strings.TrimSpace(request.SystemPrompt))
		b.WriteString("\n\n")
	}
	for _, message := range request.Messages {
		text := strings.TrimSpace(sigmaMessagePlainText(message))
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sigmaRoleLabel(message.Role))
		b.WriteString(":\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func sigmaRoleLabel(role sigma.Role) string {
	text := strings.TrimSpace(string(role))
	if text == "" {
		return "Message"
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func sigmaMessagePlainText(message sigma.Message) string {
	var parts []string
	for _, block := range message.Content {
		switch block.Type {
		case sigma.ContentBlockText:
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		case sigma.ContentBlockThinking:
			if strings.TrimSpace(block.ThinkingText) != "" {
				parts = append(parts, "[thinking] "+block.ThinkingText)
			}
		case sigma.ContentBlockImage:
			description := strings.TrimSpace(block.URL)
			if description == "" {
				description = strings.TrimSpace(block.MIMEType)
			}
			if description == "" {
				description = "image"
			}
			parts = append(parts, "[image: "+description+"]")
		case sigma.ContentBlockToolCall:
			encoded, _ := json.Marshal(block.ToolArguments)
			parts = append(parts, fmt.Sprintf("[tool_call %s %s]", block.ToolName, string(encoded)))
		}
	}
	return strings.Join(parts, "\n")
}

func appliedSigmaOptions(opts []sigma.Option) sigma.Options {
	var options sigma.Options
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}

func modelOptionsFromSigmaOptions(options sigma.Options) api.ModelOptions {
	out := api.ModelOptions{}
	if options.MaxTokens != nil {
		out.MaxOutputTokens = *options.MaxTokens
	}
	if options.Temperature != nil {
		out.Temperature = options.Temperature
	}
	if level := strings.TrimSpace(string(options.ReasoningLevel)); level != "" && level != string(sigma.ThinkingLevelOff) {
		out.ReasoningEffort = level
	}
	if options.ThinkingBudgetTokens != nil {
		out.ThinkingBudget = options.ThinkingBudgetTokens
	}
	if options.OpenAIOptions != nil {
		if level := strings.TrimSpace(string(options.OpenAIOptions.ReasoningEffort)); level != "" && level != string(sigma.ThinkingLevelOff) {
			out.ReasoningEffort = level
		}
		if schema := responseSchemaFromOpenAIResponseFormat(options.OpenAIOptions.ResponseFormat); schema != nil {
			out.ResponseSchema = schema
		}
		if options.OpenAIOptions.TopLogprobs > 0 {
			out.Logprobs = true
			out.TopLogprobs = options.OpenAIOptions.TopLogprobs
		}
	}
	return out
}

func responseSchemaFromOpenAIResponseFormat(value any) map[string]any {
	format := sigmaAnyMap(value)
	if format == nil || format["type"] != "json_schema" {
		return nil
	}
	jsonSchema := sigmaAnyMap(format["json_schema"])
	if jsonSchema == nil {
		return nil
	}
	return sigmaAnyMap(jsonSchema["schema"])
}

func sigmaAnyMap(value any) map[string]any {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal(encoded, &out); err != nil {
			return nil
		}
		return out
	}
}

func sigmaUsage(usage contract.TokenUsage) *sigma.Usage {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 && usage.ReasoningTokens == 0 && usage.CachedTokens == 0 {
		return nil
	}
	return &sigma.Usage{
		InputTokens:          int(usage.InputTokens),
		OutputTokens:         int(usage.OutputTokens),
		TotalTokens:          int(usage.TotalTokens),
		ThinkingTokens:       int(usage.ReasoningTokens),
		CacheReadInputTokens: int(usage.CachedTokens),
	}
}

func sigmaCost(costUSD float64) *sigma.Cost {
	if costUSD == 0 {
		return nil
	}
	return &sigma.Cost{TotalCost: costUSD, Currency: "USD"}
}

func sigmaEvalTokenLogprobs(items []contract.TokenLogprob) []sigmaevals.TokenLogprob {
	if len(items) == 0 {
		return nil
	}
	out := make([]sigmaevals.TokenLogprob, 0, len(items))
	for _, item := range items {
		out = append(out, sigmaevals.TokenLogprob{
			Token:       item.Token,
			Logprob:     item.Logprob,
			Bytes:       item.Bytes,
			TopLogprobs: sigmaEvalTokenLogprobs(item.TopLogprobs),
		})
	}
	return out
}

func sigmaMessageCostUSD(messages ...sigma.AssistantMessage) float64 {
	var total float64
	for _, message := range messages {
		if message.Cost != nil {
			total += message.Cost.TotalCost
			continue
		}
		if value, ok := message.ProviderMetadata["cost_usd"].(float64); ok {
			total += value
		}
	}
	return total
}

func targetSessionID(result FanoutTargetResult) string {
	if result.Session == nil {
		return ""
	}
	return result.Session.Session.SessionID
}

func sigmaEvalTargetProviderModel(target sigmaevals.Target) (string, string) {
	provider := strings.TrimSpace(string(target.Provider))
	model := strings.TrimSpace(string(target.ModelID))
	if target.ModelConfig != nil {
		if provider == "" {
			provider = strings.TrimSpace(string(target.ModelConfig.Provider))
		}
		if model == "" {
			model = strings.TrimSpace(string(target.ModelConfig.ID))
		}
	}
	return provider, model
}

func sigmaEvalTargetFromEvalTarget(raw string) sigmaevals.Target {
	target := parseEvalTarget(raw)
	return sigmaevals.Target{
		Provider: sigma.ProviderID(target.Backend),
		ModelID:  sigma.ModelID(target.Model),
		Label:    firstNonEmptyValue(target.Label, target.Model),
	}
}

func sigmaEvalMode(mode ReductionMode) sigmaevals.Mode {
	switch mode {
	case ReductionModeGEval:
		return sigmaevals.ModeGEval
	default:
		return sigmaevals.ModeEvaluate
	}
}

func rubricPrompt(raw string) string {
	return GetRubric(raw)
}
