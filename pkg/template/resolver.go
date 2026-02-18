package template

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/mohitkumar/composer/pkg/model"
)

// Resolver resolves template expressions in workflow parameters using CEL (Common Expression Language).
// It supports expressions like {{ input.field }} or {{ tasks.task_id.output }}.
type Resolver struct {
	celEnv     *cel.Env
	templateRe *regexp.Regexp
}

// NewResolver creates a new template resolver with CEL environment configured.
func NewResolver() (*Resolver, error) {
	r := &Resolver{
		templateRe: regexp.MustCompile(`{{\s*([^}]+)\s*}}`),
	}
	env, err := cel.NewEnv(
		cel.Declarations(
			decls.NewVar("input", decls.Dyn),
			decls.NewVar("config", decls.Dyn),
			decls.NewVar("tasks", decls.Dyn),
			decls.NewVar("system", decls.Dyn),
		),
	)
	if err != nil {
		return nil, err
	}
	r.celEnv = env
	return r, nil
}

// ResolveTemplates recursively resolves template expressions in the given node.
// It handles strings, maps, arrays, and other types.
// Template expressions use the format {{ expression }} where expression is a CEL expression.
func (r *Resolver) ResolveTemplates(node interface{}, rc *model.RunContext) (interface{}, error) {
	switch v := node.(type) {
	case string:
		return r.resolveStringTemplates(v, rc)
	case map[string]interface{}:
		m2 := make(map[string]interface{}, len(v))
		for k, vv := range v {
			res, err := r.ResolveTemplates(vv, rc)
			if err != nil {
				return nil, err
			}
			m2[k] = res
		}
		return m2, nil
	case []interface{}:
		arr := make([]interface{}, len(v))
		for i, vv := range v {
			res, err := r.ResolveTemplates(vv, rc)
			if err != nil {
				return nil, err
			}
			arr[i] = res
		}
		return arr, nil
	default:
		return v, nil
	}
}

// resolveStringTemplates resolves template expressions in a string.
// It finds all {{ expression }} patterns and replaces them with evaluated CEL expressions.
// If the entire string is just a template expression (with optional whitespace), it returns
// the evaluated value directly (as an object/array/etc) instead of stringifying it.
func (r *Resolver) resolveStringTemplates(s string, rc *model.RunContext) (interface{}, error) {
	matches := r.templateRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return s, nil
	}

	// Check if the entire string is just a single template expression (with optional whitespace)
	trimmed := strings.TrimSpace(s)
	if len(matches) == 1 {
		// Check if the string is exactly the template expression (with optional whitespace)
		fullMatch := strings.TrimSpace(matches[0][0])
		if trimmed == fullMatch {
			// Entire string is just the template - return the evaluated value directly
			rawExpr := strings.TrimSpace(matches[0][1])
			val, err := r.evalCELExpression(rawExpr, rc)
			if err != nil {
				return nil, err
			}
			return val, nil
		}
	}

	// Multiple templates or template is part of a larger string - do string replacement
	out := s
	for _, m := range matches {
		rawExpr := strings.TrimSpace(m[1])
		val, err := r.evalCELExpression(rawExpr, rc)
		if err != nil {
			return nil, err
		}
		// Convert val to string
		var valStr string
		switch t := val.(type) {
		case nil:
			valStr = ""
		case string:
			valStr = t
		default:
			b, err := json.Marshal(t)
			if err != nil {
				valStr = fmt.Sprintf("%v", t)
			} else {
				valStr = string(b)
			}
		}
		out = strings.Replace(out, m[0], valStr, 1)
	}
	return out, nil
}

// buildStepsView builds a view of completed tasks for use in CEL expressions.
// It provides access to task outputs and status via the "tasks" variable.
func (r *Resolver) buildStepsView(rc *model.RunContext) map[string]interface{} {
	out := map[string]interface{}{}
	for id, res := range rc.Tasks {
		if res == nil {
			out[id] = nil
			continue
		}

		// Decode json.RawMessage into interface{} for CEL evaluation
		var outputVal interface{}
		if len(res.Output) > 0 {
			if err := json.Unmarshal(res.Output, &outputVal); err != nil {
				outputVal = string(res.Output)
			}
		}

		out[id] = map[string]interface{}{
			"output": outputVal,
			"status": string(res.Status),
		}
	}
	return out
}

// evalCELExpression evaluates a CEL expression with the given run context.
// Available variables in expressions:
//   - input: The workflow input (from rc.Input)
//   - config: The workflow configuration (from rc.Config)
//   - tasks: Map of task results keyed by task/node ID
//   - system: System variables (e.g., system.now for current timestamp)
func (r *Resolver) evalCELExpression(exprStr string, rc *model.RunContext) (interface{}, error) {
	decodeJSON := func(raw json.RawMessage) interface{} {
		if len(raw) == 0 {
			return map[string]interface{}{}
		}
		var v interface{}
		if err := json.Unmarshal(raw, &v); err != nil {
			// fall back to string representation
			return string(raw)
		}
		return v
	}

	ast, iss := r.celEnv.Parse(exprStr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel parse error: %w", iss.Err())
	}

	checked, iss := r.celEnv.Check(ast)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel check error: %w", iss.Err())
	}

	prg, err := r.celEnv.Program(checked)
	if err != nil {
		return nil, err
	}

	// Prepare activation with available variables
	activation := map[string]interface{}{
		"input":  decodeJSON(rc.Input),
		"config": decodeJSON(rc.Config),
		"tasks":  r.buildStepsView(rc),
		"system": map[string]interface{}{"now": time.Now().UTC().Format(time.RFC3339)},
	}

	out, _, err := prg.Eval(activation)
	if err != nil {
		return nil, fmt.Errorf("cel eval error: %w", err)
	}
	return out.Value(), nil
}
