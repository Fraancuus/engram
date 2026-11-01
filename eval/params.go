package eval

import "fmt"

// HubFraction is the fraction of entities that act as high-degree hubs.
const HubFraction = 0.05

// Params sizes a generated eval corpus.
type Params struct {
	Seed       int64
	Namespaces int
	Topics     int
	Memories   int
	Entities   int
	Queries    int
	Days       int
	Dim        int
}

// Floor returns the minimum corpus size the eval accepts (Seed 0).
func Floor() Params {
	return Params{
		Namespaces: 3, Topics: 50, Memories: 5000, Entities: 200,
		Queries: 500, Days: 180, Dim: 64,
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
