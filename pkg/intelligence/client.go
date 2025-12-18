package intelligence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Config configures the intelligence client
type Config struct {
	AgentURL string        `yaml:"agent_url" mapstructure:"agent_url"`
	Timeout  time.Duration `yaml:"timeout" mapstructure:"timeout"`
}

// Client provides methods to interact with the AI intelligence agent
type Client struct {
	config Config
	logger zerolog.Logger
	client *http.Client
}

// NewClient creates a new intelligence client
func NewClient(config Config, logger zerolog.Logger) *Client {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &Client{
		config: config,
		logger: logger,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// SummarizeErrorsRequest represents the request to summarize errors
type SummarizeErrorsRequest struct {
	WorkflowID string   `json:"workflow_id"`
	Errors     []string `json:"errors"`
}

// SummarizeErrorsResponse represents the response from the AI agent
type SummarizeErrorsResponse struct {
	Summary string `json:"summary"`
}

// SummarizeErrors calls the AI agent to summarize workflow errors
func (c *Client) SummarizeErrors(ctx context.Context, workflowID string, errors []string) (string, error) {
	if c.config.AgentURL == "" {
		return "", fmt.Errorf("AI agent URL is not configured")
	}

	// Prepare request payload
	payload := SummarizeErrorsRequest{
		WorkflowID: workflowID,
		Errors:     errors,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make HTTP request to AI agent
	url := c.config.AgentURL + "/summarize-errors"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadJSON))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	c.logger.Debug().
		Str("url", url).
		Str("workflow_id", workflowID).
		Int("error_count", len(errors)).
		Msg("calling AI agent to summarize errors")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call AI agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI agent returned status %d", resp.StatusCode)
	}

	var result SummarizeErrorsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Info().
		Str("workflow_id", workflowID).
		Msg("successfully received summary from AI agent")

	return result.Summary, nil
}

// IsConfigured returns true if the AI agent URL is configured
func (c *Client) IsConfigured() bool {
	return c.config.AgentURL != ""
}
