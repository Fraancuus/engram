package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Fraancuus/engram"
)

// forgetStore is the narrow lifecycle capability the forget handler consumes — defined at
// its consumer and satisfied by the one concrete *neo4j.Store.
type forgetStore interface {
	SetForgotten(ctx context.Context, id engram.MemoryID) error
	Pin(ctx context.Context, id engram.MemoryID) error
	Delete(ctx context.Context, id engram.MemoryID) error
	Supersede(ctx context.Context, ids []engram.MemoryID) error
}

type forgetInput struct {
	ID   string `json:"id" jsonschema:"id of the memory to act on"`
	Mode string `json:"mode" jsonschema:"soft (exclude from recall), hard (delete), pin (protect from decay), or supersede (mark replaced)"`
}

type forgetOutput struct {
	OK bool `json:"ok"`
}

// doForget validates the untrusted input and dispatches to the matching lifecycle op. For
// soft/hard/pin a missing id surfaces as a legible ErrNotFound (actionable for the caller);
// supersede is idempotent and succeeds even for an absent id. Other internal failures are
// logged and returned sanitized.
func (h *handlers) doForget(ctx context.Context, in forgetInput) (out forgetOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("forget: recovered panic", "panic", r)
			out, err = forgetOutput{}, errors.New("forget: internal error")
		}
	}()

	if strings.TrimSpace(in.ID) == "" || len(in.ID) > maxEntityBytes {
		return forgetOutput{}, fmt.Errorf("id must be 1..%d bytes and not blank", maxEntityBytes)
	}
	id := engram.MemoryID(in.ID)

	switch in.Mode {
	case "soft":
		err = h.forget.SetForgotten(ctx, id)
	case "hard":
		err = h.forget.Delete(ctx, id)
	case "pin":
		err = h.forget.Pin(ctx, id)
	case "supersede":
		err = h.forget.Supersede(ctx, []engram.MemoryID{id})
	default:
		return forgetOutput{}, fmt.Errorf("invalid mode %q: want soft, hard, pin, or supersede", in.Mode)
	}
	if err != nil {
		if errors.Is(err, engram.ErrNotFound) {
			return forgetOutput{}, fmt.Errorf("forget %q: %w", id, err)
		}
		h.log.Error("forget: store op failed", "mode", in.Mode, "err", err)
		return forgetOutput{}, errors.New("forget: store unavailable")
	}
	return forgetOutput{OK: true}, nil
}
