package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mock"
)

func TestDoForgetDispatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode  string
		check func(*mock.FakeStore) bool
	}{
		{"soft", func(s *mock.FakeStore) bool { return len(s.Forgot) == 1 && s.Forgot[0] == "id1" }},
		{"hard", func(s *mock.FakeStore) bool { return len(s.Deleted) == 1 && s.Deleted[0] == "id1" }},
		{"pin", func(s *mock.FakeStore) bool { return len(s.PinnedIDs) == 1 && s.PinnedIDs[0] == "id1" }},
		{"supersede", func(s *mock.FakeStore) bool {
			return len(s.Superseded) == 1 && len(s.Superseded[0]) == 1 && s.Superseded[0][0] == "id1"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			t.Parallel()
			st := &mock.FakeStore{}
			h := testHandlers(&mock.FakeEmbedder{}, st)
			out, err := h.doForget(context.Background(), forgetInput{ID: "id1", Mode: tt.mode})
			if err != nil {
				t.Fatalf("doForget(%s): %v", tt.mode, err)
			}
			if !out.OK {
				t.Errorf("doForget(%s) ok = false", tt.mode)
			}
			if !tt.check(st) {
				t.Errorf("doForget(%s) did not dispatch to the right store op", tt.mode)
			}
		})
	}
}

func TestDoForgetValidation(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{}
	h := testHandlers(&mock.FakeEmbedder{}, st)
	if _, err := h.doForget(context.Background(), forgetInput{ID: "id1", Mode: "nuke"}); err == nil {
		t.Error("want error for invalid mode")
	}
	if _, err := h.doForget(context.Background(), forgetInput{ID: "  ", Mode: "soft"}); err == nil {
		t.Error("want error for blank id")
	}
	if len(st.Forgot)+len(st.Deleted) != 0 {
		t.Error("no store op should run on invalid input")
	}
}

func TestDoForgetNotFoundIsLegible(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{DeleteErr: engram.ErrNotFound}
	h := testHandlers(&mock.FakeEmbedder{}, st)
	_, err := h.doForget(context.Background(), forgetInput{ID: "missing", Mode: "hard"})
	if !errors.Is(err, engram.ErrNotFound) {
		t.Errorf("doForget(hard, missing) = %v, want wrapped ErrNotFound", err)
	}
}

func TestDoForgetStoreErrorSanitized(t *testing.T) {
	t.Parallel()
	st := &mock.FakeStore{SetForgottenErr: errors.New("db-internal-xyz")}
	h := testHandlers(&mock.FakeEmbedder{}, st)
	_, err := h.doForget(context.Background(), forgetInput{ID: "id1", Mode: "soft"})
	if err == nil {
		t.Fatal("want error when the store fails")
	}
	if strings.Contains(err.Error(), "db-internal-xyz") {
		t.Errorf("leaks internal detail: %q", err.Error())
	}
}
