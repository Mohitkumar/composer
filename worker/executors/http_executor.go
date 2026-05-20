package executors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// HTTPExecutor executes HTTP requests using a shared, pooled transport so TCP
// and TLS sessions are reused across tasks.
type HTTPExecutor struct {
	client *http.Client
}

// NewHTTPExecutor creates a new HTTP executor with a shared connection pool.
func NewHTTPExecutor() *HTTPExecutor {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &HTTPExecutor{
		// No global Timeout — each request carries its own context deadline.
		client: &http.Client{Transport: transport},
	}
}

// Type returns the executor type.
func (e *HTTPExecutor) Type() string {
	return "HTTP"
}

// Execute executes an HTTP request based on input JSON.
//
// Expected input format:
//
//	{
//	  "url": "https://example.com/api",
//	  "method": "GET|POST|PUT|DELETE|PATCH",
//	  "body": {...},
//	  "headers": {"Authorization": "Bearer token"},
//	  "timeout_seconds": 30
//	}
//
// The response body is returned as-is if it is valid JSON.
// Non-JSON responses (plain text, HTML, etc.) are wrapped as {"body": "<text>"}.
// Empty responses return {}.
func (e *HTTPExecutor) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var params struct {
		URL            string            `json:"url"`
		Method         string            `json:"method"`
		Body           json.RawMessage   `json:"body,omitempty"`
		Headers        map[string]string `json:"headers,omitempty"`
		TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("failed to parse HTTP request params: %w", err)
	}

	if params.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if params.Method == "" {
		params.Method = "GET"
	}

	timeout := 30 * time.Second
	if params.TimeoutSeconds > 0 {
		timeout = time.Duration(params.TimeoutSeconds) * time.Second
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if len(params.Body) > 0 {
		body = bytes.NewReader(params.Body)
	}

	req, err := http.NewRequestWithContext(reqCtx, params.Method, params.URL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	for k, v := range params.Headers {
		req.Header.Set(k, v)
	}
	if (params.Method == "POST" || params.Method == "PUT" || params.Method == "PATCH") && len(params.Body) > 0 {
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(out))
	}

	return normalizeResponseBody(out)
}

// normalizeResponseBody ensures the output is always valid JSON.
// Valid JSON is returned as-is. Non-JSON and empty bodies are wrapped.
func normalizeResponseBody(body []byte) (json.RawMessage, error) {
	if len(body) == 0 {
		return json.RawMessage("{}"), nil
	}
	if json.Valid(body) {
		return json.RawMessage(body), nil
	}
	// Wrap plain text / HTML / binary in a JSON object so downstream
	// CEL expressions can access it via .body
	wrapped, err := json.Marshal(map[string]string{"body": string(body)})
	if err != nil {
		return nil, fmt.Errorf("failed to encode non-JSON response body: %w", err)
	}
	return json.RawMessage(wrapped), nil
}
