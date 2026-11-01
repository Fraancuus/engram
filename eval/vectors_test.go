package eval

import (
	"math"
	"testing"

	"github.com/Fraancuus/engram/mock"
)

func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func TestUnitAndMix(t *testing.T) {
	t.Parallel()
	r := newRNG(1)
	a, b := unit(r, dimDefault), unit(r, dimDefault)
	if len(a) != dimDefault || math.Abs(norm(a)-1) > 1e-5 {
		t.Fatalf("unit: len %d norm %v", len(a), norm(a))
	}
	m := mix(a, nil, 0, b, 0.15)
	if math.Abs(norm(m)-1) > 1e-5 {
		t.Fatalf("mix norm %v", norm(m))
	}
	if c := mock.Cosine(m, a); c < 0.95 {
		t.Fatalf("mix drifted from centroid: cos %v", c)
	}
}

func TestNewRNGDeterministic(t *testing.T) {
	t.Parallel()
	a, b := unit(newRNG(9), 8), unit(newRNG(9), 8)
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("same seed, different vectors")
		}
	}
}
