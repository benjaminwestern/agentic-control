package openaicompatible

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/benjaminwestern/agentic-control/pkg/contract"
	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	"github.com/benjaminwestern/agentic-control/pkg/httpclient/openaicompat"
)

func TestServiceGenerateTextPassesControlsAndTools(t *testing.T) {
	var got struct {
		Model       string               `json:"model"`
		MaxTokens   int                  `json:"max_tokens"`
		Temperature *float64             `json:"temperature"`
		TopP        *float64             `json:"top_p"`
		Tools       []api.ToolDefinition `json:"tools"`
		Messages    []api.Message        `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("x-request-id", "req-fixture")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-fixture",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "done"},
			}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
		})
	}))
	defer server.Close()

	temp := 0.2
	topP := 0.8
	out, err := NewService(EndpointConfig{Provider: "openai", BaseURL: server.URL}).GenerateText(context.Background(), api.GenerateTextInput{
		ModelSelection: api.TextGenerationModelSelection{
			Provider: "openai",
			Model:    "gpt-fixture",
			Options: api.ModelOptions{
				MaxOutputTokens: 64,
				Temperature:     &temp,
				TopP:            &topP,
			},
		},
		Messages: []api.Message{{Role: "user", Content: "hello"}},
		Tools: []api.ToolDefinition{{
			Type: "function",
			Function: api.FunctionDefinition{
				Name:       "lookup",
				Parameters: map[string]any{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("GenerateText: %v", err)
	}
	if got.Model != "gpt-fixture" || got.MaxTokens != 64 || got.Temperature == nil || *got.Temperature != temp || got.TopP == nil || *got.TopP != topP {
		t.Fatalf("controls not passed: %+v", got)
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "lookup" {
		t.Fatalf("tools not passed: %+v", got.Tools)
	}
	if out.Text != "done" || out.ProviderResult.Provider != "openai" || out.ProviderResult.Usage.PromptTokens != 3 || out.ProviderResult.Usage.CompletionTokens != 2 {
		t.Fatalf("unexpected output: %+v", out)
	}
	if out.ProviderResult.RequestID != "req-fixture" || out.ProviderResult.RequestCount != 1 || out.ProviderResult.StatusCode != http.StatusOK || out.ProviderResult.LatencyNanos <= 0 || out.ProviderResult.OutputKind != "text" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestServiceGenerateTextMapsControlPlaneContentParts(t *testing.T) {
	var got struct {
		Messages []openaicompat.ChatMessage `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-fixture",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "described"},
			}},
		})
	}))
	defer server.Close()

	_, err := NewService(EndpointConfig{Provider: "openai", BaseURL: server.URL}).GenerateText(context.Background(), api.GenerateTextInput{
		ModelSelection: api.TextGenerationModelSelection{
			Provider: "openai",
			Model:    "gpt-fixture",
		},
		Messages: []api.Message{{
			Role:    "user",
			Content: "caption",
			Parts: []contract.ContentPart{
				{Type: contract.ContentPartTypeText, Text: "describe"},
				{Type: contract.ContentPartTypeImage, MIMEType: "image/png", Data: "aW1hZ2U="},
			},
		}},
	})
	if err != nil {
		t.Fatalf("GenerateText: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(got.Messages))
	}
	encoded, err := json.Marshal(got.Messages[0].Content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	var parts []openaicompat.ChatContentPart
	if err := json.Unmarshal(encoded, &parts); err != nil {
		t.Fatalf("decode content parts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("part count = %d, want 3: %#v", len(parts), parts)
	}
	if parts[0].Type != "text" || parts[0].Text != "caption" || parts[1].Type != "text" || parts[1].Text != "describe" {
		t.Fatalf("text parts = %#v, want caption/describe", parts)
	}
	if parts[2].Type != "image_url" || parts[2].ImageURL == nil || parts[2].ImageURL.URL != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("image part = %#v, want data image_url", parts[2])
	}
}

func TestServiceGenerateTextReturnsProviderResultErrorForEmptyFinalContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req-empty")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-empty",
			"model": "gemma4:e2b-it-q4_K_M",
			"choices": []map[string]any{{
				"finish_reason": "length",
				"message":       map[string]any{"role": "assistant", "content": ""},
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 0, "total_tokens": 10},
		})
	}))
	defer server.Close()

	_, err := NewService(EndpointConfig{Provider: "ollama", BaseURL: server.URL}).GenerateText(context.Background(), api.GenerateTextInput{
		ModelSelection: api.TextGenerationModelSelection{
			Provider: "ollama",
			Model:    "gemma4:e2b-it-q4_K_M",
			Options:  api.ModelOptions{MaxOutputTokens: 128},
		},
		Messages: []api.Message{{Role: "user", Content: "summarise"}},
	})
	if err == nil {
		t.Fatal("GenerateText succeeded, want empty-content error")
	}
	var resultErr *api.ProviderResultError
	if !errors.As(err, &resultErr) {
		t.Fatalf("error type = %T, want ProviderResultError: %v", err, err)
	}
	result := resultErr.Result
	if result.Provider != "ollama" || result.Model != "gemma4:e2b-it-q4_K_M" || result.BaseURL != server.URL || result.FinishReason != "length" || result.OutputKind != "truncated" {
		t.Fatalf("provider result = %+v", result)
	}
	if result.RequestID != "req-empty" || result.RequestCount != 1 || result.StatusCode != http.StatusOK || result.LatencyNanos <= 0 || result.Usage.PromptTokens != 10 {
		t.Fatalf("provider result metadata = %+v", result)
	}
	for _, want := range []string{"provider=ollama", "model=gemma4:e2b-it-q4_K_M", "base_url=" + server.URL, "finish_reason=length", "output_kind=truncated"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestServiceGenerateEmbeddingsPassesDimensions(t *testing.T) {
	var got struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("x-request-id", "embed-req")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "embed-fixture",
			"data":  []map[string]any{{"embedding": []float64{1, 2, 3}}},
		})
	}))
	defer server.Close()

	out, err := NewService(EndpointConfig{Provider: "openai-compatible", BaseURL: server.URL}).GenerateEmbeddings(context.Background(), api.EmbeddingInput{
		ModelSelection: api.EmbeddingModelSelection{
			Provider: "openai-compatible",
			Model:    "embed-fixture",
		},
		Texts:      []string{"alpha"},
		Dimensions: 3,
	})
	if err != nil {
		t.Fatalf("GenerateEmbeddings: %v", err)
	}
	if got.Model != "embed-fixture" || got.Dimensions != 3 || len(got.Input) != 1 || got.Input[0] != "alpha" {
		t.Fatalf("unexpected request: %+v", got)
	}
	if len(out.Vectors) != 1 || len(out.Vectors[0]) != 3 || out.ProviderResult.Usage.VectorCount != 1 {
		t.Fatalf("unexpected output: %+v", out)
	}
	if out.ProviderResult.RequestID != "embed-req" || out.ProviderResult.RequestCount != 1 || out.ProviderResult.StatusCode != http.StatusOK || out.ProviderResult.LatencyNanos <= 0 || out.ProviderResult.OutputKind != "embeddings" {
		t.Fatalf("unexpected provider result: %+v", out.ProviderResult)
	}
}
