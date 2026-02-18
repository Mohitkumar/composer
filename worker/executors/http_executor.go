package executors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPExecutor executes HTTP requests
type HTTPExecutor struct{}

// NewHTTPExecutor creates a new HTTP executor
func NewHTTPExecutor() *HTTPExecutor {
	return &HTTPExecutor{}
}

// Type returns the executor type
func (e *HTTPExecutor) Type() string {
	return "HTTP"
}

// Execute executes an HTTP request based on input JSON
// Expected input format:
//
//	{
//	  "url": "https://example.com/api",
//	  "method": "GET|POST|PUT|DELETE",
//	  "body": {...},
//	  "headers": {"Authorization": "Bearer token"},
//	  "timeout_seconds": 30
//	}
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

	// Set headers
	for k, v := range params.Headers {
		req.Header.Set(k, v)
	}

	// Set content-type for POST/PUT requests
	if (params.Method == "POST" || params.Method == "PUT" || params.Method == "PATCH") && len(params.Body) > 0 {
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	client := &http.Client{
		Timeout: timeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(out))
	}

	// Return raw bytes as json.RawMessage
	return json.RawMessage(out), nil
}
