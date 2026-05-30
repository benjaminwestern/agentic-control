package openaicompatible

import (
	"context"
	"encoding/json"
	"fmt"

	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	"github.com/benjaminwestern/agentic-control/pkg/httpclient/openaicompat"
)

func (p *Provider) GenerateCommitMessage(ctx context.Context, input api.CommitMessageInput) (*api.CommitMessageOutput, error) {
	model := input.ModelSelection.Model
	if model == "" {
		model = "ollama"
	}

	client := openAIClientForOptions(input.ModelSelection.Options)

	systemPrompt := "You are an expert software engineer. Write a concise, high-quality commit message for the provided diff. Follow conventional commits format."
	if input.Instruction != "" {
		systemPrompt = input.Instruction
	}

	req := openaicompat.ChatCompletionRequest{
		Model: model,
		Messages: []openaicompat.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: input.Diff},
		},
		ResponseFormat: &openaicompat.ResponseFormat{Type: "text"},
		Stream:         false,
	}
	applyChatModelOptions(&req, input.ModelSelection.Options)

	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible text generation failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	return &api.CommitMessageOutput{
		Message:  openaicompat.MessageContentText(resp.Choices[0].Message.Content),
		Metadata: map[string]any{"model": resp.Model, "usage": resp.Usage},
	}, nil
}

func (p *Provider) GeneratePrContent(ctx context.Context, input api.PrContentInput) (*api.PrContentOutput, error) {
	model := input.ModelSelection.Model
	if model == "" {
		model = "ollama"
	}

	client := openAIClientForOptions(input.ModelSelection.Options)

	systemPrompt := "You are an expert software engineer. Write a Pull Request title and body based on the provided diff. Output MUST be a JSON object with 'title' and 'body' string fields."
	if input.Instruction != "" {
		systemPrompt = input.Instruction
	}

	req := openaicompat.ChatCompletionRequest{
		Model: model,
		Messages: []openaicompat.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: fmt.Sprintf("Title: %s\n\nDiff:\n%s", input.Title, input.Diff)},
		},
		ResponseFormat: &openaicompat.ResponseFormat{Type: "json_object"},
		Stream:         false,
	}
	applyChatModelOptions(&req, input.ModelSelection.Options)

	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible text generation failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	rawText := openaicompat.MessageContentText(resp.Choices[0].Message.Content)
	var out struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(rawText), &out); err != nil {
		return nil, fmt.Errorf("failed to parse json response: %w\nRaw: %s", err, rawText)
	}

	return &api.PrContentOutput{
		Title:    out.Title,
		Body:     out.Body,
		Metadata: map[string]any{"model": resp.Model, "usage": resp.Usage},
	}, nil
}

func (p *Provider) GenerateBranchName(ctx context.Context, input api.BranchNameInput) (*api.BranchNameOutput, error) {
	model := input.ModelSelection.Model
	if model == "" {
		model = "ollama"
	}

	client := openAIClientForOptions(input.ModelSelection.Options)

	req := openaicompat.ChatCompletionRequest{
		Model: model,
		Messages: []openaicompat.ChatMessage{
			{Role: "system", Content: "Generate a short, valid git branch name based on the following summary. Output ONLY the branch name, lowercase with hyphens, no explanation."},
			{Role: "user", Content: input.Summary},
		},
		ResponseFormat: &openaicompat.ResponseFormat{Type: "text"},
		Stream:         false,
	}
	applyChatModelOptions(&req, input.ModelSelection.Options)

	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible text generation failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	return &api.BranchNameOutput{
		Name:     openaicompat.MessageContentText(resp.Choices[0].Message.Content),
		Metadata: map[string]any{"model": resp.Model, "usage": resp.Usage},
	}, nil
}

func (p *Provider) GenerateThreadTitle(ctx context.Context, input api.ThreadTitleInput) (*api.ThreadTitleOutput, error) {
	model := input.ModelSelection.Model
	if model == "" {
		model = "ollama"
	}

	client := openAIClientForOptions(input.ModelSelection.Options)

	req := openaicompat.ChatCompletionRequest{
		Model: model,
		Messages: []openaicompat.ChatMessage{
			{Role: "system", Content: "Generate a short (3-6 words) descriptive title for a chat thread based on this initial prompt. Output ONLY the title, no explanation or quotes."},
			{Role: "user", Content: input.Prompt},
		},
		ResponseFormat: &openaicompat.ResponseFormat{Type: "text"},
		Stream:         false,
	}
	applyChatModelOptions(&req, input.ModelSelection.Options)

	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible text generation failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	return &api.ThreadTitleOutput{
		Title:    openaicompat.MessageContentText(resp.Choices[0].Message.Content),
		Metadata: map[string]any{"model": resp.Model, "usage": resp.Usage},
	}, nil
}

func (p *Provider) GenerateText(ctx context.Context, input api.GenerateTextInput) (*api.GenerateTextOutput, error) {
	return GenerateTextWithSigma(ctx, endpointConfigFromSelection(input.ModelSelection), input)
}

func openAIClientForOptions(options api.ModelOptions) *openaicompat.Client {
	client := openaicompat.NewClient(options.BaseURL, options.APIKeyEnv)
	client.SetAPIKey(options.APIKey)
	if options.OAuthTokenURL != "" {
		client.SetOAuthCredentials(options.OAuthTokenURL, options.OAuthClientID, options.OAuthClientSecret)
	}
	return client
}

func applyChatModelOptions(req *openaicompat.ChatCompletionRequest, options api.ModelOptions) {
	if req == nil {
		return
	}
	req.ReasoningEffort = options.ReasoningEffort
	req.Logprobs = options.Logprobs
	req.TopLogprobs = options.TopLogprobs
	req.MaxTokens = options.MaxOutputTokens
	req.Temperature = options.Temperature
	req.TopP = options.TopP
	if options.ResponseSchema != nil {
		req.ResponseFormat = &openaicompat.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &openaicompat.JSONSchemaDef{
				Name:   "structured_output",
				Strict: true,
				Schema: options.ResponseSchema,
			},
		}
	}
}

func endpointConfigFromSelection(selection api.TextGenerationModelSelection) EndpointConfig {
	endpoint := ResolveEndpointConfig(EndpointResolutionInput{
		Provider:  selection.Provider,
		Model:     selection.Model,
		BaseURL:   selection.Options.BaseURL,
		APIKeyEnv: selection.Options.APIKeyEnv,
		APIKey:    selection.Options.APIKey,
	})
	endpoint.OAuthTokenURL = selection.Options.OAuthTokenURL
	endpoint.OAuthClientID = selection.Options.OAuthClientID
	endpoint.OAuthClientSecret = selection.Options.OAuthClientSecret
	return endpoint
}
