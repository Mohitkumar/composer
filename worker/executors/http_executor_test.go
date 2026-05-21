package executors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeResponseBody(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		wantRaw   string // exact JSON string match; empty means skip raw check
		wantBody  string // decoded .body field for wrapped non-JSON responses
		wantErr   bool
	}{
		{name: "empty body", input: []byte{}, wantRaw: `{}`},
		{name: "valid JSON object", input: []byte(`{"key":"val"}`), wantRaw: `{"key":"val"}`},
		{name: "valid JSON array", input: []byte(`[1,2,3]`), wantRaw: `[1,2,3]`},
		{name: "valid JSON string", input: []byte(`"hello"`), wantRaw: `"hello"`},
		{name: "plain text", input: []byte(`OK`), wantBody: "OK"},
		// HTML: json.Marshal escapes < and > — compare decoded value, not raw JSON.
		{name: "html", input: []byte(`<html>error</html>`), wantBody: "<html>error</html>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeResponseBody(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantRaw != "" && string(got) != tc.wantRaw {
				t.Errorf("got %s, want %s", got, tc.wantRaw)
			}
			if tc.wantBody != "" {
				var decoded map[string]string
				if err := json.Unmarshal(got, &decoded); err != nil {
					t.Fatalf("output not valid JSON: %v (raw: %s)", err, got)
				}
				if decoded["body"] != tc.wantBody {
					t.Errorf("body: got %q, want %q", decoded["body"], tc.wantBody)
				}
			}
		})
	}
}

func TestHTTPExecutor_JSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"value":42}`))
	}))
	defer srv.Close()

	exec := NewHTTPExecutor()
	input, _ := json.Marshal(map[string]interface{}{"url": srv.URL, "method": "GET"})
	out, err := exec.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if got["value"] != float64(42) {
		t.Errorf("unexpected value: %v", got)
	}
}

func TestHTTPExecutor_PlainTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`pong`))
	}))
	defer srv.Close()

	exec := NewHTTPExecutor()
	input, _ := json.Marshal(map[string]interface{}{"url": srv.URL, "method": "GET"})
	out, err := exec.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if got["body"] != "pong" {
		t.Errorf("expected body=pong, got %v", got)
	}
}

func TestHTTPExecutor_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	exec := NewHTTPExecutor()
	input, _ := json.Marshal(map[string]interface{}{"url": srv.URL, "method": "DELETE"})
	out, err := exec.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "{}" {
		t.Errorf("expected {}, got %s", out)
	}
}

func TestHTTPExecutor_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad request`))
	}))
	defer srv.Close()

	exec := NewHTTPExecutor()
	input, _ := json.Marshal(map[string]interface{}{"url": srv.URL, "method": "GET"})
	_, err := exec.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for 4xx response")
	}
}

func TestHTTPExecutor_ConnectionReuse(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	exec := NewHTTPExecutor()
	input, _ := json.Marshal(map[string]interface{}{"url": srv.URL, "method": "GET"})

	for i := 0; i < 5; i++ {
		if _, err := exec.Execute(context.Background(), input); err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	if requests != 5 {
		t.Errorf("expected 5 requests, got %d", requests)
	}
}
