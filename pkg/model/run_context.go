package model

import (
	"encoding/json"
	"fmt"
)

type StepResult struct {
	Status TaskInstanceStatus `json:"status"`
	Output json.RawMessage    `json:"output,omitempty"`
	// Branches records the successor node IDs a SWITCH selected, so join
	// decisions made later never depend on re-evaluating its conditions.
	Branches []string `json:"branches,omitempty"`
	Err      error    `json:"-"`
}

// RunContext captures workflow-level data available to node templates.
// It is persisted on the workflow run and enriched as nodes complete.
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

// DecodeRunContext rebuilds a RunContext from the persisted context_json,
// falling back to the run's input column when the context does not carry it.
func DecodeRunContext(input, contextJSON json.RawMessage) (*RunContext, error) {
	rc := &RunContext{Input: input}
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, rc); err != nil {
			return nil, fmt.Errorf("unmarshal run context: %w", err)
		}
	}
	if rc.Tasks == nil {
		rc.Tasks = map[string]*StepResult{}
	}
	if len(rc.Input) == 0 {
		rc.Input = input
	}
	return rc, nil
}
