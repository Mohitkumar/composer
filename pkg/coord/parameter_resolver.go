package coord

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

type ParameterResolver struct {
	celEnv     *cel.Env
	templateRe *regexp.Regexp
}

func NewParameterResolver() (*ParameterResolver, error) {
	pr := &ParameterResolver{
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
	pr.celEnv = env
	return pr, nil
}

func (pr *ParameterResolver) ResolveTemplates(node interface{}, rc *model.RunContext) (interface{}, error) {
	switch v := node.(type) {
	case string:
		return pr.resolveStringTemplates(v, rc)
	case map[string]interface{}:
		m2 := make(map[string]interface{}, len(v))
		for k, vv := range v {
			res, err := pr.ResolveTemplates(vv, rc)
			if err != nil {
				return nil, err
			}
			m2[k] = res
		}
		return m2, nil
	case []interface{}:
		arr := make([]interface{}, len(v))
		for i, vv := range v {
			res, err := pr.ResolveTemplates(vv, rc)
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

func (pr *ParameterResolver) resolveStringTemplates(s string, rc *model.RunContext) (interface{}, error) {
	matches := pr.templateRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return s, nil
	}

	out := s
	for _, m := range matches {
		rawExpr := strings.TrimSpace(m[1])
		val, err := pr.evalCELExpression(rawExpr, rc)
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

func (pr *ParameterResolver) buildStepsView(rc *model.RunContext) map[string]interface{} {
	out := map[string]interface{}{}
	for id, res := range rc.Tasks {
		if res == nil {
			out[id] = nil
			continue
		}

		out[id] = map[string]interface{}{
			"output": res.Output,
			"status": string(res.Status),
		}
	}
	return out
}

func (pr *ParameterResolver) evalCELExpression(exprStr string, rc *model.RunContext) (interface{}, error) {
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

	ast, iss := pr.celEnv.Parse(exprStr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel parse error: %w", iss.Err())
	}

	checked, iss := pr.celEnv.Check(ast)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel check error: %w", iss.Err())
	}

	prg, err := pr.celEnv.Program(checked)
	if err != nil {
		return nil, err
	}

	// Prepare activation
	activation := map[string]interface{}{
		"input":  decodeJSON(rc.Input),
		"config": decodeJSON(rc.Config),
		"tasks":  pr.buildStepsView(rc),
		"system": map[string]interface{}{"now": time.Now().UTC().Format(time.RFC3339)},
	}

	out, _, err := prg.Eval(activation)
	if err != nil {
		return nil, fmt.Errorf("cel eval error: %w", err)
	}
	return out.Value(), nil
}
