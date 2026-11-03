package mock

import (
	"context"
	"fmt"
	"math"

	"github.com/Fraancuus/engram"
)

// Cosine returns the cosine similarity of a and b, or 0 when either has zero norm.
// Vectors of unequal length are compared over the shorter prefix.
func Cosine(a, b engram.Vector) float64 {
	var dot, na, nb float64
	for i := 0; i < len(a) && i < len(b); i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	for _, x := range a {
		na += float64(x) * float64(x)
	}
	for _, x := range b {
		nb += float64(x) * float64(x)
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// LookupEmbedder is an engram.Embedder that returns pre-registered vectors by exact
// text. It has no mutex: register everything before use; it is read-only afterwards.
type LookupEmbedder struct {
	vecs map[string]engram.Vector
}

// NewLookupEmbedder returns an empty LookupEmbedder.
func NewLookupEmbedder() *LookupEmbedder {
	return &LookupEmbedder{vecs: map[string]engram.Vector{}}
}

// Register maps content to a copy of v.
func (e *LookupEmbedder) Register(content string, v engram.Vector) {
	e.vecs[content] = append(engram.Vector(nil), v...)
}

// Embed returns the vector registered for text, or an error naming the text.
func (e *LookupEmbedder) Embed(_ context.Context, text string) (engram.Vector, error) {
	v, ok := e.vecs[text]
	if !ok {
		return nil, fmt.Errorf("embed %q: text not registered", text)
	}
	return v, nil
}

// CosineReranker is an engram.Reranker that scores docs by cosine similarity to the
// query over Embedder's vectors. It never consults labels.
type CosineReranker struct{ Embedder *LookupEmbedder }

// Rerank returns one cosine score per doc, aligned with docs.
func (r CosineReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	q, err := r.Embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("rerank query: %w", err)
	}
	out := make([]float64, len(docs))
	for i, d := range docs {
		dv, err := r.Embedder.Embed(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("rerank doc %d: %w", i, err)
		}
		out[i] = Cosine(q, dv)
	}
	return out, nil
}
