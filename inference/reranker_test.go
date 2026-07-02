package inference_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Fraancuus/engram/inference"
)

const rerankQuery = "the query"

func rerankStub(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Errorf("path = %q, want /rerank", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var req struct {
			Query string   `json:"query"`
			Texts []string `json:"texts"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("body not JSON: %v", err)
		}
		if req.Query != rerankQuery {
			t.Errorf("query = %q, want %q", req.Query, rerankQuery)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// rerankBody builds a TEI /rerank reply: a list of {index, score}.
func rerankBody(t *testing.T, pairs [][2]float64) []byte {
	t.Helper()
	type item struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	}
	items := make([]item, len(pairs))
	for i, p := range pairs {
		items[i] = item{Index: int(p[0]), Score: p[1]}
	}
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return b
}

func TestRerankerRealignsToInputOrder(t *testing.T) {
	t.Parallel()
	// The sidecar returns scores out of order; the client must realign to input order.
	srv := rerankStub(t, http.StatusOK, rerankBody(t, [][2]float64{{2, 0.9}, {0, 0.1}, {1, 0.5}}))
	got, err := inference.NewReranker(srv.URL).Rerank(context.Background(), rerankQuery, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	want := []float64{0.1, 0.5, 0.9}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("Rerank = %v, want %v (realigned to input order)", got, want)
	}
}

func TestRerankerEmptyDocs(t *testing.T) {
	t.Parallel()
	// No HTTP call for empty docs (unreachable URL proves it).
	got, err := inference.NewReranker("http://127.0.0.1:0").Rerank(context.Background(), rerankQuery, nil)
	if err != nil {
		t.Fatalf("Rerank(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Rerank(empty) = %v, want empty", got)
	}
}

func TestRerankerErrorStatusOmitsBody(t *testing.T) {
	t.Parallel()
	const secret = "reranker-internal-detail"
	srv := rerankStub(t, http.StatusServiceUnavailable, []byte(secret))
	_, err := inference.NewReranker(srv.URL).Rerank(context.Background(), rerankQuery, []string{"a", "b"})
	if err == nil {
		t.Fatal("want error on 503")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks body: %q", err.Error())
	}
}

func TestRerankerCountMismatch(t *testing.T) {
	t.Parallel()
	srv := rerankStub(t, http.StatusOK, rerankBody(t, [][2]float64{{0, 0.1}, {1, 0.5}})) // 2 for 3 docs
	_, err := inference.NewReranker(srv.URL).Rerank(context.Background(), rerankQuery, []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("want error on score/doc count mismatch")
	}
}

func TestRerankerMalformedJSON(t *testing.T) {
	t.Parallel()
	srv := rerankStub(t, http.StatusOK, []byte("not json"))
	_, err := inference.NewReranker(srv.URL).Rerank(context.Background(), rerankQuery, []string{"a", "b"})
	if err == nil {
		t.Fatal("want error on malformed json")
	}
}
