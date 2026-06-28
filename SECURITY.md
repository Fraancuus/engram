# Security Policy

Engram is a single-user, single-node, local-first service (no multi-tenancy or auth
in v1). It still treats two boundaries as untrusted: **MCP tool inputs** (from a
calling agent) and the **inference sidecar** response. This policy describes how we
report vulnerabilities and how automated gates and human judgment divide the work.

## Reporting a vulnerability

Please report privately — **do not open a public issue** for a security bug.

- Preferred: open a [GitHub private security advisory](https://github.com/Fraancuus/engram/security/advisories/new)
  ("Report a vulnerability").
- Include affected version/commit, reproduction steps, and impact.

We aim to acknowledge within a few days. As a personal open-source project there is no
formal SLA, but credible reports are taken seriously and fixed before disclosure.

## Defense model: gates vs. judgment

The deterministic scanners cover the mechanical surface; the `security-auditor`
subagent (see [CONTRIBUTING.md](CONTRIBUTING.md)) covers the judgment surface. Together
they leave few gaps.

| Concern | Deterministic tool | Where it runs | Judgment layer |
|---|---|---|---|
| Secrets / leaks | gitleaks | pre-commit (staged) + CI (full history) | security-auditor (config, logging) |
| Known dependency vulns | govulncheck | CI | security-auditor (reachability) |
| Dependency freshness | Dependabot | GitHub | — |
| Go SAST (bugs/security) | gosec (via golangci-lint) + CodeQL | pre-commit + CI | security-auditor |
| Cypher injection | — (not reliably auto-detected) | — | **security-auditor** (parameterization) |
| MCP input validation | — | — | **security-auditor** + reviewer |
| Import-boundary / architecture | depguard | pre-commit + CI | architect |
| Unhandled errors | errcheck | pre-commit + CI | reviewer |
| Supply-chain posture | SHA-pinned GitHub Actions | CI | — |

## Invariants we hold

- **Every Neo4j query is parameterized.** No Cypher is built by concatenating user or
  agent input.
- **MCP tool inputs are validated** (types and bounds) at the handler boundary before
  anything reaches the domain.
- **No secrets in code or logs**, and errors crossing the MCP boundary never expose
  internal stack traces or full queries.
- **GitHub Actions are pinned to commit SHAs**, not tags.
