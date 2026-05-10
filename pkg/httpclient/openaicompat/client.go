package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Client handles communication with OpenAI-compatible HTTP endpoints.
type Client struct {
	baseURL           string
	apiKey            string
	oauthTokenURL     string
	oauthClientID     string
	oauthClientSecret string
	httpClient        *http.Client
	retryPolicy       RetryPolicy

	tokenMu        sync.Mutex
	cachedToken    string
	tokenExpiresAt time.Time
}

type RetryPolicy struct {
	MaxAttempts int
	Backoff     time.Duration
}

type ClientOptions struct {
	APIKey            string
	OAuthTokenURL     string
	OAuthClientID     string
	OAuthClientSecret string
	HTTPClient        *http.Client
	Timeout           time.Duration
	RetryPolicy       RetryPolicy
}

type ResponseMetadata struct {
	StatusCode int
	RequestID  string
}

// NewClient creates a new Client.
func NewClient(baseURL, apiKeyEnv string) *Client {
	return NewClientWithOptions(baseURL, apiKeyEnv, ClientOptions{})
}

// NewClientWithOptions creates a new Client with transport and auth controls.
func NewClientWithOptions(baseURL, apiKeyEnv string, options ClientOptions) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL()
	}
	baseURL = strings.TrimRight(baseURL, "/")

	var apiKey string
	if apiKeyEnv != "" {
		apiKey = os.Getenv(apiKeyEnv)
	}
	if options.APIKey != "" {
		apiKey = options.APIKey
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		timeout := options.Timeout
		if timeout <= 0 {
			timeout = 120 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}

	return &Client{
		baseURL:           baseURL,
		apiKey:            apiKey,
		oauthTokenURL:     options.OAuthTokenURL,
		oauthClientID:     options.OAuthClientID,
		oauthClientSecret: options.OAuthClientSecret,
		httpClient:        httpClient,
		retryPolicy:       options.RetryPolicy,
	}
}

// DefaultBaseURL returns the default local base URL, typically Ollama.
func DefaultBaseURL() string {
	if url := os.Getenv("OPENAI_COMPATIBLE_BASE_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:11434/v1"
}

// SetAPIKey sets the literal API key.
func (c *Client) SetAPIKey(key string) {
	if key != "" {
		c.apiKey = key
	}
}

// SetOAuthCredentials configures 2-legged OAuth client credentials.
func (c *Client) SetOAuthCredentials(tokenURL, clientID, clientSecret string) {
	c.oauthTokenURL = tokenURL
	c.oauthClientID = clientID
	c.oauthClientSecret = clientSecret
}

// getBearerToken resolves the authentication token for requests.
// It prioritizes a static API key. If absent, it attempts to resolve an OAuth token.
func (c *Client) getBearerToken(ctx context.Context) (string, error) {
	if c.apiKey != "" {
		return c.apiKey, nil
	}
	if c.oauthTokenURL == "" || c.oauthClientID == "" || c.oauthClientSecret == "" {
		return "", nil // No auth configured
	}

	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	// Add a 5-second buffer before actual expiration
	if c.cachedToken != "" && time.Now().Add(5*time.Second).Before(c.tokenExpiresAt) {
		return c.cachedToken, nil
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", c.oauthClientID)
	data.Set("client_secret", c.oauthClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create oauth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("oauth token request returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // Usually in seconds
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode oauth token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("oauth token response contained no access token")
	}

	c.cachedToken = tokenResp.AccessToken
	expiresIn := time.Duration(tokenResp.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = 1 * time.Hour // Fallback if missing
	}
	c.tokenExpiresAt = time.Now().Add(expiresIn)

	return c.cachedToken, nil
}

// CreateChatCompletion sends a chat completion request to the server.
func (c *Client) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	resp, _, err := c.CreateChatCompletionWithMetadata(ctx, req)
	return resp, err
}

func (c *Client) CreateChatCompletionWithMetadata(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, ResponseMetadata, error) {
	var resp ChatCompletionResponse
	metadata, err := c.postJSONWithMetadata(ctx, "/chat/completions", req, &resp)
	if err != nil {
		return nil, metadata, err
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return &resp, metadata, &APIError{Kind: ErrorKindAPI, Operation: "chat.completions", StatusCode: metadata.StatusCode, Message: resp.Error.Message, Type: resp.Error.Type, Param: resp.Error.Param, Code: resp.Error.Code}
	}
	return &resp, metadata, nil
}

// StreamChatCompletion sends a streaming chat completion request and yields responses.
func (c *Client) StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) (<-chan *ChatCompletionResponse, <-chan error, error) {
	req.Stream = true

	data, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	token, err := c.getBearerToken(ctx)
	if err != nil {
		return nil, nil, err
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("http request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		apiErr := apiErrorFromResponse(http.MethodPost, c.baseURL+"/chat/completions", resp.StatusCode, body)
		return nil, nil, apiErr
	}

	responses := make(chan *ChatCompletionResponse, 100)
	errors := make(chan error, 1)

	go func() {
		defer func() { _ = resp.Body.Close() }()
		defer close(responses)
		defer close(errors)

		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err != io.EOF {
					errors <- err
				}
				return
			}

			lineStr := strings.TrimSpace(string(line))
			if lineStr == "" {
				continue
			}

			if !strings.HasPrefix(lineStr, "data: ") {
				continue
			}

			dataStr := strings.TrimPrefix(lineStr, "data: ")
			if dataStr == "[DONE]" {
				return
			}

			var streamResp ChatCompletionResponse
			if err := json.Unmarshal([]byte(dataStr), &streamResp); err != nil {
				errors <- fmt.Errorf("failed to unmarshal stream chunk: %w", err)
				return
			}

			select {
			case <-ctx.Done():
				return
			case responses <- &streamResp:
			}
		}
	}()

	return responses, errors, nil
}

// CreateEmbeddings sends an embeddings request to the server.
func (c *Client) CreateEmbeddings(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error) {
	resp, _, err := c.CreateEmbeddingsWithMetadata(ctx, req)
	return resp, err
}

func (c *Client) CreateEmbeddingsWithMetadata(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, ResponseMetadata, error) {
	var resp EmbeddingResponse
	metadata, err := c.postJSONWithMetadata(ctx, "/embeddings", req, &resp)
	if err != nil {
		return nil, metadata, err
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return &resp, metadata, &APIError{Kind: ErrorKindAPI, Operation: "embeddings", StatusCode: metadata.StatusCode, Message: resp.Error.Message, Type: resp.Error.Type, Param: resp.Error.Param, Code: resp.Error.Code}
	}
	return &resp, metadata, nil
}

// ListModels sends a GET request to the /v1/models endpoint.
func (c *Client) ListModels(ctx context.Context) (*ModelListResponse, error) {
	body, err := c.doJSON(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}

	var listResp ModelListResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&listResp); err != nil {
		return nil, &APIError{Kind: ErrorKindDecode, Operation: "models.list", Cause: err}
	}

	return &listResp, nil
}

func (c *Client) postJSONWithMetadata(ctx context.Context, path string, payload any, responseTarget any) (ResponseMetadata, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return ResponseMetadata{}, fmt.Errorf("failed to marshal payload: %w", err)
	}
	body, metadata, err := c.doJSONWithMetadata(ctx, http.MethodPost, path, data)
	if err != nil {
		return metadata, err
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(responseTarget); err != nil {
		return metadata, &APIError{Kind: ErrorKindDecode, Operation: strings.Trim(path, "/"), StatusCode: metadata.StatusCode, Cause: err}
	}

	return metadata, nil
}

func (c *Client) doJSON(ctx context.Context, method string, path string, data []byte) ([]byte, error) {
	body, _, err := c.doJSONWithMetadata(ctx, method, path, data)
	return body, err
}

func (c *Client) doJSONWithMetadata(ctx context.Context, method string, path string, data []byte) ([]byte, ResponseMetadata, error) {
	attempts := c.retryPolicy.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	if attempts > 10 {
		attempts = 10
	}
	var lastErr error
	var lastMetadata ResponseMetadata
	for attempt := 1; attempt <= attempts; attempt++ {
		body, metadata, retryable, err := c.doJSONOnce(ctx, method, path, data)
		lastMetadata = metadata
		if err == nil {
			return body, metadata, nil
		}
		lastErr = err
		if !retryable || attempt == attempts {
			break
		}
		backoff := c.retryPolicy.Backoff
		if backoff <= 0 {
			backoff = 100 * time.Millisecond
		}
		timer := time.NewTimer(backoff * time.Duration(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, lastMetadata, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastMetadata, lastErr
}

func (c *Client) doJSONOnce(ctx context.Context, method string, path string, data []byte) ([]byte, ResponseMetadata, bool, error) {
	var body io.Reader
	if data != nil {
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, ResponseMetadata{}, false, &APIError{Kind: ErrorKindRequest, Method: method, URL: c.baseURL + path, Cause: err}
	}

	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	token, err := c.getBearerToken(ctx)
	if err != nil {
		return nil, ResponseMetadata{}, false, &APIError{Kind: ErrorKindAuth, Method: method, URL: c.baseURL + path, Cause: err}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		apiErr := &APIError{Kind: ErrorKindTransport, Method: method, URL: c.baseURL + path, Cause: err, Retryable: true}
		return nil, ResponseMetadata{}, true, apiErr
	}
	defer func() { _ = resp.Body.Close() }()
	metadata := responseMetadata(resp)
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		apiErr := apiErrorFromResponse(method, c.baseURL+path, resp.StatusCode, respBody)
		return nil, metadata, apiErr.Retryable, apiErr
	}
	return respBody, metadata, false, nil
}

func responseMetadata(resp *http.Response) ResponseMetadata {
	if resp == nil {
		return ResponseMetadata{}
	}
	return ResponseMetadata{
		StatusCode: resp.StatusCode,
		RequestID:  firstNonEmptyHeader(resp.Header, "x-request-id", "openai-request-id", "x-requestid", "request-id"),
	}
}

func firstNonEmptyHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func apiErrorFromResponse(method string, requestURL string, statusCode int, body []byte) *APIError {
	var apiErr Error
	if jsonErr := json.Unmarshal(body, &apiErr); jsonErr == nil && apiErr.Message != "" {
		return &APIError{
			Kind:       ErrorKindAPI,
			Method:     method,
			URL:        requestURL,
			StatusCode: statusCode,
			Message:    apiErr.Message,
			Type:       apiErr.Type,
			Param:      apiErr.Param,
			Code:       apiErr.Code,
			Body:       string(body),
			Retryable:  retryableStatus(statusCode),
		}
	}
	var wrapped struct {
		Error Error `json:"error"`
	}
	if jsonErr := json.Unmarshal(body, &wrapped); jsonErr == nil && wrapped.Error.Message != "" {
		return &APIError{
			Kind:       ErrorKindAPI,
			Method:     method,
			URL:        requestURL,
			StatusCode: statusCode,
			Message:    wrapped.Error.Message,
			Type:       wrapped.Error.Type,
			Param:      wrapped.Error.Param,
			Code:       wrapped.Error.Code,
			Body:       string(body),
			Retryable:  retryableStatus(statusCode),
		}
	}
	return &APIError{
		Kind:       ErrorKindHTTP,
		Method:     method,
		URL:        requestURL,
		StatusCode: statusCode,
		Message:    string(body),
		Body:       string(body),
		Retryable:  retryableStatus(statusCode),
	}
}
