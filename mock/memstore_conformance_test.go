package mock_test

import (
	"context"

	"github.com/Fraancuus/engram"
	"github.com/Fraancuus/engram/mcp"
	"github.com/Fraancuus/engram/mock"
	"github.com/Fraancuus/engram/sweep"
)

var (
	_ mcp.Store     = (*mock.MemStore)(nil)
	_ sweep.Pruner  = (*mock.MemStore)(nil)
	_ forgetMethods = (*mock.MemStore)(nil)
)

// forgetMethods mirrors the unexported mcp forgetStore method set.
type forgetMethods interface {
	SetForgotten(ctx context.Context, id engram.MemoryID) error
	Pin(ctx context.Context, id engram.MemoryID) error
	Delete(ctx context.Context, id engram.MemoryID) error
	Supersede(ctx context.Context, ids []engram.MemoryID) error
}
