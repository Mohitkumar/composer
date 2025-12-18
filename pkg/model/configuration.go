package model

import (
	"database/sql"
	"time"
)

// Configuration represents a reusable configuration template
type Configuration struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description sql.NullString `json:"description,omitempty"`
	Type        string         `json:"type"` // s3, http, database, api_key, etc.
	ConfigJSON  []byte         `json:"config_json"`
	CreatedBy   sql.NullString `json:"created_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// ConfigurationResponse is the API response format for configurations
type ConfigurationResponse struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description *string        `json:"description,omitempty"`
	Type        string         `json:"type"`
	ConfigJSON  map[string]any `json:"config_json"`
	CreatedBy   *string        `json:"created_by,omitempty"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

// CreateConfigurationRequest represents a request to create a new configuration
type CreateConfigurationRequest struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Type        string         `json:"type"`
	ConfigJSON  map[string]any `json:"config_json"`
	CreatedBy   string         `json:"created_by,omitempty"`
}

// UpdateConfigurationRequest represents a request to update a configuration
type UpdateConfigurationRequest struct {
	Name        *string        `json:"name,omitempty"`
	Description *string        `json:"description,omitempty"`
	ConfigJSON  map[string]any `json:"config_json,omitempty"`
}
