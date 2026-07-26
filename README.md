# Ida

Ida is a local-first knowledge graph for Rails codebases, built for AI coding
agents.

It will connect Rails conventions that general code graphs miss: routes,
controllers, views, Active Record, jobs, mailers, Turbo Streams, Stimulus,
React, and Tailwind CSS. Agents access the current graph through a small CLI or
MCP server instead of repeatedly scanning the repository.

> Status: foundation implementation. Project discovery, scope, local indexing,
> status, search, and bounded source context are available; relationship
> resolution, watching, MCP, and framework-specific extraction remain planned.

## Why Ida?

Ida stands for **Inference-Driven Architecture**. Rails applications are held
together by more than explicit function calls: a route implies a controller
action, a model name implies a table, and a Turbo Stream can connect a server
change to a browser update without either side naming the whole path.

Ida follows those clues. It turns framework conventions into an explicit,
traceable graph so an agent can understand the architecture from evidence
rather than guesswork. The name reflects that journey: start with what the code
says, infer only what the framework supports, and preserve the reasoning that
connects the two.

## Intended experience

```sh
ida init .
ida status
ida context "How does a comment update reach the browser?"
ida impact Comment#publish
ida mcp
```

Build and try the implemented foundation:

```sh
go build -o ida ./cmd/ida
./ida init /path/to/rails-app
./ida status
./ida search Comment
./ida context "Comment#publish"
```

Ida is designed to:

- refresh incrementally after file saves;
- focus on authored Rails code and ignore generated/vendor data;
- ingest Markdown, AsciiDoc, HTML, and text documentation;
- run offline on macOS, Windows, and Linux;
- use a local Turso database from one Go executable;
- optionally enrich the graph with Ruby and TypeScript language servers; and
- return bounded source excerpts with provenance, confidence, and freshness.

No UI, embeddings, cloud account, or LLM is required.

## Documentation

- [Product requirements](docs/product-requirements.md)
- [Architecture](docs/architecture.md)
- [Coding-agent guidance](AGENTS.md)

## Planned development

The delivery order is deliberately narrow:

1. Rails project scope, local storage, Ruby/Rails extraction, docs, and CLI.
2. Incremental refresh, freshness reporting, MCP, and agent configuration.
3. Turbo, Stimulus, React, and Tailwind relationships.
4. Optional LSP enrichment, portability hardening, and benchmarks.

See the product requirements for acceptance criteria. Claims about speed or
retrieval quality will be published only after the fixture corpus and benchmark
exist.
