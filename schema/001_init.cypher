// Engram schema — idempotent (IF NOT EXISTS), safe to re-run. The embedding dimension
// and similarity function are recorded here so the model choice (BGE-small, 384-dim,
// cosine) is versioned with the schema and stays swappable.

// Identity / uniqueness.
CREATE CONSTRAINT memory_id IF NOT EXISTS
  FOR (m:Memory) REQUIRE m.id IS UNIQUE;
CREATE CONSTRAINT entity_id IF NOT EXISTS
  FOR (e:Entity) REQUIRE e.id IS UNIQUE;

// Native vector index on :Memory.embedding (BGE-small = 384 dims, cosine similarity).
CREATE VECTOR INDEX memory_embedding IF NOT EXISTS
  FOR (m:Memory) ON (m.embedding)
  OPTIONS { indexConfig: {
    `vector.dimensions`: 384,
    `vector.similarity_function`: 'cosine'
  }};

// Scalar indexes for the namespace/type filtering that recall will use (M1).
CREATE INDEX memory_namespace IF NOT EXISTS FOR (m:Memory) ON (m.namespace);
CREATE INDEX memory_type IF NOT EXISTS FOR (m:Memory) ON (m.type);
