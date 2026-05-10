package openaicompat

import (
	"encoding/json"
	"strings"
)

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"` // Can be string or []ChatContentPart
	Reasoning        string     `json:"reasoning,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
}

// ToolCall represents a tool call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents the function and arguments requested.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON encoded arguments
}

// ToolDefinition describes a tool/function made available to a chat model.
type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition describes an OpenAI-compatible function tool.
type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ChatContentPart represents a part of a message content (e.g., text, image).
type ChatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *ChatImageURL `json:"image_url,omitempty"`
}

// ChatImageURL holds the URL for an image content part.
type ChatImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ResponseFormat specifies the format of the response, including structured JSON schema.
type ResponseFormat struct {
	Type       string         `json:"type"` // "text", "json_object", "json_schema"
	JSONSchema *JSONSchemaDef `json:"json_schema,omitempty"`
}

// JSONSchemaDef defines the schema for structured output.
type JSONSchemaDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema"`
	Strict      bool           `json:"strict,omitempty"`
}

// ChatCompletionRequest is the payload sent to /v1/chat/completions
type ChatCompletionRequest struct {
	Model           string           `json:"model"`
	Messages        []ChatMessage    `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ToolChoice      any              `json:"tool_choice,omitempty"`
	MaxTokens       int              `json:"max_tokens,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	TopP            *float64         `json:"top_p,omitempty"`
	ResponseFormat  *ResponseFormat  `json:"response_format,omitempty"`
	Stream          bool             `json:"stream,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	Logprobs        bool             `json:"logprobs,omitempty"`
	TopLogprobs     int              `json:"top_logprobs,omitempty"`
}

type TokenLogprob struct {
	Token       string         `json:"token"`
	Logprob     float64        `json:"logprob"`
	Bytes       []byte         `json:"bytes,omitempty"`
	TopLogprobs []TokenLogprob `json:"top_logprobs,omitempty"`
}

type ChoiceLogprobs struct {
	Content []TokenLogprob `json:"content,omitempty"`
}

type ChatCompletionChoice struct {
	Index        int             `json:"index"`
	Message      ChatMessage     `json:"message,omitempty"`
	Delta        ChatMessage     `json:"delta,omitempty"`
	Logprobs     *ChoiceLogprobs `json:"logprobs,omitempty"`
	FinishReason string          `json:"finish_reason"`
}

// ChatCompletionResponse is the payload received from /v1/chat/completions
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *Usage                 `json:"usage,omitempty"`
	Error   *Error                 `json:"error,omitempty"`
}

// Usage holds token usage statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Error represents an error returned by the API.
type Error struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param"`
	Code    any    `json:"code"`
}

func (e *Error) Error() string {
	return e.Message
}

// EmbeddingRequest is the payload sent to /v1/embeddings
type EmbeddingRequest struct {
	Model          string `json:"model"`
	Input          any    `json:"input"`                     // Can be string or []string
	EncodingFormat string `json:"encoding_format,omitempty"` // e.g. "float"
	Dimensions     int    `json:"dimensions,omitempty"`
}

// EmbeddingResponse is the payload received from /v1/embeddings
type EmbeddingResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
	Usage *Usage `json:"usage,omitempty"`
	Error *Error `json:"error,omitempty"`
}

// MessageContentText extracts text from the mixed content field.
func MessageContentText(content any) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var b []byte
		b, _ = json.Marshal(v)
		var parts []ChatContentPart
		if err := json.Unmarshal(b, &parts); err == nil {
			var result string
			for _, part := range parts {
				if part.Type == "text" {
					result += part.Text
				}
			}
			return result
		}
	case []ChatContentPart:
		var result string
		for _, part := range v {
			if part.Type == "text" {
				result += part.Text
			}
		}
		return result
	}
	return ""
}

func MessageContentReasoning(message ChatMessage) string {
	if strings.TrimSpace(message.ReasoningContent) != "" {
		return message.ReasoningContent
	}
	if strings.TrimSpace(message.Reasoning) != "" {
		return message.Reasoning
	}
	return contentPartText(message.Content, "reasoning")
}

func contentPartText(content any, partType string) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case []interface{}:
		var b []byte
		b, _ = json.Marshal(v)
		var parts []ChatContentPart
		if err := json.Unmarshal(b, &parts); err == nil {
			var result string
			for _, part := range parts {
				if part.Type == partType {
					result += part.Text
				}
			}
			return result
		}
	case []ChatContentPart:
		var result string
		for _, part := range v {
			if part.Type == partType {
				result += part.Text
			}
		}
		return result
	}
	return ""
}

func ChoiceContentText(choice ChatCompletionChoice) string {
	if text := MessageContentText(choice.Delta.Content); text != "" {
		return text
	}
	return MessageContentText(choice.Message.Content)
}

// ModelListResponse represents the response from /v1/models
type ModelListResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model represents a single model in the list
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}
