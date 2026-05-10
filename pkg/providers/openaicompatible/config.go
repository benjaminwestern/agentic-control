package openaicompatible

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	"github.com/benjaminwestern/agentic-control/pkg/httpclient/openaicompat"
)

const (
	ProviderOllama           = "ollama"
	ProviderOpenAI           = "openai"
	ProviderOpenAICompatible = "openai-compatible"

	DefaultOllamaBaseURL = "http://127.0.0.1:11434/v1"
	DefaultOpenAIBaseURL = "https://api.openai.com/v1"
)

type EndpointConfig struct {
	Provider          string
	BaseURL           string
	APIKeyEnv         string
	APIKey            string
	Models            []string
	OAuthTokenURL     string
	OAuthClientID     string
	OAuthClientSecret string
	Timeout           time.Duration
	HTTPClient        *http.Client
	RetryPolicy       openaicompat.RetryPolicy
}

type ModelConfig struct {
	ID       string
	Label    string
	Provider string
	Options  map[string]any
}

type ProviderConfig struct {
	Models    []ModelConfig
	Endpoints []EndpointConfig
}

type EndpointResolutionInput struct {
	Provider    string
	Model       string
	BaseURL     string
	APIKeyEnv   string
	APIKey      string
	Environment map[string]string
}

func ResolveEndpointConfig(input EndpointResolutionInput) EndpointConfig {
	provider := NormalizeProvider(input.Provider)
	apiKeyEnv := strings.TrimSpace(input.APIKeyEnv)
	if apiKeyEnv == "" {
		apiKeyEnv = defaultAPIKeyEnv(provider)
	}
	apiKey := strings.TrimSpace(input.APIKey)
	if apiKey == "" && apiKeyEnv != "" {
		apiKey = lookupEnvironment(input.Environment, apiKeyEnv)
	}

	model := strings.TrimSpace(input.Model)
	models := []string(nil)
	if model != "" {
		models = []string{model}
	}

	return EndpointConfig{
		Provider:  provider,
		BaseURL:   resolveBaseURL(input, provider),
		APIKeyEnv: apiKeyEnv,
		APIKey:    apiKey,
		Models:    models,
	}
}

func ResolveTextGenerationSelection(input EndpointResolutionInput) api.TextGenerationModelSelection {
	endpoint := ResolveEndpointConfig(input)
	return api.TextGenerationModelSelection{
		Provider: endpoint.Provider,
		Model:    strings.TrimSpace(input.Model),
		Options: api.ModelOptions{
			BaseURL: endpoint.BaseURL,
			APIKey:  endpoint.APIKey,
		},
	}
}

func ResolveEmbeddingSelection(input EndpointResolutionInput, dimensions int) api.EmbeddingModelSelection {
	endpoint := ResolveEndpointConfig(input)
	return api.EmbeddingModelSelection{
		Provider:   endpoint.Provider,
		Model:      strings.TrimSpace(input.Model),
		Dimensions: dimensions,
		Options: api.ModelOptions{
			BaseURL: endpoint.BaseURL,
			APIKey:  endpoint.APIKey,
		},
	}
}

func NormalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", ProviderOllama:
		return ProviderOllama
	case ProviderOpenAI:
		return ProviderOpenAI
	case ProviderOpenAICompatible, "openaicompatible", "openai_compatible":
		return ProviderOpenAICompatible
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func NormalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, "/v1") {
		parsed.Path += "/v1"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func resolveBaseURL(input EndpointResolutionInput, provider string) string {
	if value := strings.TrimSpace(input.BaseURL); value != "" {
		return NormalizeBaseURL(value)
	}
	switch provider {
	case ProviderOpenAI:
		if value := lookupEnvironment(input.Environment, "OPENAI_BASE_URL"); value != "" {
			return NormalizeBaseURL(value)
		}
		return DefaultOpenAIBaseURL
	case ProviderOllama:
		if value := lookupEnvironment(input.Environment, "OLLAMA_HOST"); value != "" {
			return NormalizeBaseURL(value)
		}
		if value := lookupEnvironment(input.Environment, "OPENAI_COMPATIBLE_BASE_URL"); value != "" {
			return NormalizeBaseURL(value)
		}
		return DefaultOllamaBaseURL
	case ProviderOpenAICompatible:
		if value := lookupEnvironment(input.Environment, "OPENAI_COMPATIBLE_BASE_URL"); value != "" {
			return NormalizeBaseURL(value)
		}
		if value := lookupEnvironment(input.Environment, "OLLAMA_HOST"); value != "" {
			return NormalizeBaseURL(value)
		}
		return DefaultOllamaBaseURL
	default:
		if value := lookupEnvironment(input.Environment, "OPENAI_COMPATIBLE_BASE_URL"); value != "" {
			return NormalizeBaseURL(value)
		}
		return DefaultOllamaBaseURL
	}
}

func defaultAPIKeyEnv(provider string) string {
	switch provider {
	case ProviderOpenAI:
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}

func lookupEnvironment(environment map[string]string, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if environment != nil {
		return strings.TrimSpace(environment[key])
	}
	return strings.TrimSpace(os.Getenv(key))
}

func (cfg EndpointConfig) client() *openaicompat.Client {
	client := openaicompat.NewClientWithOptions(cfg.BaseURL, cfg.APIKeyEnv, openaicompat.ClientOptions{
		APIKey:            cfg.APIKey,
		OAuthTokenURL:     cfg.OAuthTokenURL,
		OAuthClientID:     cfg.OAuthClientID,
		OAuthClientSecret: cfg.OAuthClientSecret,
		HTTPClient:        cfg.HTTPClient,
		Timeout:           cfg.Timeout,
		RetryPolicy:       cfg.RetryPolicy,
	})
	return client
}
