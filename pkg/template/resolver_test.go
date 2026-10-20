package template_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/template"
)

func newContext() *model.RunContext {
	rc := model.NewRunContext(json.RawMessage(`{"user": {"id": 7, "name": "ada"}, "tags": ["a", "b"]}`),
		json.RawMessage(`{"region": "eu"}`))
	rc.Tasks["fetch"] = &model.StepResult{Status: model.TaskInstanceStatusCompleted,
		Output: json.RawMessage(`{"count": 3, "items": [1, 2, 3]}`)}
	return rc
}

func resolve(t *testing.T, spec any) any {
	t.Helper()
	r, err := template.NewResolver()
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.ResolveTemplates(spec, newContext())
	if err != nil {
		t.Fatalf("resolve %v: %v", spec, err)
	}
	return out
}

func TestWholeStringTemplateKeepsItsType(t *testing.T) {
	cases := map[string]any{
		"{{input.user.id}}":                float64(7),
		"{{ input.user }}":                 map[string]any{"id": float64(7), "name": "ada"},
		"{{tasks.fetch.output.count > 2}}": true,
		"{{tasks.fetch.status}}":           "COMPLETED",
		"{{config.region}}":                "eu",
	}
	for expr, want := range cases {
		got := resolve(t, expr)
		// CEL returns native Go maps/slices; compare through JSON for map shapes.
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		if string(gj) != string(wj) {
			t.Errorf("%s: got %s, want %s", expr, gj, wj)
		}
	}
}

func TestTemplatesInsideTextAreInterpolated(t *testing.T) {
	got := resolve(t, "user {{input.user.name}} has {{tasks.fetch.output.count}} items: {{input.tags}}")
	if got != `user ada has 3 items: ["a","b"]` {
		t.Fatalf("got %q", got)
	}
}

func TestNestedStructuresAreResolvedRecursively(t *testing.T) {
	got := resolve(t, map[string]any{
		"url":     "https://api/{{input.user.id}}",
		"headers": map[string]any{"X-Region": "{{config.region}}"},
		"ids":     []any{"{{input.user.id}}", 42, true},
	})
	want := map[string]any{
		"url":     "https://api/7",
		"headers": map[string]any{"X-Region": "eu"},
		"ids":     []any{float64(7), 42, true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestPlainValuesPassThrough(t *testing.T) {
	for _, v := range []any{"no templates here", 3.5, nil, false} {
		if got := resolve(t, v); !reflect.DeepEqual(got, v) {
			t.Errorf("got %#v, want %#v", got, v)
		}
	}
}

func TestErrors(t *testing.T) {
	r, _ := template.NewResolver()
	for _, expr := range []string{
		"{{input.user.}}",            // parse error
		"{{tasks.missing.output.x}}", // no such task
		"{{unknownVar}}",             // undeclared variable
	} {
		if _, err := r.ResolveTemplates(expr, newContext()); err == nil {
			t.Errorf("%s: expected an error", expr)
		}
	}
}
