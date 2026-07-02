package inference_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Fraancuus/engram/inference"
)

const testInput = "hello"

// vec builds an n-length deterministic embedding fixture.
func vec(n int) []float32 {
	v := make([]float32, n)
	for i := range v {
		v[i] = float32(i) / float32(n)
	}
	return v
}

func jsonBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return b
}

// teiStub is a fake TEI sidecar that asserts the request shape (POST /embed with an
// {"inputs": ...} body) and replies with the given status and raw body.
func teiStub(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("request path = %q, want /embed", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("request method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("request Content-Type = %q, want application/json", ct)
		}
		var req struct {
			Inputs string `json:"inputs"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("request body not JSON: %v", err)
		}
		if req.Inputs != testInput {
			t.Errorf("request inputs = %q, want %q", req.Inputs, testInput)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientEmbed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		body    []byte
		wantLen int
		wantErr bool
	}{
		{
			name:    "happy path 384-dim",
			status:  http.StatusOK,
			body:    jsonBytes(t, [][]float32{vec(384)}),
			wantLen: 384,
		},
		{
			name:    "tei error status",
			status:  http.StatusServiceUnavailable,
			body:    []byte("model backend crashed"),
			wantErr: true,
		},
		{
			name:    "malformed json",
			status:  http.StatusOK,
			body:    []byte("this is not json"),
			wantErr: true,
		},
		{
			name:    "dimension mismatch",
			status:  http.StatusOK,
			body:    jsonBytes(t, [][]float32{vec(5)}),
			wantErr: true,
		},
		{
			name:    "wrong vector count",
			status:  http.StatusOK,
			body:    jsonBytes(t, [][]float32{vec(384), vec(384)}),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := teiStub(t, tt.status, tt.body)
			got, err := inference.New(srv.URL).Embed(context.Background(), testInput)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Embed: want error, got nil (vector len %d)", len(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("Embed: unexpected error: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("Embed: vector len = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

// TestClientEmbedErrorOmitsBody verifies the sidecar's response body never leaks into
// the error crossing back to the caller (no internal/info leak across the boundary),
// on both the status-error path and the decode-error path. The secret is non-JSON, so
// on a 200 it also exercises the decoder's failure path.
func TestClientEmbedErrorOmitsBody(t *testing.T) {
	t.Parallel()
	const secret = "internal-stacktrace-and-model-path"
	tests := []struct {
		name   string
		status int
	}{
		{"status error path", http.StatusServiceUnavailable},
		{"decode error path", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := teiStub(t, tt.status, []byte(secret))
			_, err := inference.New(srv.URL).Embed(context.Background(), testInput)
			if err == nil {
				t.Fatal("Embed: want error, got nil")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error leaks sidecar body: %q", err.Error())
			}
		})
	}
}

// TestClientEmbedContextCanceled verifies a cancelled context aborts the call and the
// wrapped error still unwraps to context.Canceled.
func TestClientEmbedContextCanceled(t *testing.T) {
	t.Parallel()
	srv := teiStub(t, http.StatusOK, jsonBytes(t, [][]float32{vec(384)}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := inference.New(srv.URL).Embed(ctx, testInput)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Embed: want error unwrapping to context.Canceled, got %v", err)
	}
}

// TestClientEmbedDeadlineExceeded verifies an expired deadline aborts the call and the
// wrapped error unwraps to context.DeadlineExceeded (distinct from Canceled).
func TestClientEmbedDeadlineExceeded(t *testing.T) {
	t.Parallel()
	srv := teiStub(t, http.StatusOK, jsonBytes(t, [][]float32{vec(384)}))
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	_, err := inference.New(srv.URL).Embed(ctx, testInput)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Embed: want error unwrapping to context.DeadlineExceeded, got %v", err)
	}
}
