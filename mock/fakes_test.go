package mock

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Fraancuus/engram"
)

func TestFakeStoreDeleteIfStale(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	for _, tc := range []struct {
		name    string
		f       *FakeStore
		want    map[engram.MemoryID]bool // what DeleteIfStale reports, per id
		wantErr error
	}{
		{name: "zero value deletes", f: &FakeStore{},
			want: map[engram.MemoryID]bool{"a": true, "b": true}},
		{name: "an id in KeptIDs is kept while another is deleted", f: &FakeStore{KeptIDs: map[engram.MemoryID]bool{"b": true}},
			want: map[engram.MemoryID]bool{"a": true, "b": false}},
		{name: "DeleteErr returns false and the error", f: &FakeStore{DeleteErr: boom},
			want: map[engram.MemoryID]bool{"a": false, "b": false}, wantErr: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// One sweep pass: the candidate read, then a delete per candidate, all with one cutoff.
			if _, err := tc.f.PruneCandidates(ctx, t0, 10); err != nil {
				t.Fatal(err)
			}
			for _, id := range []engram.MemoryID{"a", "b"} {
				deleted, err := tc.f.DeleteIfStale(ctx, id, t0)
				if deleted != tc.want[id] || !errors.Is(err, tc.wantErr) {
					t.Errorf("DeleteIfStale(%s) = (%v, %v), want (%v, %v)", id, deleted, err, tc.want[id], tc.wantErr)
				}
			}
			if !slices.Equal(tc.f.Deleted, []engram.MemoryID{"a", "b"}) {
				t.Errorf("Deleted = %v, want [a b]", tc.f.Deleted)
			}
			if !slices.EqualFunc(tc.f.StaleBefore, []time.Time{t0, t0}, time.Time.Equal) {
				t.Errorf("StaleBefore = %v, want [%v %v]", tc.f.StaleBefore, t0, t0)
			}
			if !tc.f.PruneBefore.Equal(t0) {
				t.Errorf("PruneBefore = %v, want %v", tc.f.PruneBefore, t0)
			}
		})
	}
}

func TestFakeStorePerIDScripts(t *testing.T) {
	t.Parallel()
	gone, boom := errors.New("gone"), errors.New("boom")
	f := &FakeStore{ReinforceErrs: map[engram.MemoryID]error{"b": gone}}
	if err := f.Reinforce(ctx, "a", t0); err != nil {
		t.Errorf("Reinforce(a) = %v, want nil", err)
	}
	if err := f.Reinforce(ctx, "b", t0); !errors.Is(err, gone) {
		t.Errorf("Reinforce(b) = %v, want its own error gone", err)
	}
	f.ReinforceErr = boom
	if err := f.Reinforce(ctx, "a", t0); !errors.Is(err, boom) {
		t.Errorf("Reinforce(a) with ReinforceErr set = %v, want boom", err)
	}
	if err := f.Reinforce(ctx, "b", t0); !errors.Is(err, gone) {
		t.Errorf("Reinforce(b) with ReinforceErr set = %v, want gone: the per-id error comes first", err)
	}
	if !slices.Equal(f.Reinforced, []engram.MemoryID{"a", "b", "a", "b"}) {
		t.Errorf("Reinforced = %v, want [a b a b]: a scripted error is still recorded", f.Reinforced)
	}

	for _, id := range []engram.MemoryID{"c", "a", "c"} {
		if err := f.PropagateReinforce(ctx, id, 0.5, t0); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(f.Propagated, []engram.MemoryID{"c", "a", "c"}) {
		t.Errorf("Propagated = %v, want [c a c] in call order", f.Propagated)
	}
	if f.LastPropagateID != "c" {
		t.Errorf("LastPropagateID = %q, want c", f.LastPropagateID)
	}
}
