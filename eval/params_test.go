package eval

import (
	"strings"
	"testing"
)

func TestParamsFloor(t *testing.T) {
	t.Parallel()
	if err := Floor().Validate(); err != nil {
		t.Fatalf("floor invalid: %v", err)
	}
	p := Floor()
	p.Memories = 4999
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "memories") || !strings.Contains(err.Error(), "5000") {
		t.Fatalf("err = %v, want memories/5000", err)
	}
}

func TestParamsScale(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -3} {
		if got := Floor().Scaled(n); got != Floor() {
			t.Errorf("Scaled(%d) = %+v, want floor", n, got)
		}
	}
	want := Params{Namespaces: 3, Topics: 100, Memories: 10000, Entities: 400, Queries: 1000, Days: 180, Dim: 64}
	if got := Floor().Scaled(2); got != want {
		t.Fatalf("Scaled(2) = %+v, want %+v", got, want)
	}
}
