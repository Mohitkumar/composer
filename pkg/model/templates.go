package model

import (
	"database/sql"
	"time"
)

// NodeTemplate represents a reusable node template
type NodeTemplate struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description sql.NullString `json:"description,omitempty"`
	Runtime     string         `json:"runtime"`
	NodeType    string         `json:"node_type"`
	SpecJSON    []byte         `json:"spec_json"`
	Category    sql.NullString `json:"category,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// WorkflowTemplate represents a reusable workflow template
type WorkflowTemplate struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description sql.NullString `json:"description,omitempty"`
	SpecJSON    []byte         `json:"spec_json"`
	Category    sql.NullString `json:"category,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}
