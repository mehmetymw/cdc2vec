package embeddings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaHTTP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type","application/json")
		json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{0.1,0.2,0.3}})
	}))
	defer ts.Close()
	p := &ollamaHTTP{baseURL: ts.URL, model: "m", http: ts.Client()}
	vec, err := p.Embed("x")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 3 {
		t.Fatalf("len %d", len(vec))
	}
}
