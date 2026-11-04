package mock_test

import (
	"github.com/Fraancuus/engram/mcp"
	"github.com/Fraancuus/engram/mock"
	"github.com/Fraancuus/engram/sweep"
)

var (
	_ sweep.Pruner    = (*mock.MemStore)(nil)
	_ mcp.ServerStore = (*mock.MemStore)(nil)
)
