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
	want := Params{Namespaces: 3, Topics: 100, Memories: 10000, Entities: 2400, Queries: 1000, Days: 180, Dim: 128, Mix: MixAgent}
	if got := Floor().Scaled(2); got != want {
		t.Fatalf("Scaled(2) = %+v, want %+v", got, want)
	}
}

func TestParamsRejectUnknownMix(t *testing.T) {
	t.Parallel()
	p := Floor()
	p.Mix = "weekend"
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "weekend") {
		t.Fatalf("err = %v, want it to name weekend", err)
	}
}

func TestParseMix(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{MixAgent, MixStress} {
		if got, err := ParseMix(ok); err != nil || got != ok {
			t.Errorf("ParseMix(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"", "weekend"} {
		if _, err := ParseMix(bad); err == nil {
			t.Errorf("ParseMix(%q) accepted", bad)
		}
	}
}

func TestScaledKeepsMix(t *testing.T) {
	t.Parallel()
	p := Floor()
	p.Mix = MixStress
	if got := p.Scaled(2).Mix; got != MixStress {
		t.Fatalf("Scaled lost mix: %q", got)
	}
	tp := tinyParams()
	tp.Mix = MixStress
	tp.Seed = 3 // the default tiny seed (2) has no noise draw that fits at twice the size
	d, err := generateUnchecked(tp.Scaled(2))
	if err != nil {
		t.Fatal(err)
	}
	if d.Params.Mix != MixStress || len(d.Queries) != tp.Queries*2 {
		t.Fatalf("mix %q, %d queries", d.Params.Mix, len(d.Queries))
	}
}
