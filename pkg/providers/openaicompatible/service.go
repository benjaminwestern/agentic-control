package openaicompatible

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/benjaminwestern/agentic-control/pkg/contract"
	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	"github.com/benjaminwestern/agentic-control/pkg/httpclient/openaicompat"
)

type Service struct {
	endpoint EndpointConfig
}

func NewService(endpoint EndpointConfig) *Service {
	return &Service{endpoint: endpoint}
}

func (s *Service) Endpoint() EndpointConfig {
	if s == nil {
		return EndpointConfig{}
	}
	return s.endpoint
}

func (s *Service) GenerateText(ctx context.Context, input api.GenerateTextInput) (*api.GenerateTextOutput, error) {
	started := time.Now()
	endpoint := s.endpointConfig()
	model := strings.TrimSpace(input.ModelSelection.Model)
	if model == "" {
		model = "ollama"
	}
	messages, err := openAICompatibleMessages(input)
	if err != nil {
		result := providerResultFromError(endpoint, model, started, openaicompat.ResponseMetadata{}, err)
		result.OutputKind = "invalid_request"
		return nil, api.NewProviderResultError("openai-compatible text generation input is invalid", result, err)
	}

	req := openaicompat.ChatCompletionRequest{
		Model:      model,
		Messages:   messages,
		Tools:      openAICompatibleTools(input.Tools),
		ToolChoice: input.ToolChoice,
		Stream:     false,
	}
	switch input.ResponseFormat {
	case "json", "json_object":
		req.ResponseFormat = &openaicompat.ResponseFormat{Type: "json_object"}
	case "text":
		req.ResponseFormat = &openaicompat.ResponseFormat{Type: "text"}
	}
	applyChatModelOptions(&req, input.ModelSelection.Options)

	resp, responseMetadata, err := endpoint.client().CreateChatCompletionWithMetadata(ctx, req)
	if err != nil {
		result := providerResultFromError(endpoint, model, started, responseMetadata, err)
		return nil, api.NewProviderResultError("openai-compatible text generation failed", result, err)
	}
	if len(resp.Choices) == 0 {
		result := providerResultFromResponse(endpoint, model, started, responseMetadata, resp, openaicompat.ChatCompletionChoice{})
		result.OutputKind = "no_choices"
		return nil, api.NewProviderResultError("openai-compatible text generation returned no choices", result, nil)
	}
	choice := resp.Choices[0]
	result := providerResultFromResponse(endpoint, model, started, responseMetadata, resp, choice)
	text := openaicompat.MessageContentText(choice.Message.Content)
	if strings.TrimSpace(text) == "" {
		result.OutputKind = classifyEmptyTextOutput(req, choice)
		return nil, api.NewProviderResultError("openai-compatible text generation returned empty final content", result, nil)
	}
	result.OutputKind = "text"
	metadata := providerMetadata(result)
	return &api.GenerateTextOutput{
		Text:           text,
		Metadata:       metadata,
		Logprobs:       controlplaneLogprobs(choice.Logprobs),
		ProviderResult: result,
	}, nil
}

func (s *Service) GenerateEmbeddings(ctx context.Context, input api.EmbeddingInput) (*api.EmbeddingOutput, error) {
	started := time.Now()
	endpoint := s.endpointConfig()
	model := strings.TrimSpace(input.ModelSelection.Model)
	if model == "" {
		model = "nomic-embed-text"
	}
	dimensions := input.Dimensions
	if dimensions <= 0 {
		dimensions = input.ModelSelection.Dimensions
	}

	resp, responseMetadata, err := endpoint.client().CreateEmbeddingsWithMetadata(ctx, openaicompat.EmbeddingRequest{
		Model:      model,
		Input:      input.Texts,
		Dimensions: dimensions,
	})
	if err != nil {
		result := providerResultFromError(endpoint, model, started, responseMetadata, err)
		return nil, api.NewProviderResultError("openai-compatible embedding failed", result, err)
	}

	vectors := make([][]float64, 0, len(resp.Data))
	for _, item := range resp.Data {
		vectors = append(vectors, item.Embedding)
	}
	result := api.ProviderResultMetadata{
		Provider:      endpoint.providerName(),
		Model:         firstNonEmpty(resp.Model, model),
		BaseURL:       endpoint.BaseURL,
		RequestID:     responseMetadata.RequestID,
		RequestCount:  1,
		StatusCode:    responseMetadata.StatusCode,
		LatencyMillis: time.Since(started).Milliseconds(),
		LatencyNanos:  time.Since(started).Nanoseconds(),
		OutputKind:    "embeddings",
		Usage:         providerUsage(resp.Usage, len(vectors)),
	}
	metadata := providerMetadata(result)
	return &api.EmbeddingOutput{
		Vectors:        vectors,
		Metadata:       metadata,
		ProviderResult: result,
	}, nil
}

func (s *Service) ListModels(ctx context.Context) ([]contract.RuntimeModel, error) {
	endpoint := s.endpointConfig()
	return listOpenAICompatibleModelsWithError(ctx, endpoint.client(), endpoint.providerName())
}

func (s *Service) endpointConfig() EndpointConfig {
	if s == nil {
		return EndpointConfig{}
	}
	return s.endpoint
}

func (cfg EndpointConfig) providerName() string {
	if strings.TrimSpace(cfg.Provider) == "" {
		return runtimeName
	}
	return strings.TrimSpace(cfg.Provider)
}

func openAICompatibleMessages(input api.GenerateTextInput) ([]openaicompat.ChatMessage, error) {
	if len(input.Messages) > 0 {
		messages := make([]openaicompat.ChatMessage, 0, len(input.Messages))
		for _, message := range input.Messages {
			content, err := openAICompatibleMessageContent(message)
			if err != nil {
				return nil, err
			}
			messages = append(messages, openaicompat.ChatMessage{
				Role:       message.Role,
				Content:    content,
				ToolCalls:  openAICompatibleToolCalls(message.ToolCalls),
				ToolCallID: message.ToolCallID,
				Name:       message.Name,
			})
		}
		return messages, nil
	}
	messages := make([]openaicompat.ChatMessage, 0, 2)
	if input.SystemPrompt != "" {
		messages = append(messages, openaicompat.ChatMessage{Role: "system", Content: input.SystemPrompt})
	}
	if input.Prompt != "" {
		messages = append(messages, openaicompat.ChatMessage{Role: "user", Content: input.Prompt})
	}
	return messages, nil
}

func openAICompatibleMessageContent(message api.Message) (any, error) {
	if len(message.Parts) == 0 {
		return message.Content, nil
	}
	parts, err := openAICompatibleContentParts(message.Content, message.Parts)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 && parts[0].Type == contract.ContentPartTypeText {
		return parts[0].Text, nil
	}
	return parts, nil
}

func openAICompatibleContentParts(prefix any, contentParts []contract.ContentPart) ([]openaicompat.ChatContentPart, error) {
	if err := contract.ValidateContentParts(contentParts); err != nil {
		return nil, err
	}
	parts := make([]openaicompat.ChatContentPart, 0, len(contentParts)+1)
	if text, ok := prefix.(string); ok && strings.TrimSpace(text) != "" {
		parts = append(parts, openaicompat.ChatContentPart{Type: contract.ContentPartTypeText, Text: text})
	}
	for _, part := range contentParts {
		switch part.Type {
		case contract.ContentPartTypeText:
			parts = append(parts, openaicompat.ChatContentPart{Type: contract.ContentPartTypeText, Text: part.Text})
		case contract.ContentPartTypeImage:
			url := part.URL
			if url == "" && part.Data != "" {
				mime := part.MIMEType
				if mime == "" {
					mime = "image/jpeg"
				}
				url = fmt.Sprintf("data:%s;base64,%s", mime, part.Data)
			}
			parts = append(parts, openaicompat.ChatContentPart{
				Type: "image_url",
				ImageURL: &openaicompat.ChatImageURL{
					URL: url,
				},
			})
		default:
			return nil, fmt.Errorf("openai-compatible content part type %q is not supported", part.Type)
		}
	}
	return parts, nil
}

func openAICompatibleTools(tools []api.ToolDefinition) []openaicompat.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openaicompat.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		out = append(out, openaicompat.ToolDefinition{
			Type: tool.Type,
			Function: openaicompat.FunctionDefinition{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			},
		})
	}
	return out
}

func openAICompatibleToolCalls(calls []api.ToolCall) []openaicompat.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]openaicompat.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, openaicompat.ToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: openaicompat.FunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

func controlplaneLogprobs(logprobs *openaicompat.ChoiceLogprobs) []api.TokenLogprob {
	if logprobs == nil || len(logprobs.Content) == 0 {
		return nil
	}
	out := make([]api.TokenLogprob, 0, len(logprobs.Content))
	for _, item := range logprobs.Content {
		out = append(out, api.TokenLogprob{
			Token:   item.Token,
			Logprob: item.Logprob,
			Bytes:   item.Bytes,
		})
	}
	return out
}

func providerUsage(usage *openaicompat.Usage, vectorCount int) api.ProviderUsage {
	out := api.ProviderUsage{VectorCount: vectorCount}
	if usage == nil {
		return out
	}
	out.PromptTokens = usage.PromptTokens
	out.CompletionTokens = usage.CompletionTokens
	out.TotalTokens = usage.TotalTokens
	return out
}

func providerMetadata(result api.ProviderResultMetadata) map[string]any {
	return map[string]any{
		"provider":        result.Provider,
		"model":           result.Model,
		"base_url":        result.BaseURL,
		"request_id":      result.RequestID,
		"request_count":   result.RequestCount,
		"status_code":     result.StatusCode,
		"latency_nanos":   result.LatencyNanos,
		"latency_millis":  result.LatencyMillis,
		"finish_reason":   result.FinishReason,
		"output_kind":     result.OutputKind,
		"usage":           result.Usage,
		"provider_result": result,
	}
}

func providerResultFromResponse(endpoint EndpointConfig, fallbackModel string, started time.Time, responseMetadata openaicompat.ResponseMetadata, resp *openaicompat.ChatCompletionResponse, choice openaicompat.ChatCompletionChoice) api.ProviderResultMetadata {
	elapsed := time.Since(started)
	result := api.ProviderResultMetadata{
		Provider:      endpoint.providerName(),
		Model:         fallbackModel,
		BaseURL:       endpoint.BaseURL,
		RequestID:     responseMetadata.RequestID,
		RequestCount:  1,
		StatusCode:    responseMetadata.StatusCode,
		LatencyMillis: elapsed.Milliseconds(),
		LatencyNanos:  elapsed.Nanoseconds(),
		FinishReason:  choice.FinishReason,
	}
	if resp != nil {
		result.Model = firstNonEmpty(resp.Model, fallbackModel)
		result.RequestID = firstNonEmpty(responseMetadata.RequestID, resp.ID)
		result.Usage = providerUsage(resp.Usage, 0)
	}
	return result
}

func providerResultFromError(endpoint EndpointConfig, fallbackModel string, started time.Time, responseMetadata openaicompat.ResponseMetadata, err error) api.ProviderResultMetadata {
	elapsed := time.Since(started)
	return api.ProviderResultMetadata{
		Provider:      endpoint.providerName(),
		Model:         fallbackModel,
		BaseURL:       endpoint.BaseURL,
		RequestID:     responseMetadata.RequestID,
		RequestCount:  1,
		StatusCode:    responseMetadata.StatusCode,
		LatencyMillis: elapsed.Milliseconds(),
		LatencyNanos:  elapsed.Nanoseconds(),
		OutputKind:    "provider_error",
		Error:         providerErrorDetails(err),
	}
}

func providerErrorDetails(err error) *api.ProviderError {
	if err == nil {
		return nil
	}
	var apiErr *openaicompat.APIError
	if errors.As(err, &apiErr) {
		return &api.ProviderError{
			Kind:       string(apiErr.Kind),
			Message:    apiErr.Message,
			Type:       apiErr.Type,
			Param:      apiErr.Param,
			Code:       apiErr.Code,
			StatusCode: apiErr.StatusCode,
			Body:       apiErr.Body,
			Retryable:  apiErr.Retryable,
		}
	}
	return &api.ProviderError{Message: err.Error()}
}

func classifyEmptyTextOutput(req openaicompat.ChatCompletionRequest, choice openaicompat.ChatCompletionChoice) string {
	finishReason := strings.ToLower(strings.TrimSpace(choice.FinishReason))
	if finishReason == "length" || finishReason == "max_tokens" || (req.MaxTokens > 0 && finishReason == "") {
		return "truncated"
	}
	if len(choice.Message.ToolCalls) > 0 {
		return "tool_only"
	}
	if strings.TrimSpace(openaicompat.MessageContentReasoning(choice.Message)) != "" {
		return "reasoning_only"
	}
	return "empty_final_content"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
