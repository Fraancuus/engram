// Package mock provides in-memory fakes implementing the engram domain ports
// (MemoryStore, Embedder, Reranker, DecayModel, Clock) for tests and the eval
// harness. The fakes implement the same ports as the real adapters so the eval can
// swap in deterministic, clock-virtualized behaviour.
package mock
