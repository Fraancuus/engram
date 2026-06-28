// Package inference implements the engram.Embedder and engram.Reranker ports by
// calling the local inference sidecar (HF Text Embeddings Inference or llama.cpp)
// over HTTP. The sidecar is a separate local process; this package is the client
// behind those ports. It depends on engram, never the reverse.
package inference
