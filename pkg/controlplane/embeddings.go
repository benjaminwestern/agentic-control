package controlplane

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type EmbeddingInput struct {
	ModelSelection EmbeddingModelSelection
	Texts          []string
	Dimensions     int
	Metadata       map[string]any
}

type EmbeddingOutput struct {
	Vectors        [][]float64
	Metadata       map[string]any
	ProviderResult ProviderResultMetadata
}

type EmbeddingProvider interface {
	GenerateEmbeddings(context.Context, EmbeddingInput) (*EmbeddingOutput, error)
}

type EmbeddingModelSelection struct {
	Provider   string
	Model      string
	Dimensions int
	Options    ModelOptions
	Fallbacks  []string
}

type EmbeddingRouter struct {
	mu              sync.RWMutex
	defaultProvider string
	providers       map[string]EmbeddingProvider
}

func NewEmbeddingRouter(defaultProvider string, providers map[string]EmbeddingProvider) *EmbeddingRouter {
	router := &EmbeddingRouter{
		defaultProvider: NormalizeRuntimeBackend(defaultProvider),
		providers:       make(map[string]EmbeddingProvider, len(providers)),
	}
	for name, provider := range providers {
		router.Register(name, provider)
	}
	return router
}

func (r *EmbeddingRouter) Register(providerName string, provider EmbeddingProvider) {
	if r == nil || provider == nil {
		return
	}
	providerName = NormalizeRuntimeBackend(providerName)
	if providerName == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[providerName] = provider
	if r.defaultProvider == "" {
		r.defaultProvider = providerName
	}
}

func (r *EmbeddingRouter) Route(providerName string) (EmbeddingProvider, error) {
	if r == nil {
		return nil, fmt.Errorf("embedding router is nil")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if provider := r.providers[NormalizeRuntimeBackend(providerName)]; provider != nil {
		return provider, nil
	}
	if provider := r.providers[r.defaultProvider]; provider != nil {
		return provider, nil
	}
	return nil, fmt.Errorf("no embedding provider registered")
}

func (r *EmbeddingRouter) ResolveSelection(selection EmbeddingModelSelection) EmbeddingModelSelection {
	resolved := selection
	resolved.Provider = r.resolveProviderName(selection)
	return resolved
}

func (r *EmbeddingRouter) resolveProviderName(selection EmbeddingModelSelection) string {
	defaultProvider := ""
	if r != nil {
		defaultProvider = r.defaultProvider
	}
	candidates := []string{
		selection.Provider,
		InferRuntimeBackend(selection.Model),
	}
	candidates = append(candidates, selection.Fallbacks...)
	candidates = append(candidates, defaultProvider)

	for _, candidate := range candidates {
		normalized := NormalizeRuntimeBackend(candidate)
		if normalized == "" {
			continue
		}
		if r == nil {
			return normalized
		}
		r.mu.RLock()
		provider := r.providers[normalized]
		r.mu.RUnlock()
		if provider != nil {
			return normalized
		}
	}
	return NormalizeRuntimeBackend(selection.Provider)
}

func (r *EmbeddingRouter) GenerateEmbeddings(ctx context.Context, providerName string, input EmbeddingInput) (*EmbeddingOutput, error) {
	started := time.Now()
	if providerName != "" {
		input.ModelSelection.Provider = providerName
	}
	input.ModelSelection = r.ResolveSelection(input.ModelSelection)
	provider, err := r.Route(input.ModelSelection.Provider)
	if err != nil {
		return nil, err
	}
	out, err := provider.GenerateEmbeddings(ctx, input)
	if err != nil || out == nil {
		return out, err
	}
	if out.ProviderResult.Provider == "" {
		out.ProviderResult.Provider = input.ModelSelection.Provider
	}
	if out.ProviderResult.Model == "" {
		out.ProviderResult.Model = input.ModelSelection.Model
	}
	elapsed := time.Since(started)
	if out.ProviderResult.LatencyMillis == 0 {
		out.ProviderResult.LatencyMillis = elapsed.Milliseconds()
	}
	if out.ProviderResult.LatencyNanos == 0 {
		out.ProviderResult.LatencyNanos = elapsed.Nanoseconds()
	}
	if out.ProviderResult.RequestCount == 0 {
		out.ProviderResult.RequestCount = 1
	}
	if out.ProviderResult.Usage.VectorCount == 0 {
		out.ProviderResult.Usage.VectorCount = len(out.Vectors)
	}
	out.Metadata = mergeProviderMetadata(out.Metadata, out.ProviderResult)
	return out, nil
}

func (r *EmbeddingRouter) GenerateEmbeddingsForSelection(ctx context.Context, input EmbeddingInput) (*EmbeddingOutput, error) {
	return r.GenerateEmbeddings(ctx, "", input)
}
