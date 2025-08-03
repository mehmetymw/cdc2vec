package embeddings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestOllamaHTTP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Fatalf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{0.1, 0.2, 0.3}})
	}))
	defer ts.Close()

	logger := zap.NewNop()
	p := &ollamaHTTP{
		baseURL: ts.URL,
		model:   "test-model",
		http:    ts.Client(),
		logger:  logger,
	}

	vec, err := p.Embed("test text")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(vec))
	}

	expected := []float32{0.1, 0.2, 0.3}
	for i, v := range vec {
		if v != expected[i] {
			t.Fatalf("expected %f at index %d, got %f", expected[i], i, v)
		}
	}
}

func BenchmarkOllamaHTTP_Embed_Small(b *testing.B) {
	benchmarkOllamaEmbed(b, "Short text for embedding")
}

func BenchmarkOllamaHTTP_Embed_Medium(b *testing.B) {
	text := "This is a medium-length text that represents a typical document excerpt that might be processed in a real-world CDC scenario. It contains multiple sentences and should give us a good baseline for performance testing of the embedding generation process."
	benchmarkOllamaEmbed(b, text)
}

func BenchmarkOllamaHTTP_Embed_Large(b *testing.B) {

	text := ""
	baseText := "This is a longer text segment that represents a substantial document or article that might be encountered in a production change data capture scenario. It includes multiple paragraphs and complex sentence structures that will test the embedding model's ability to process larger content efficiently. "
	for i := 0; i < 10; i++ {
		text += baseText
	}
	benchmarkOllamaEmbed(b, text)
}

func benchmarkOllamaEmbed(b *testing.B, text string) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vectorSize := min(1536, max(384, len(text)/4))
		embedding := make([]float32, vectorSize)
		for i := range embedding {
			embedding[i] = float32(i) * 0.001
		}
		json.NewEncoder(w).Encode(map[string]any{"embedding": embedding})
	}))
	defer ts.Close()

	logger := zap.NewNop()
	p := &ollamaHTTP{
		baseURL: ts.URL,
		model:   "benchmark-model",
		http:    ts.Client(),
		logger:  logger,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := p.Embed(text)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
