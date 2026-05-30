package openaicompatible

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/benjaminwestern/agentic-control/internal/config"
	"github.com/benjaminwestern/agentic-control/pkg/contract"
	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	"github.com/benjaminwestern/agentic-control/pkg/httpclient/openaicompat"
)

func TestProviderSmoke(t *testing.T) {
	events := make(chan contract.RuntimeEvent, 100)
	provider := NewProvider(func(e contract.RuntimeEvent) {
		events <- e
	}, config.RuntimeConfig{})

	// Spin up mock server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		t.Log("Mock server received chat completions request")
		var req openaicompat.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}

		if req.Model == "" {
			t.Error("expected model")
		}
		if len(req.Messages) == 0 {
			t.Error("expected messages")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		resp1 := openaicompat.ChatCompletionResponse{
			ID:      "mock-id",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
		}
		resp1.Choices = append(resp1.Choices, openaicompat.ChatCompletionChoice{
			Index: 0,
			Delta: openaicompat.ChatMessage{
				Role:    "assistant",
				Content: "smoke",
			},
		})
		b1, _ := json.Marshal(resp1)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b1))
		w.(http.Flusher).Flush()

		time.Sleep(50 * time.Millisecond)

		resp2 := openaicompat.ChatCompletionResponse{
			ID:      "mock-id",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
		}
		resp2.Choices = append(resp2.Choices, openaicompat.ChatCompletionChoice{
			Index: 0,
			Delta: openaicompat.ChatMessage{
				Role:    "assistant",
				Content: "-ok",
			},
			FinishReason: "stop",
		})
		b2, _ := json.Marshal(resp2)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b2))
		w.(http.Flusher).Flush()

		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	t.Setenv("OPENAI_COMPATIBLE_BASE_URL", server.URL+"/v1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := provider.StartSession(ctx, api.StartSessionRequest{
		SessionID: "test-sess",
		Model:     "mock-model",
	})
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	if sess.SessionID != "test-sess" {
		t.Errorf("expected test-sess, got %q", sess.SessionID)
	}

	event, err := provider.SendInput(ctx, api.SendInputRequest{
		SessionID: "test-sess",
		Text:      "Say smoke-ok",
	})
	if err != nil {
		t.Fatalf("SendInput failed: %v", err)
	}
	if event == nil {
		t.Fatal("expected event")
	}

	var accumulated string
	for {
		select {
		case e := <-events:
			t.Logf("Received event: %s", e.EventType)
			if e.EventType == "session.errored" {
				t.Fatalf("Session errored: %v", e.Payload["last_error"])
			}
			if e.EventType == "assistant.message.delta" {
				if delta, ok := e.Payload["delta"].(string); ok {
					accumulated += delta
				}
			}
			if e.EventType == "turn.completed" {
				if accumulated != "smoke-ok" {
					t.Errorf("expected smoke-ok, got %q", accumulated)
				}
				return // Done
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for events")
		}
	}
}

func TestProviderMapsTextAndImageContentParts(t *testing.T) {
	events := make(chan contract.RuntimeEvent, 100)
	provider := NewProvider(func(e contract.RuntimeEvent) {
		events <- e
	}, config.RuntimeConfig{})

	seenParts := make(chan []openaicompat.ChatContentPart, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req openaicompat.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}
		if len(req.Messages) == 0 {
			t.Fatal("expected messages")
		}
		content, err := json.Marshal(req.Messages[len(req.Messages)-1].Content)
		if err != nil {
			t.Fatalf("failed to marshal message content: %v", err)
		}
		var parts []openaicompat.ChatContentPart
		if err := json.Unmarshal(content, &parts); err != nil {
			t.Fatalf("failed to decode content parts: %v", err)
		}
		seenParts <- parts

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := provider.StartSession(ctx, api.StartSessionRequest{
		SessionID: "parts-sess",
		Model:     "mock-model",
		ModelOptions: api.ModelOptions{
			BaseURL: server.URL + "/v1",
		},
	}); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if _, err := provider.SendInput(ctx, api.SendInputRequest{
		SessionID: "parts-sess",
		Text:      "caption",
		Parts: []contract.ContentPart{
			{Type: contract.ContentPartTypeText, Text: "describe"},
			{Type: contract.ContentPartTypeImage, MIMEType: "image/png", Data: "aW1hZ2U="},
		},
	}); err != nil {
		t.Fatalf("SendInput failed: %v", err)
	}

	select {
	case parts := <-seenParts:
		if len(parts) != 3 {
			t.Fatalf("part count = %d, want 3: %#v", len(parts), parts)
		}
		if parts[0].Type != "text" || parts[0].Text != "caption" {
			t.Fatalf("first part = %#v, want caption text", parts[0])
		}
		if parts[1].Type != "text" || parts[1].Text != "describe" {
			t.Fatalf("second part = %#v, want describe text", parts[1])
		}
		if parts[2].Type != "image_url" || parts[2].ImageURL == nil || parts[2].ImageURL.URL != "data:image/png;base64,aW1hZ2U=" {
			t.Fatalf("third part = %#v, want data image_url", parts[2])
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for chat completion request")
	}
}

func TestProviderRejectsInvalidOrUnsupportedContentParts(t *testing.T) {
	provider := NewProvider(func(contract.RuntimeEvent) {}, config.RuntimeConfig{})
	ctx := context.Background()
	if _, err := provider.StartSession(ctx, api.StartSessionRequest{SessionID: "invalid-parts"}); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	tests := []struct {
		name string
		part contract.ContentPart
		want string
	}{
		{name: "empty image", part: contract.ContentPart{Type: contract.ContentPartTypeImage}, want: "image part requires url or data"},
		{name: "unsupported valid media", part: contract.ContentPart{Type: contract.ContentPartTypeAudio, URL: "https://example.com/audio.wav"}, want: "not supported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := provider.SendInput(ctx, api.SendInputRequest{
				SessionID: "invalid-parts",
				Parts:     []contract.ContentPart{tt.part},
			})
			if err == nil {
				t.Fatal("SendInput succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestDescribeProbesConfiguredEndpointModels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		resp := openaicompat.ModelListResponse{
			Object: "list",
			Data: []openaicompat.Model{{
				ID:      "configured-dynamic",
				Object:  "model",
				OwnedBy: "test-provider",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewProvider(func(contract.RuntimeEvent) {}, config.RuntimeConfig{
		Endpoints: []config.OpenAICompatibleEndpoint{{
			Provider: "test-provider",
			BaseURL:  server.URL + "/v1",
		}},
	})
	descriptor := provider.Describe()
	if descriptor.Probe == nil || !descriptor.Probe.Installed {
		t.Fatalf("probe = %#v, want installed", descriptor.Probe)
	}
	for _, model := range descriptor.Probe.Models {
		if model.ID == "configured-dynamic" && model.Provider == "test-provider" {
			return
		}
	}
	t.Fatalf("configured endpoint model not discovered: %#v", descriptor.Probe.Models)
}

func TestGenerateTextRoutesModelOptions(t *testing.T) {
	seen := make(chan map[string]any, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}
		seen <- req

		model, _ := req["model"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-test\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"{\\\"ok\\\":true}\"}}]}\n\n", model)
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-test\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n", model)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewProvider(func(contract.RuntimeEvent) {}, config.RuntimeConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := provider.GenerateText(ctx, api.GenerateTextInput{
		ModelSelection: api.TextGenerationModelSelection{
			Model: "mock-model",
			Options: api.ModelOptions{
				BaseURL:         server.URL + "/v1",
				ReasoningEffort: "high",
				Logprobs:        true,
				TopLogprobs:     3,
				ResponseSchema: map[string]any{
					"type": "object",
				},
			},
		},
		Prompt:         "return json",
		ResponseFormat: "text",
	})
	if err != nil {
		t.Fatalf("GenerateText failed: %v", err)
	}
	if out.Text != `{"ok":true}` {
		t.Fatalf("text = %q, want JSON body", out.Text)
	}

	select {
	case req := <-seen:
		if req["reasoning_effort"] != "high" || req["logprobs"] != true || req["top_logprobs"] != float64(3) {
			t.Fatalf("model options not routed: %#v", req)
		}
		responseFormat, _ := req["response_format"].(map[string]any)
		jsonSchema, _ := responseFormat["json_schema"].(map[string]any)
		if responseFormat["type"] != "json_schema" || jsonSchema["schema"] == nil {
			t.Fatalf("response format = %#v, want json_schema", responseFormat)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for request")
	}
}

func TestEmbeddingsSmoke(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req openaicompat.EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}

		resp := openaicompat.EmbeddingResponse{
			Object: "list",
			Model:  req.Model,
		}

		resp.Data = append(resp.Data, struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		}{
			Object:    "embedding",
			Index:     0,
			Embedding: []float64{0.1, 0.2, 0.3},
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := openaicompat.NewClient(server.URL+"/v1", "")

	resp, err := client.CreateEmbeddings(context.Background(), openaicompat.EmbeddingRequest{
		Model: "mock-embed",
		Input: []string{"test string"},
	})

	if err != nil {
		t.Fatalf("CreateEmbeddings failed: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(resp.Data))
	}
	if resp.Data[0].Embedding[0] != 0.1 {
		t.Errorf("unexpected embedding vector value: %v", resp.Data[0].Embedding)
	}
}
