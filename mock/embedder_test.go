package mock

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/Fraancuus/engram"
)

func TestLookupEmbedderUnknown(t *testing.T) {
	t.Parallel()
	_, err := NewLookupEmbedder().Embed(context.Background(), "never registered")
	if err == nil || !strings.Contains(err.Error(), "never registered") {
		t.Fatalf("err = %v, want error containing the text", err)
	}
}

func TestLookupEmbedderRegisterCopies(t *testing.T) {
	t.Parallel()
	e := NewLookupEmbedder()
	v := engram.Vector{1, 2}
	e.Register("a", v)
	v[0] = 99
	got, err := e.Embed(context.Background(), "a")
	if err != nil || got[0] != 1 {
		t.Fatalf("got %v, %v; want [1 2] (copy)", got, err)
	}
}

func TestCosineRerankerAlignment(t *testing.T) {
	t.Parallel()
	e := NewLookupEmbedder()
	q, d1, d2 := engram.Vector{1, 0}, engram.Vector{1, 1}, engram.Vector{0, 1}
	e.Register("q", q)
	e.Register("d1", d1)
	e.Register("d2", d2)
	got, err := CosineReranker{Embedder: e}.Rerank(context.Background(), "q", []string{"d1", "d2"})
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{Cosine(q, d1), Cosine(q, d2)}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := (CosineReranker{Embedder: e}).Rerank(context.Background(), "q", []string{"missing"}); err == nil {
		t.Fatal("want error for unregistered doc")
	}
}

func TestCosineZeroNorm(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b engram.Vector
		want float64
	}{
		{"zero a", engram.Vector{0, 0}, engram.Vector{1, 1}, 0},
		{"zero b", engram.Vector{1, 1}, engram.Vector{0, 0}, 0},
		{"identical", engram.Vector{3, 4}, engram.Vector{3, 4}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Cosine(tt.a, tt.b); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("Cosine = %v, want %v", got, tt.want)
			}
		})
	}
}
