# Claude guidance for Ida

Read [AGENTS.md](AGENTS.md) before changing this repository.

For architecture or scope decisions, also read:

- [docs/product-requirements.md](docs/product-requirements.md)
- [docs/architecture.md](docs/architecture.md)

Keep the implementation local-first, Rails-specific, and small. Do not add a
UI, embeddings, hosted services, a daemon, or automatic project dependency
edits unless the requirements change or measurements justify them.

When Ida becomes runnable, use its own MCP/CLI context before broad repository
scans and refresh the graph after code changes.
