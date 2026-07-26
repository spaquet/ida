# Coding-agent guidance

## Mission

Build Ida: a local-first Rails knowledge graph exposed through CLI and MCP.
Product behavior lives in [docs/product-requirements.md](docs/product-requirements.md);
system boundaries live in [docs/architecture.md](docs/architecture.md).

## Working rules

- Read both design documents before changing behavior or architecture.
- Prefer the smallest end-to-end change that satisfies a stated requirement.
- Use Go's standard library and existing dependencies before adding packages.
- Keep one executable and one local `.ida/ida.db` per worktree.
- Keep indexing deterministic and usable without an LSP, network, cloud, or LLM.
- Treat Rails conventions as explicit resolver logic with provenance and
  confidence; omit uncertain edges instead of inventing certainty.
- Use project-relative slash-separated graph identities.
- Share one scope predicate between scanning and watching.
- Replace a changed file's subgraph atomically and expose stale/degraded state.
- Keep MCP stdout protocol-only; send diagnostics to stderr.
- Validate paths, URLs, MCP inputs, SQL parameters, and response budgets.
- Never index secrets, databases, uploads, generated assets, dependencies, or
  paths outside the project root by default.
- Do not silently edit Gemfiles, lockfiles, package manifests, or global agent
  configuration.

## Expected checks

Until implementation exists, keep Markdown links valid and the documents
consistent. Once Go code is present, run at minimum:

```sh
gofmt -w <changed-go-files>
go test ./...
go vet ./...
```

Non-trivial extraction or resolution changes need one small fixture-based test
that would fail without the change. Watcher changes need create, modify, and
delete coverage on the affected platform.

## Scope discipline

Do not add speculative abstractions, graph servers, vector stores, cloud sync,
telemetry, a visual UI, or a shared daemon. Add one only after a requirement or
measurement establishes the need.
