package model

import (
	"encoding/json"
	"fmt"
)

type StepResult struct {
	Status TaskInstanceStatus `json:"status"`
	Output json.RawMessage    `json:"output,omitempty"`
	Err    error              `json:"-"`
}

// RunContext captures workflow-level data available to node templates.
// Only Input and Config are persisted on the workflow run; Tasks is filled on
// demand from task_instances so a run's row never grows with its outputs.
type RunContext struct {
	Input  json.RawMessage        `json:"input"`
	Config json.RawMessage        `json:"config,omitempty"`
	Tasks  map[string]*StepResult `json:"tasks,omitempty"`
}

// NewRunContext returns an empty context for a new run.
func NewRunContext(input, config json.RawMessage) *RunContext {
	return &RunContext{
		Input:  input,
		Config: config,
		Tasks:  map[string]*StepResult{},
	}
}

// Persisted returns the part of the context stored in workflow_runs.context_json.
func (rc *RunContext) Persisted() *RunContext {
	return &RunContext{Input: rc.Input, Config: rc.Config}
}

// DecodeRunContext rebuilds a RunContext from the persisted context_json,
// falling back to the run's input column when the context does not carry it.
// Task outputs found in older context_json rows are ignored: task_instances is
// the source of truth.
func DecodeRunContext(input, contextJSON json.RawMessage) (*RunContext, error) {
	rc := &RunContext{Input: input}
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, rc); err != nil {
			return nil, fmt.Errorf("unmarshal run context: %w", err)
		}
	}
	rc.Tasks = map[string]*StepResult{}
	if len(rc.Input) == 0 {
		rc.Input = input
	}
	return rc, nil
}
