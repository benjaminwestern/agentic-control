package openaicompatible

import "testing"

func TestResolveEndpointConfigHonorsOllamaHostAndNormalizesV1(t *testing.T) {
	endpoint := ResolveEndpointConfig(EndpointResolutionInput{
		Provider: "ollama",
		Model:    "nomic-embed-text:latest",
		Environment: map[string]string{
			"OLLAMA_HOST": "localhost:11434",
		},
	})
	if endpoint.Provider != ProviderOllama {
		t.Fatalf("provider = %q, want %q", endpoint.Provider, ProviderOllama)
	}
	if endpoint.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("base url = %q, want normalized Ollama /v1 URL", endpoint.BaseURL)
	}
	if len(endpoint.Models) != 1 || endpoint.Models[0] != "nomic-embed-text:latest" {
		t.Fatalf("models = %#v", endpoint.Models)
	}
}

func TestResolveEndpointConfigOpenAIDefaults(t *testing.T) {
	endpoint := ResolveEndpointConfig(EndpointResolutionInput{
		Provider: "openai",
		Model:    "gpt-4o-mini",
		Environment: map[string]string{
			"OPENAI_API_KEY": "sk-fixture",
		},
	})
	if endpoint.Provider != ProviderOpenAI {
		t.Fatalf("provider = %q, want %q", endpoint.Provider, ProviderOpenAI)
	}
	if endpoint.BaseURL != DefaultOpenAIBaseURL {
		t.Fatalf("base url = %q, want %q", endpoint.BaseURL, DefaultOpenAIBaseURL)
	}
	if endpoint.APIKeyEnv != "OPENAI_API_KEY" || endpoint.APIKey != "sk-fixture" {
		t.Fatalf("api key env/key = %q/%q", endpoint.APIKeyEnv, endpoint.APIKey)
	}
}

func TestResolveSelectionsReturnEndpointOptions(t *testing.T) {
	text := ResolveTextGenerationSelection(EndpointResolutionInput{
		Provider:  "openaicompatible",
		Model:     "fixture-chat",
		BaseURL:   "127.0.0.1:9000/custom",
		APIKeyEnv: "FIXTURE_API_KEY",
		Environment: map[string]string{
			"FIXTURE_API_KEY": "secret",
		},
	})
	if text.Provider != ProviderOpenAICompatible || text.Model != "fixture-chat" {
		t.Fatalf("text selection = %+v", text)
	}
	if text.Options.BaseURL != "http://127.0.0.1:9000/custom/v1" || text.Options.APIKey != "secret" {
		t.Fatalf("text options = %+v", text.Options)
	}

	embedding := ResolveEmbeddingSelection(EndpointResolutionInput{
		Provider: "openai-compatible",
		Model:    "fixture-embed",
		BaseURL:  "https://example.test",
	}, 768)
	if embedding.Provider != ProviderOpenAICompatible || embedding.Model != "fixture-embed" || embedding.Dimensions != 768 {
		t.Fatalf("embedding selection = %+v", embedding)
	}
	if embedding.Options.BaseURL != "https://example.test/v1" {
		t.Fatalf("embedding base url = %q", embedding.Options.BaseURL)
	}
}
