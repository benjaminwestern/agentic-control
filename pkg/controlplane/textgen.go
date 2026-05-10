package controlplane

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/benjaminwestern/agentic-control/pkg/contract"
)

type CommitMessageInput struct {
	ModelSelection TextGenerationModelSelection
	Diff           string
	Instruction    string
	Metadata       map[string]any
}

type CommitMessageOutput struct {
	Message  string
	Metadata map[string]any
}

type PrContentInput struct {
	ModelSelection TextGenerationModelSelection
	Diff           string
	Title          string
	Instruction    string
	Metadata       map[string]any
}

type PrContentOutput struct {
	Title    string
	Body     string
	Metadata map[string]any
}

type BranchNameInput struct {
	ModelSelection TextGenerationModelSelection
	Summary        string
	Metadata       map[string]any
}

type BranchNameOutput struct {
	Name     string
	Metadata map[string]any
}

type ThreadTitleInput struct {
	ModelSelection TextGenerationModelSelection
	Prompt         string
	Metadata       map[string]any
}

type ThreadTitleOutput struct {
	Title    string
	Metadata map[string]any
}

type GenerateTextInput struct {
	ModelSelection TextGenerationModelSelection
	Prompt         string
	SystemPrompt   string
	Messages       []Message
	Tools          []ToolDefinition
	ToolChoice     any
	ResponseFormat string
	Metadata       map[string]any
}

type Message struct {
	Role       string                 `json:"role"`
	Content    any                    `json:"content,omitempty"`
	Parts      []contract.ContentPart `json:"parts,omitempty"`
	ToolCalls  []ToolCall             `json:"tool_calls,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
	Name       string                 `json:"name,omitempty"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type TokenLogprob struct {
	Token   string
	Logprob float64
	Bytes   []byte
}

type GenerateTextOutput struct {
	Text           string
	Metadata       map[string]any
	Logprobs       []TokenLogprob
	ProviderResult ProviderResultMetadata
}

type TextGenerationProvider interface {
	GenerateCommitMessage(context.Context, CommitMessageInput) (*CommitMessageOutput, error)
	GeneratePrContent(context.Context, PrContentInput) (*PrContentOutput, error)
	GenerateBranchName(context.Context, BranchNameInput) (*BranchNameOutput, error)
	GenerateThreadTitle(context.Context, ThreadTitleInput) (*ThreadTitleOutput, error)
	GenerateText(context.Context, GenerateTextInput) (*GenerateTextOutput, error)
}

type TextGenerationModelSelection struct {
	Provider  string
	Model     string
	Options   ModelOptions
	Fallbacks []string
}

type TextGenerationRouter struct {
	mu              sync.RWMutex
	defaultProvider string
	providers       map[string]TextGenerationProvider
}

func NewTextGenerationRouter(defaultProvider string, providers map[string]TextGenerationProvider) *TextGenerationRouter {
	router := &TextGenerationRouter{
		defaultProvider: NormalizeRuntimeBackend(defaultProvider),
		providers:       make(map[string]TextGenerationProvider, len(providers)),
	}
	for name, provider := range providers {
		router.Register(name, provider)
	}
	return router
}

func (r *TextGenerationRouter) Register(providerName string, provider TextGenerationProvider) {
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

func (r *TextGenerationRouter) Route(providerName string) (TextGenerationProvider, error) {
	if r == nil {
		return nil, fmt.Errorf("text generation router is nil")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if provider := r.providers[NormalizeRuntimeBackend(providerName)]; provider != nil {
		return provider, nil
	}
	if provider := r.providers[r.defaultProvider]; provider != nil {
		return provider, nil
	}
	return nil, fmt.Errorf("no text generation provider registered")
}

func (r *TextGenerationRouter) RouteSelection(selection TextGenerationModelSelection) (TextGenerationProvider, error) {
	resolved := r.ResolveSelection(selection)
	return r.Route(resolved.Provider)
}

func (r *TextGenerationRouter) ResolveSelection(selection TextGenerationModelSelection) TextGenerationModelSelection {
	resolved := selection
	resolved.Provider = r.resolveProviderName(selection)
	return resolved
}

func (r *TextGenerationRouter) Providers() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]string, 0, len(r.providers))
	for provider := range r.providers {
		providers = append(providers, provider)
	}
	return providers
}

func (r *TextGenerationRouter) GenerateCommitMessage(ctx context.Context, providerName string, input CommitMessageInput) (*CommitMessageOutput, error) {
	input.ModelSelection.Provider = coalesceProvider(providerName, input.ModelSelection.Provider)
	input.ModelSelection = r.ResolveSelection(input.ModelSelection)
	provider, err := r.Route(input.ModelSelection.Provider)
	if err != nil {
		return nil, err
	}
	return provider.GenerateCommitMessage(ctx, input)
}

func (r *TextGenerationRouter) GeneratePrContent(ctx context.Context, providerName string, input PrContentInput) (*PrContentOutput, error) {
	input.ModelSelection.Provider = coalesceProvider(providerName, input.ModelSelection.Provider)
	input.ModelSelection = r.ResolveSelection(input.ModelSelection)
	provider, err := r.Route(input.ModelSelection.Provider)
	if err != nil {
		return nil, err
	}
	return provider.GeneratePrContent(ctx, input)
}

func (r *TextGenerationRouter) GenerateBranchName(ctx context.Context, providerName string, input BranchNameInput) (*BranchNameOutput, error) {
	input.ModelSelection.Provider = coalesceProvider(providerName, input.ModelSelection.Provider)
	input.ModelSelection = r.ResolveSelection(input.ModelSelection)
	provider, err := r.Route(input.ModelSelection.Provider)
	if err != nil {
		return nil, err
	}
	return provider.GenerateBranchName(ctx, input)
}

func (r *TextGenerationRouter) GenerateThreadTitle(ctx context.Context, providerName string, input ThreadTitleInput) (*ThreadTitleOutput, error) {
	input.ModelSelection.Provider = coalesceProvider(providerName, input.ModelSelection.Provider)
	input.ModelSelection = r.ResolveSelection(input.ModelSelection)
	provider, err := r.Route(input.ModelSelection.Provider)
	if err != nil {
		return nil, err
	}
	return provider.GenerateThreadTitle(ctx, input)
}

func (r *TextGenerationRouter) GenerateText(ctx context.Context, providerName string, input GenerateTextInput) (*GenerateTextOutput, error) {
	started := time.Now()
	input.ModelSelection.Provider = coalesceProvider(providerName, input.ModelSelection.Provider)
	input.ModelSelection = r.ResolveSelection(input.ModelSelection)
	provider, err := r.Route(input.ModelSelection.Provider)
	if err != nil {
		return nil, err
	}
	out, err := provider.GenerateText(ctx, input)
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
	out.Metadata = mergeProviderMetadata(out.Metadata, out.ProviderResult)
	return out, nil
}

func (r *TextGenerationRouter) GenerateCommitMessageForSelection(ctx context.Context, input CommitMessageInput) (*CommitMessageOutput, error) {
	return r.GenerateCommitMessage(ctx, "", input)
}

func (r *TextGenerationRouter) GeneratePrContentForSelection(ctx context.Context, input PrContentInput) (*PrContentOutput, error) {
	return r.GeneratePrContent(ctx, "", input)
}

func (r *TextGenerationRouter) GenerateBranchNameForSelection(ctx context.Context, input BranchNameInput) (*BranchNameOutput, error) {
	return r.GenerateBranchName(ctx, "", input)
}

func (r *TextGenerationRouter) GenerateThreadTitleForSelection(ctx context.Context, input ThreadTitleInput) (*ThreadTitleOutput, error) {
	return r.GenerateThreadTitle(ctx, "", input)
}

func (r *TextGenerationRouter) GenerateTextForSelection(ctx context.Context, input GenerateTextInput) (*GenerateTextOutput, error) {
	return r.GenerateText(ctx, "", input)
}

func (r *TextGenerationRouter) resolveProviderName(selection TextGenerationModelSelection) string {
	defaultProvider := ""
	if r != nil {
		defaultProvider = r.defaultProvider
	}
	for _, candidate := range providerCandidates(selection, defaultProvider) {
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

func providerCandidates(selection TextGenerationModelSelection, defaultProvider string) []string {
	candidates := []string{
		selection.Provider,
		InferRuntimeBackend(selection.Model),
	}
	candidates = append(candidates, selection.Fallbacks...)
	candidates = append(candidates, defaultProvider)
	return candidates
}

func InferTextGenerationProvider(model string) string {
	return InferRuntimeProvider(model)
}

func coalesceProvider(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
