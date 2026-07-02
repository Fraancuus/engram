package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Fraancuus/engram"
)

// defaultDim is the embedding width Engram expects from the sidecar: BGE-small-en-v1.5
// is 384-dim. A response of any other width is rejected rather than silently stored.
const defaultDim = 384

// defaultTimeout bounds a single embed call so a wedged sidecar cannot hang a caller
// whose context carries no deadline of its own.
const defaultTimeout = 30 * time.Second

// maxResponseBytes caps how much of the sidecar response is read. A valid 384-float
// embedding is a few KB; this is generous headroom while still refusing to allocate an
// unbounded body from a buggy or misbehaving sidecar.
const maxResponseBytes = 1 << 20 // 1 MiB

var _ engram.Embedder = (*Client)(nil)

// Client is an engram.Embedder backed by an HF Text Embeddings Inference (TEI) sidecar
// over HTTP. Construct it with New; it is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	dim        int
}

// New returns a Client that talks to the TEI sidecar at baseURL (e.g.
// "http://localhost:8080") and expects 384-dim embeddings.
func New(baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		baseURL:    baseURL,
		dim:        defaultDim,
	}
}

// embedRequest is the TEI /embed request payload.
type embedRequest struct {
	Inputs string `json:"inputs"`
}

// Embed returns the embedding of text from the TEI sidecar. It validates that the
// sidecar returned exactly one vector of the expected dimension, so a model or index
// mismatch fails loudly instead of corrupting stored memories. Errors are wrapped with
// what was being attempted and never echo the sidecar's response body.
func (c *Client) Embed(ctx context.Context, text string) (engram.Vector, error) {
	body, err := json.Marshal(embedRequest{Inputs: text})
	if err != nil {
		return nil, fmt.Errorf("embed marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Drain so the HTTP transport can reuse the connection under sustained errors.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("embed: tei returned status %d", resp.StatusCode)
	}

	var out [][]float32 // TEI returns one vector per input
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed decode response: %w", err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("embed: want 1 vector, got %d", len(out))
	}
	if len(out[0]) != c.dim {
		return nil, fmt.Errorf("embed: want %d-dim vector, got %d", c.dim, len(out[0]))
	}
	return engram.Vector(out[0]), nil
}
