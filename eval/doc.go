// Package eval is the Engram evaluation harness: it generates a synthetic
// labelled corpus, replays it against decay strategies under a virtual clock,
// and reports retrieval quality.
//
// Anti-circularity rule: relevance is judged against ground-truth labels owned
// by the generator, never by the reranker or any other pipeline output. The
// system under test never grades itself.
package eval
