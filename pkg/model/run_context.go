package model

import "encoding/json"

type StepResult struct {
	Status TaskInstanceStatus `json:"status"`
	Output json.RawMessage    `json:"output,omitempty"`
	Err    error              `json:"-"`
}

// RunContext captures workflow-level data available to node templates.
// It is persisted on the workflow run and enriched as nodes complete.
type RunContext struct {
	Input  json.RawMessage        `json:"input"`
	Config json.RawMessage        `json:"config,omitempty"`
	Tasks  map[string]*StepResult `json:"tasks,omitempty"`
}
