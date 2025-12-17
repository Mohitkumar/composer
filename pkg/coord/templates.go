package coord

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/mohitkumar/composer/pkg/util"
)

// NodeTemplateResponse is the API response format for node templates
type NodeTemplateResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	Runtime     string          `json:"runtime"`
	NodeType    string          `json:"node_type"`
	SpecJSON    json.RawMessage `json:"spec_json"`
	Category    *string         `json:"category,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// WorkflowTemplateResponse is the API response format for workflow templates
type WorkflowTemplateResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	SpecJSON    json.RawMessage `json:"spec_json"`
	Category    *string         `json:"category,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// ListNodeTemplates lists all node templates, optionally filtered by runtime or category
func (c *Coordinator) ListNodeTemplates(ctx context.Context, runtime, category string) ([]NodeTemplateResponse, error) {
	ctx, span := util.StartSpan(ctx, "templates.list_node_templates")
	defer util.EndSpan(span, nil)

	query := `SELECT id, name, description, runtime, node_type, spec_json, category, tags, created_at, updated_at 
	          FROM node_templates WHERE 1=1`
	args := []interface{}{}
	argIndex := 1

	if runtime != "" {
		query += ` AND runtime = $` + strconv.Itoa(argIndex)
		args = append(args, runtime)
		argIndex++
	}

	if category != "" {
		query += ` AND category = $` + strconv.Itoa(argIndex)
		args = append(args, category)
		argIndex++
	}

	query += ` ORDER BY name, created_at DESC`

	rows, err := c.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var templates []NodeTemplateResponse
	for rows.Next() {
		var t NodeTemplateResponse
		var description, category sql.NullString
		var tags []string

		var createdAt, updatedAt time.Time
		err := rows.Scan(
			&t.ID, &t.Name, &description, &t.Runtime, &t.NodeType, &t.SpecJSON,
			&category, &tags, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}

		if description.Valid {
			t.Description = &description.String
		}
		if category.Valid {
			t.Category = &category.String
		}
		t.Tags = tags
		t.CreatedAt = createdAt.Format(time.RFC3339)
		t.UpdatedAt = updatedAt.Format(time.RFC3339)

		templates = append(templates, t)
	}

	return templates, rows.Err()
}

// GetNodeTemplate retrieves a node template by ID
func (c *Coordinator) GetNodeTemplate(ctx context.Context, id string) (*NodeTemplateResponse, error) {
	ctx, span := util.StartSpan(ctx, "templates.get_node_template",
		util.Attribute("template.id", id),
	)
	defer util.EndSpan(span, nil)

	var t NodeTemplateResponse
	var description, category sql.NullString
	var tags []string
	var createdAt, updatedAt time.Time

	err := c.DB.QueryRow(ctx, `SELECT id, name, description, runtime, node_type, spec_json, category, tags, created_at, updated_at 
	                            FROM node_templates WHERE id = $1`, id).Scan(
		&t.ID, &t.Name, &description, &t.Runtime, &t.NodeType, &t.SpecJSON,
		&category, &tags, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("node template not found")
		}
		return nil, err
	}

	if description.Valid {
		t.Description = &description.String
	}
	if category.Valid {
		t.Category = &category.String
	}
	t.Tags = tags
	t.CreatedAt = createdAt.Format(time.RFC3339)
	t.UpdatedAt = updatedAt.Format(time.RFC3339)

	return &t, nil
}

// ListWorkflowTemplates lists all workflow templates, optionally filtered by category
func (c *Coordinator) ListWorkflowTemplates(ctx context.Context, category string) ([]WorkflowTemplateResponse, error) {
	ctx, span := util.StartSpan(ctx, "templates.list_workflow_templates")
	defer util.EndSpan(span, nil)

	query := `SELECT id, name, description, spec_json, category, tags, created_at, updated_at 
	          FROM workflow_templates WHERE 1=1`
	args := []interface{}{}
	argIndex := 1

	if category != "" {
		query += ` AND category = $` + strconv.Itoa(argIndex)
		args = append(args, category)
		argIndex++
	}

	query += ` ORDER BY name, created_at DESC`

	rows, err := c.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var templates []WorkflowTemplateResponse
	for rows.Next() {
		var t WorkflowTemplateResponse
		var description, category sql.NullString
		var tags []string

		var createdAt, updatedAt time.Time
		err := rows.Scan(
			&t.ID, &t.Name, &description, &t.SpecJSON,
			&category, &tags, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}

		if description.Valid {
			t.Description = &description.String
		}
		if category.Valid {
			t.Category = &category.String
		}
		t.Tags = tags
		t.CreatedAt = createdAt.Format(time.RFC3339)
		t.UpdatedAt = updatedAt.Format(time.RFC3339)

		templates = append(templates, t)
	}

	return templates, rows.Err()
}

// GetWorkflowTemplate retrieves a workflow template by ID
func (c *Coordinator) GetWorkflowTemplate(ctx context.Context, id string) (*WorkflowTemplateResponse, error) {
	ctx, span := util.StartSpan(ctx, "templates.get_workflow_template",
		util.Attribute("template.id", id),
	)
	defer util.EndSpan(span, nil)

	var t WorkflowTemplateResponse
	var description, category sql.NullString
	var tags []string
	var createdAt, updatedAt time.Time

	err := c.DB.QueryRow(ctx, `SELECT id, name, description, spec_json, category, tags, created_at, updated_at 
	                            FROM workflow_templates WHERE id = $1`, id).Scan(
		&t.ID, &t.Name, &description, &t.SpecJSON,
		&category, &tags, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("workflow template not found")
		}
		return nil, err
	}

	if description.Valid {
		t.Description = &description.String
	}
	if category.Valid {
		t.Category = &category.String
	}
	t.Tags = tags
	t.CreatedAt = createdAt.Format(time.RFC3339)
	t.UpdatedAt = updatedAt.Format(time.RFC3339)

	return &t, nil
}
