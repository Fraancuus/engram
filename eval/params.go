package eval

import "fmt"

// HubFraction is the fraction of entities that act as high-degree hubs.
const HubFraction = 0.05

// Params sizes a generated eval corpus.
type Params struct {
	Seed       int64 `json:"seed"`
	Namespaces int   `json:"namespaces"`
	Topics     int   `json:"topics"`
	Memories   int   `json:"memories"`
	Entities   int   `json:"entities"`
	Queries    int   `json:"queries"`
	Days       int   `json:"days"`
	Dim        int   `json:"dim"`
	// Mix names the query class mix; a plain string so Params stays comparable.
	Mix string `json:"mix"`
}

// Query mix preset names.
const (
	// MixAgent is the primary mix, weighted to what an agent asks.
	MixAgent = "agent"
	// MixStress is the original even-handed mix that stresses every class.
	MixStress = "stress"
)

// mixPcts are class percentages: topic, fact, recent, old; bridge takes the remainder.
var mixPcts = map[string][4]int{
	MixAgent:  {35, 20, 30, 5},
	MixStress: {25, 15, 20, 15},
}

// ParseMix validates a mix preset name.
func ParseMix(s string) (string, error) {
	if _, ok := mixPcts[s]; !ok {
		return "", fmt.Errorf("unknown mix %q: want %s or %s", s, MixAgent, MixStress)
	}
	return s, nil
}

// Floor returns the minimum corpus size the eval accepts (Seed 0).
func Floor() Params {
	return Params{
		Namespaces: 3, Topics: 50, Memories: 5000, Entities: 1200,
		Queries: 500, Days: 180, Dim: 128, Mix: MixAgent,
	}
}

// Scaled returns p with Topics, Memories, Entities and Queries multiplied by n.
// n < 1 is treated as 1.
func (p Params) Scaled(n int) Params {
	if n < 1 {
		n = 1
	}
	p.Topics *= n
	p.Memories *= n
	p.Entities *= n
	p.Queries *= n
	return p
}

// Validate reports the first dimension of p that is below its floor.
func (p Params) Validate() error {
	if _, err := ParseMix(p.Mix); err != nil {
		return err
	}
	f := Floor()
	for _, d := range []struct {
		name      string
		got, want int
	}{
		{"namespaces", p.Namespaces, f.Namespaces},
		{"topics", p.Topics, f.Topics},
		{"memories", p.Memories, f.Memories},
		{"entities", p.Entities, f.Entities},
		{"queries", p.Queries, f.Queries},
		{"days", p.Days, f.Days},
		{"dim", p.Dim, f.Dim},
	} {
		if d.got < d.want {
			return fmt.Errorf("%s %d below floor %d", d.name, d.got, d.want)
		}
	}
	return nil
}
