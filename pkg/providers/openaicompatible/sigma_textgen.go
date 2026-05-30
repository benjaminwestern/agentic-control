package openaicompatible

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/benjaminwestern/agentic-control/pkg/contract"
	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	"github.com/wintermi/sigma"
	sigmaopenai "github.com/wintermi/sigma/provider/openai"
)

// GenerateTextWithSigma adapts Agentic Control's text-generation contract to
// Sigma's provider-neutral AI core. The session/runtime providers still own
// long-lived agent processes; this path is for direct model calls.
func GenerateTextWithSigma(ctx context.Context, endpoint EndpointConfig, input api.GenerateTextInput) (*api.GenerateTextOutput, error) {
	started := time.Now()
	input = api.GenerateTextInputWithMedia(input)
	endpoint = resolveSigmaEndpoint(endpoint, input.ModelSelection)

	modelID := strings.TrimSpace(input.ModelSelection.Model)
	if modelID == "" {
		modelID = "ollama"
	}
	providerID := sigma.ProviderID(endpoint.providerName())

	request, err := sigmaRequestFromGenerateTextInput(input)
	if err != nil {
		result := sigmaProviderResultFromError(endpoint, modelID, started, err)
		result.OutputKind = "invalid_request"
		return nil, api.NewProviderResultError("sigma text generation input is invalid", result, err)
	}

	registry := sigma.NewRegistry()
	providerOptions := []sigmaopenai.ProviderOption{sigmaopenai.WithBaseURL(endpoint.BaseURL)}
	if endpoint.HTTPClient != nil {
		providerOptions = append(providerOptions, sigmaopenai.WithHTTPClient(endpoint.HTTPClient))
	}
	if err := sigmaopenai.Register(registry, providerID, providerOptions...); err != nil {
		result := sigmaProviderResultFromError(endpoint, modelID, started, err)
		result.OutputKind = "registry_error"
		return nil, api.NewProviderResultError("sigma text provider registration failed", result, err)
	}
	model := sigma.OpenAICompatibleModel(sigma.OpenAICompatibleModelConfig{
		ID:              sigma.ModelID(modelID),
		Provider:        providerID,
		BaseURL:         endpoint.BaseURL,
		Name:            modelID,
		SupportedInputs: []sigma.ContentBlockType{sigma.ContentBlockText, sigma.ContentBlockImage},
		SupportsTools:   len(input.Tools) > 0,
		SupportsThinking: strings.TrimSpace(input.ModelSelection.Options.ReasoningEffort) != "" ||
			strings.TrimSpace(input.ModelSelection.Options.ThinkingLevel) != "",
	})
	if err := registry.RegisterModel(model); err != nil {
		result := sigmaProviderResultFromError(endpoint, modelID, started, err)
		result.OutputKind = "registry_error"
		return nil, api.NewProviderResultError("sigma model registration failed", result, err)
	}

	client := sigma.NewClient(
		sigma.WithRegistry(registry),
		sigma.WithHTTPClient(endpoint.HTTPClient),
		sigma.WithAuthResolver(endpointSigmaAuthResolver{endpoint: endpoint}),
	)
	message, err := client.Complete(ctx, model, request, sigmaOptionsFromGenerateTextInput(providerID, input)...)
	if err != nil {
		result := sigmaProviderResultFromMessage(endpoint, modelID, started, message)
		if result.Model == "" {
			result.Model = modelID
		}
		result.Error = sigmaProviderError(err)
		result.OutputKind = "provider_error"
		return nil, api.NewProviderResultError("sigma text generation failed", result, err)
	}

	text, err := sigmaAssistantText(message)
	result := sigmaProviderResultFromMessage(endpoint, modelID, started, message)
	if err != nil {
		result.OutputKind = "empty_content"
		result.Error = &api.ProviderError{Kind: "empty_content", Message: err.Error()}
		return nil, api.NewProviderResultError("sigma text generation returned empty final content", result, err)
	}
	result.OutputKind = "text"
	metadata := providerMetadata(result)
	for key, value := range message.ProviderMetadata {
		metadata[key] = value
	}
	if message.Cost != nil {
		metadata["cost"] = message.Cost
	}
	if logprobs := sigmaControlPlaneLogprobs(message.ProviderMetadata); len(logprobs) > 0 {
		metadata["logprobs"] = logprobs
	}

	return &api.GenerateTextOutput{
		Text:           text,
		Metadata:       metadata,
		Logprobs:       sigmaControlPlaneLogprobs(message.ProviderMetadata),
		ProviderResult: result,
	}, nil
}

func resolveSigmaEndpoint(endpoint EndpointConfig, selection api.TextGenerationModelSelection) EndpointConfig {
	if strings.TrimSpace(endpoint.Provider) != "" && strings.TrimSpace(endpoint.BaseURL) != "" {
		return endpoint
	}
	resolved := ResolveEndpointConfig(EndpointResolutionInput{
		Provider:  firstNonEmpty(endpoint.Provider, selection.Provider),
		Model:     selection.Model,
		BaseURL:   firstNonEmpty(endpoint.BaseURL, selection.Options.BaseURL),
		APIKeyEnv: firstNonEmpty(endpoint.APIKeyEnv, selection.Options.APIKeyEnv),
		APIKey:    firstNonEmpty(endpoint.APIKey, selection.Options.APIKey),
	})
	resolved.OAuthTokenURL = firstNonEmpty(endpoint.OAuthTokenURL, selection.Options.OAuthTokenURL)
	resolved.OAuthClientID = firstNonEmpty(endpoint.OAuthClientID, selection.Options.OAuthClientID)
	resolved.OAuthClientSecret = firstNonEmpty(endpoint.OAuthClientSecret, selection.Options.OAuthClientSecret)
	resolved.HTTPClient = endpoint.HTTPClient
	resolved.Timeout = endpoint.Timeout
	resolved.RetryPolicy = endpoint.RetryPolicy
	return resolved
}

func sigmaRequestFromGenerateTextInput(input api.GenerateTextInput) (sigma.Request, error) {
	request := sigma.Request{
		SystemPrompt: strings.TrimSpace(input.SystemPrompt),
		Tools:        sigmaTools(input.Tools),
	}
	if len(input.Messages) == 0 {
		if strings.TrimSpace(input.Prompt) != "" {
			request.Messages = []sigma.Message{sigma.UserText(input.Prompt)}
		}
		return request, nil
	}

	var systemPrompts []string
	for _, message := range input.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "system" {
			if text := sigmaMessageText(message); text != "" {
				systemPrompts = append(systemPrompts, text)
			}
			continue
		}
		converted, err := sigmaMessage(message)
		if err != nil {
			return sigma.Request{}, err
		}
		request.Messages = append(request.Messages, converted)
	}
	if len(systemPrompts) > 0 {
		if request.SystemPrompt != "" {
			systemPrompts = append([]string{request.SystemPrompt}, systemPrompts...)
		}
		request.SystemPrompt = strings.Join(systemPrompts, "\n\n")
	}
	return request, nil
}

func sigmaMessage(message api.Message) (sigma.Message, error) {
	blocks, err := sigmaContentBlocks(message.Content, message.Parts)
	if err != nil {
		return sigma.Message{}, err
	}
	role := sigma.RoleUser
	switch strings.ToLower(strings.TrimSpace(message.Role)) {
	case "", "user":
		role = sigma.RoleUser
	case "developer":
		role = sigma.RoleDeveloper
	case "assistant":
		role = sigma.RoleAssistant
		for _, call := range message.ToolCalls {
			blocks = append(blocks, sigma.ToolCallBlock(call.ID, call.Function.Name, jsonArguments(call.Function.Arguments)))
		}
	case "tool":
		role = sigma.RoleTool
	default:
		return sigma.Message{}, fmt.Errorf("unsupported message role %q", message.Role)
	}
	return sigma.Message{
		Role:       role,
		Content:    blocks,
		ToolCallID: message.ToolCallID,
		ToolName:   message.Name,
	}, nil
}

func sigmaContentBlocks(content any, parts []contract.ContentPart) ([]sigma.ContentBlock, error) {
	blocks := make([]sigma.ContentBlock, 0, len(parts)+1)
	if text := contentText(content); strings.TrimSpace(text) != "" {
		blocks = append(blocks, sigma.Text(text))
	}
	for _, part := range parts {
		switch part.Type {
		case contract.ContentPartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				blocks = append(blocks, sigma.Text(part.Text))
			}
		case contract.ContentPartTypeImage:
			mime := strings.TrimSpace(part.MIMEType)
			if mime == "" {
				mime = "image/jpeg"
			}
			if strings.TrimSpace(part.URL) != "" {
				blocks = append(blocks, sigma.ImageURL(mime, part.URL))
			} else if strings.TrimSpace(part.Data) != "" {
				blocks = append(blocks, sigma.ImageBase64(mime, part.Data))
			}
		default:
			return nil, fmt.Errorf("unsupported content part type %q", part.Type)
		}
	}
	return blocks, nil
}

func sigmaMessageText(message api.Message) string {
	blocks, err := sigmaContentBlocks(message.Content, message.Parts)
	if err != nil {
		return ""
	}
	var values []string
	for _, block := range blocks {
		if block.Type == sigma.ContentBlockText && strings.TrimSpace(block.Text) != "" {
			values = append(values, block.Text)
		}
	}
	return strings.Join(values, "\n")
}

func contentText(content any) string {
	switch value := content.(type) {
	case nil:
		return ""
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
}

func sigmaTools(tools []api.ToolDefinition) []sigma.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]sigma.Tool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		out = append(out, sigma.Tool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	return out
}

func jsonArguments(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return decoded
	}
	return raw
}

func sigmaOptionsFromGenerateTextInput(provider sigma.ProviderID, input api.GenerateTextInput) []sigma.Option {
	modelOptions := input.ModelSelection.Options
	options := make([]sigma.Option, 0, 6)
	if modelOptions.MaxOutputTokens > 0 {
		options = append(options, sigma.WithMaxTokens(modelOptions.MaxOutputTokens))
	}
	if modelOptions.Temperature != nil {
		options = append(options, sigma.WithTemperature(*modelOptions.Temperature))
	}
	if level := firstNonEmpty(modelOptions.ThinkingLevel, modelOptions.ReasoningEffort); strings.TrimSpace(level) != "" {
		options = append(options, sigma.WithReasoningLevel(sigma.ThinkingLevel(level)))
	}
	if modelOptions.ThinkingBudget != nil {
		options = append(options, sigma.WithThinkingBudgetTokens(*modelOptions.ThinkingBudget))
	}
	if strings.TrimSpace(modelOptions.APIKey) != "" {
		options = append(options, sigma.WithAPIKey(modelOptions.APIKey))
	}
	if len(input.Metadata) > 0 {
		options = append(options, sigma.WithMetadata(input.Metadata))
	}

	extraBody := map[string]any{}
	if modelOptions.TopP != nil {
		extraBody["top_p"] = *modelOptions.TopP
	}
	if strings.TrimSpace(modelOptions.ReasoningEffort) != "" {
		extraBody["reasoning_effort"] = modelOptions.ReasoningEffort
	}
	if modelOptions.Logprobs {
		extraBody["logprobs"] = true
		if modelOptions.TopLogprobs > 0 {
			extraBody["top_logprobs"] = modelOptions.TopLogprobs
		}
	}
	if input.ToolChoice != nil {
		extraBody["tool_choice"] = input.ToolChoice
	}
	if responseFormat := sigmaResponseFormat(input); responseFormat != nil {
		extraBody["response_format"] = responseFormat
	}

	providerOptions := map[string]any{"include_usage": true}
	if len(extraBody) > 0 {
		providerOptions["extra_body"] = extraBody
	}
	options = append(options, sigma.WithProviderOptions(provider, providerOptions))
	return options
}

func sigmaResponseFormat(input api.GenerateTextInput) map[string]any {
	if input.ModelSelection.Options.ResponseSchema != nil {
		return map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "structured_output",
				"strict": true,
				"schema": input.ModelSelection.Options.ResponseSchema,
			},
		}
	}
	switch input.ResponseFormat {
	case "json", "json_object":
		return map[string]any{"type": "json_object"}
	case "text":
		return map[string]any{"type": "text"}
	default:
		return nil
	}
}

type endpointSigmaAuthResolver struct {
	endpoint EndpointConfig
}

func (r endpointSigmaAuthResolver) Resolve(_ context.Context, _ sigma.Model, _ sigma.Options) (sigma.Credential, error) {
	if value := strings.TrimSpace(r.endpoint.APIKey); value != "" {
		return sigma.Credential{Type: sigma.CredentialTypeAPIKey, Value: value, Source: "endpoint:api-key"}, nil
	}
	if env := strings.TrimSpace(r.endpoint.APIKeyEnv); env != "" {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			return sigma.Credential{Type: sigma.CredentialTypeAPIKey, Value: value, Source: "env:" + env}, nil
		}
	}
	return sigma.Credential{}, nil
}

func sigmaAssistantText(message sigma.AssistantMessage) (string, error) {
	var parts []string
	for _, block := range message.Content {
		if block.Type == sigma.ContentBlockText && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, ""))
	if text == "" {
		return "", fmt.Errorf("assistant message contains no final text")
	}
	return text, nil
}

func sigmaProviderResultFromMessage(endpoint EndpointConfig, fallbackModel string, started time.Time, message sigma.AssistantMessage) api.ProviderResultMetadata {
	elapsed := time.Since(started)
	result := api.ProviderResultMetadata{
		Provider:      firstNonEmpty(string(message.Provider), endpoint.providerName()),
		Model:         firstNonEmpty(string(message.Model), fallbackModel),
		BaseURL:       endpoint.BaseURL,
		RequestCount:  1,
		LatencyMillis: elapsed.Milliseconds(),
		LatencyNanos:  elapsed.Nanoseconds(),
		FinishReason:  string(message.StopReason),
		RequestID:     stringFromMap(message.ProviderMetadata, "id"),
		Usage:         sigmaProviderUsage(message.Usage),
	}
	return result
}

func sigmaProviderResultFromError(endpoint EndpointConfig, fallbackModel string, started time.Time, err error) api.ProviderResultMetadata {
	elapsed := time.Since(started)
	return api.ProviderResultMetadata{
		Provider:      endpoint.providerName(),
		Model:         fallbackModel,
		BaseURL:       endpoint.BaseURL,
		RequestCount:  1,
		LatencyMillis: elapsed.Milliseconds(),
		LatencyNanos:  elapsed.Nanoseconds(),
		OutputKind:    "provider_error",
		Error:         sigmaProviderError(err),
	}
}

func sigmaProviderUsage(usage *sigma.Usage) api.ProviderUsage {
	if usage == nil {
		return api.ProviderUsage{}
	}
	return api.ProviderUsage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

func sigmaProviderError(err error) *api.ProviderError {
	if err == nil {
		return nil
	}
	var providerErr *sigma.ProviderError
	if errors.As(err, &providerErr) {
		return &api.ProviderError{
			Kind:       "provider_error",
			Message:    providerErr.Error(),
			StatusCode: providerErr.StatusCode,
			Body:       providerErr.BodyPreview,
			Retryable:  providerErr.RetryAfter > 0,
		}
	}
	return &api.ProviderError{Kind: "provider_error", Message: err.Error()}
}

func sigmaControlPlaneLogprobs(metadata map[string]any) []api.TokenLogprob {
	if len(metadata) == 0 || metadata["logprobs"] == nil {
		return nil
	}
	encoded, err := json.Marshal(metadata["logprobs"])
	if err != nil {
		return nil
	}
	var envelope struct {
		Content []api.TokenLogprob `json:"content"`
	}
	if err := json.Unmarshal(encoded, &envelope); err == nil && len(envelope.Content) > 0 {
		return envelope.Content
	}
	var direct []api.TokenLogprob
	if err := json.Unmarshal(encoded, &direct); err == nil && len(direct) > 0 {
		return direct
	}
	return nil
}

func stringFromMap(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}
