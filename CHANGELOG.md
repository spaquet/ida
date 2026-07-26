# Changelog

Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.5.2]

- Fixed method/class qualified names for Ruby classes nested inside another
  class or module with no repeated outer name in source (e.g. a bare `class
  Scope` inside `class AgentFlowPolicy`, the common Pundit policy-scope
  shape). Qualified names are now built from the full enclosing nesting
  stack (`AgentFlowPolicy::Scope`, not `Scope`), so unrelated files' nested
  classes of the same short name no longer collide into one identity —
  previously this made every Pundit `Scope#initialize`/`Scope#resolve`
  across a whole app look like one method declared N times.
- Added `ida unused method` (and `method` as an `ida_unused` MCP kind) to
  list methods with no resolved incoming use. Coverage is heuristic: a
  same-named `.method`/`:method` reference anywhere in the codebase counts
  as a use (matched by name only, since most Ruby calls are on a
  local/instance variable whose class Ida cannot resolve), and
  `initialize`, Pundit `*Policy`/`*Policy::Scope` methods, and
  `?`/`!`-suffixed methods are excluded outright as conventionally
  framework/template-invoked. Treat results as a lead list, not a
  dead-code list.
- Removed `db/migrate/`, `public/`, `bin/`, and `vendor/` from the default
  indexing scope. Migrations are framework-invoked-only boilerplate that
  only added noise to `ida unused method`; `public/`, `bin`, and `vendor`
  are never authored business logic.
- Changed `ida impact`'s default depth from 2 to 1 (CLI and MCP tool
  `ida_impact`). At depth 2, traversal walked straight through any
  high-fan-out hub node (e.g. a multi-tenant `Organization` model that
  dozens of unrelated models `belongs_to`), surfacing most of the app as
  "impacted" by an unrelated change. Depth 1 now returns only relationships
  that directly touch the queried node; `depth` can still be raised
  explicitly when a deeper walk is genuinely wanted.
- Changed `confidence` on every node and edge from a string label
  (`"exact"`/`"convention"`/`"lsp"`) to an integer 0-100, so consuming agents
  get a directly usable value instead of an opaque enum. Every fact Ida
  records today is deterministic and unambiguous by construction, so
  `confidence` is currently always `100`; a lower score is reserved for a
  planned `ambiguous` tier. This is a breaking schema change — run `ida sync
  --rebuild` after upgrading to convert an existing index.
- Fixed a crash (`UNIQUE constraint failed: nodes.id`) where a line
  referencing the same `Receiver.method` call twice (e.g.
  `Rails.env.development? || Rails.env.test?`) produced two identical
  `method_call_use` nodes; node extraction now de-duplicates by ID.
- `ida mcp` now prints its status, transport, project path, and exit
  instructions to stderr on start, instead of starting silently.
- Documented `ida unused`, `ida duplicates`, `ida env`, and `ida mcp config`
  in the CLI reference, and `ida_unused`/`ida_duplicates`/`ida_env` in the MCP
  reference — all previously implemented but undocumented.
- Clarified that `ida mcp` already runs its own in-process watcher (the same
  one `ida watch` runs standalone); running both against the same project is
  redundant and can contend for the `.ida/ida.db` file lock.
- README's command list now points to the CLI/MCP references instead of
  listing commands with no context.

## [0.5.0]

- Portability hardening: CI now runs gofmt, go vet, golangci-lint, build, and
  the full test suite on macOS, Linux, and Windows runners (previously
  Ubuntu-only).
- Fixed `internal/lsp.PathToURI`/`URIToPath` to produce and parse
  spec-correct `file://` URIs for Windows-shaped paths (drive letter,
  backslashes) instead of naive string concatenation, so LSP enrichment
  interoperates with real Ruby/TypeScript language servers on Windows.
- Added a tag-triggered (`v*`) release workflow that gates on the full test
  matrix, then builds and publishes native binaries to GitHub Releases for
  macOS (amd64, arm64), Linux (amd64, arm64), and Windows (amd64) — built
  natively per platform since the tree-sitter bindings require cgo.
- `ida --version` now reports the released tag (embedded via `-ldflags` at
  build time) instead of a hardcoded, easily stale constant.

## [0.4.4]

- Added partial resolution: `render "name"`/`render partial: "name"` calls
  resolve to the `app/views/**/_name.*` file they render, following Rails'
  own same-directory/`app/views`-rooted lookup convention, as a
  `renders_partial` edge.
- Added ViewComponent resolution: `render FooComponent.new(...)` resolves to
  the matching `class Foo < ViewComponent::Base` declaration as a
  `renders_component` edge.
- Added `ida unused partial` / `ida unused view_component` (MCP tool
  `ida_unused`) to list partials/components with no resolved incoming
  render edge.
- Added one-hop Ruby class-method call resolution: `Receiver.method(...)`
  and `Receiver.new(...).method(...)` calls resolve to a `calls` edge when
  `Receiver` uniquely names a project class and its file declares exactly
  one method with that name, letting `ida impact`/`ida node` on a
  service/method surface which controllers (or anything else) call it.
- Ruby method nodes are now qualified by their owning class/module (e.g.
  `NotifyService#call`), matching the shape Stimulus controller methods
  already used. Added `ida duplicates method` / `ida duplicates
  stimulus_controller` (MCP tool `ida_duplicates`) to list declarations
  sharing one qualified name — the concrete risk that Ruby's own
  "last definition loaded wins" semantics create — marking a group
  `expected: true` when every location is a `config/environments/*.rb` or
  `config/locales/*.rb` file, since those are never loaded together.
- Added ENV variable tracking: `ENV["NAME"]`/`ENV.fetch("NAME", ...)` reads
  (Ruby and YAML, since Rails renders YAML config through ERB) become an
  `env_var_use` node, skipping commented-out lines. `ida env` (MCP tool
  `ida_env`) lists every variable found, with every read site, categorized
  as database/initializer/environment/config/app by its path.

## [0.4.2]

- Fixed a crash (`UNIQUE constraint failed: nodes.id`) where a `validates`
  line referencing the same symbol twice — once as a field, once inside an
  option value such as `if: :confirmed?` — produced two identical
  `validation` nodes; field extraction now stops at the first keyword option
  and de-duplicates.
- Fixed a crash (`UNIQUE constraint failed: edges.id`) where two textual
  occurrences resolving to the same `(source, target, kind)` edge fact
  (e.g. two associations targeting the same class) failed to insert; edge
  IDs are deliberately content-addressed for this reason, so the insert now
  ignores the conflict instead of erroring.
- Fixed `ida init`/`ida sync` failing to launch a project-local
  `typescript-language-server` (installed under `node_modules/.bin` rather
  than on `$PATH`): `ida doctor` already resolved it there, but the resolved
  path was discarded before spawning the process, so the server always
  failed with "executable file not found in $PATH".
- Added support for TypeScript 7's native `tsgo` language server
  (`@typescript/native-preview`, `tsgo --lsp --stdio`) as the default
  `typescript` LSP backend when the project's installed or declared
  TypeScript version is 7 or newer; TypeScript &lt;7 projects keep using
  `typescript-language-server --stdio` as before. An explicit `typescript`
  entry in project config still overrides detection either way.
- Fixed `ida watch` hanging indefinitely after a TypeScript LSP server fails
  `initialize` (e.g. `typescript-language-server` against a TS7 project it
  can't load): shutdown on close now runs under a bounded timeout instead of
  `context.Background()`, so a server that never replies to `shutdown` no
  longer blocks the watcher from starting.
- LSP enrichment failures (missing/failed-to-start/failed-to-initialize
  server) now log as "unavailable, skipping enrichment" instead of a raw
  wrapped error, and `ida watch` prints a "watching ... (Ctrl+C to stop)"
  line once it's actually running, so a skipped LSP integration reads as
  informational rather than as a crash.

## [0.4.0]

- JavaScript/TypeScript/JSX/TSX extraction via tree-sitter
  (`github.com/tree-sitter/go-tree-sitter` plus the pinned JS, TypeScript,
  and CSS grammars): modules/imports, top-level function/class/const
  declarations, a heuristic React component/hook subset, and JSX
  component-usage sites.
- Stimulus: controller identifier, `static targets/values/classes/outlets`,
  and action/lifecycle methods extracted from `controllers/*_controller.js`;
  `data-controller`/`data-action` template attributes resolve to the
  matching controller and action at `convention` confidence.
- Turbo: `broadcasts_to`/`broadcasts`/`broadcast_*_to` model macros,
  `turbo_frame_tag`/`<turbo-frame>`, and `turbo_stream_from` are recorded as
  searchable nodes (not yet resolved to edges — see docs/relationships.md).
- React: relative `import` specifiers resolve to their target file
  (`imports`), JSX component usage resolves to its same-file or
  cross-file-imported declaration (`jsx_renders`), and `react_component(...)`
  Ruby/ERB helper calls resolve to the uniquely named component (`mounts`).
- Tailwind CSS: `theme.extend.*` tokens in `tailwind.config.js`/`.ts`
  resolve to every template, JSX/TSX component, or CSS `@apply` rule whose
  static class list uses them (`tailwind_uses`); dynamically constructed
  class values are skipped rather than guessed at, and individual utility
  classes never become nodes.
- Optional LSP enrichment: a JSON-RPC-over-stdio client (`internal/lsp`)
  talks to `ruby-lsp`/`typescript-language-server` when available, filling
  gaps deterministic resolution left unresolved (unresolved associations,
  `js_import`, `jsx_use`) via `textDocument/definition`, at a new `lsp`
  confidence tier. Strictly additive and best-effort: a missing/failed
  server never fails an index. Runs only on a full `ida sync`, not on
  watch-triggered incremental refreshes.

## [0.3.0]

- `ida mcp config [agent...]` prints ready-to-use MCP configuration snippets
  for Claude Code, Cursor, Codex, Pi, and OpenCode, with the resolved binary
  and project paths filled in. Ida never edits agent configuration itself.
- `ida status` / `status --json` and the `ida_status` MCP tool now report
  `extractor_versions` (the distinct extractor tags present in the index) and
  `enabled_integrations` (watcher, docs, and available LSP servers).
- MCP tool calls other than `ida_status` now include a `freshness` note in
  their response when the index is incomplete, the watcher is degraded, or
  files are still pending re-extraction, so an agent knows a result may be
  stale without a separate status call.
- Filesystem rename handling (watcher debounce, atomic refresh, old-node
  cleanup) now has direct test coverage alongside create/modify/delete.

Filesystem watch/debounce/hash-scan, atomic per-file refresh, `ida watch`,
`ida status`, and all seven MCP tools (including `ida_refresh`) were already
in place from 0.1.0/0.2.0; this release closes the remaining "agent loop"
gaps: installer snippets, fuller freshness reporting, and observability
fields.

## [0.2.0]

- Rails engine recognition: `lib/<name>/engine.rb` declaring a class under
  `Rails::Engine` is detected, and its `app/`, `config/`, `lib/`, and
  `db/migrate/` paths are scoped using the same default rules as the project
  root. Surfaced in `ida doctor` and `ida scope`.
- Route macro extraction: `resources`/`resource` (with `only:`, `except:`,
  `controller:`), `namespace :name do ... end` nesting, and `root` are now
  parsed in `config/routes.rb`, in addition to one-line `get`/`post`/`put`/
  `patch`/`delete` declarations.
- Active Record association extraction and resolution: `has_many`, `has_one`,
  `belongs_to`, and `has_and_belongs_to_many` become graph edges to the
  conventionally named target class at `convention` confidence.
  `validates`/`validate` and `scope :name` become searchable nodes on their
  class.
- Document link and code-symbol-mention extraction: every ingested document
  section now records its outbound links and its explicit (backtick-quoted)
  code-symbol mentions. A local section's unambiguous mention becomes a
  `mentions` edge to the matching graph node.
- `ida sync` now reconciles the index with disk (hash-checks and re-extracts
  only changed files) instead of always rebuilding; `ida sync --rebuild`
  forces a complete rebuild.

## [0.1.0]

- Ida foundation index: project discovery/scope, Turso-backed local storage, Ruby/Rails core extraction (models, controllers, views, routes).
- Conservative Rails relationship resolution (route -> controller action -> view).
- Bounded CLI and MCP server queries (`ida status`, `ida search`, `ida context`, `ida impact`, `ida mcp`).
- Incremental filesystem watcher with health reporting.
- `ida doctor` project diagnostics and `ida init --install-lsp` language server setup.
- `ida docs` ingestion for local and remote documentation.
- CI workflow and cross-platform `.gitignore`.
