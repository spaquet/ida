# Ida product requirements

## Product summary

Ida is a local-first knowledge graph for Rails applications. It gives AI coding
agents a small, current, Rails-aware view of a repository through a CLI and an
MCP server.

Ida is not a general-purpose graph builder or an IDE. Its advantage is knowing
that a Rails application is more than Ruby calls: routes lead to controller
actions, actions render views, models map to tables and associations, jobs are
enqueued, and Hotwire connects server-rendered HTML to browser behavior.

## Goals

1. Answer architecture, navigation, change-impact, and implementation questions
   with less repository scanning.
2. Keep answers current within two seconds of a normal file save.
3. Model Rails, Turbo, Stimulus, Tailwind CSS, and React conventions.
4. Read project documentation without requiring PDF, image, audio, or video
   support.
5. Run locally on macOS, Windows, and Linux as a single Go CLI.
6. Work with Claude Code, Cursor, Codex, Pi, OpenCode, and other MCP clients.
7. Keep source code local unless the user explicitly enables remote Turso sync
   or adds a remote documentation URL.

## Non-goals

- A graph visualization or full-screen UI.
- A replacement for grep, an editor, the Rails console, or an LSP.
- Perfect runtime analysis of Ruby metaprogramming.
- Indexing gem source, generated assets, logs, caches, or user data by default.
- Embeddings, an LLM dependency, or a hosted service in the first release.
- PDF, image, audio, or video ingestion.
- Automatic edits to an application's source or dependency files.

## Users and primary jobs

The primary user is an AI coding agent operating inside a Rails repository. A
developer configures Ida and inspects its health through the CLI.

The agent must be able to:

- find the few files and symbols relevant to a task;
- trace a request from route to controller, view, model, and frontend behavior;
- find callers, references, rendered templates, broadcasts, and enqueued jobs;
- estimate what a change can affect;
- retrieve a bounded source excerpt with file and line locations;
- search relevant project documentation alongside code; and
- tell whether the graph is complete, refreshing, stale, or degraded.

## Functional requirements

### Project discovery and scope

`ida init [path]` must find the nearest Rails root using `Gemfile`,
`config/application.rb`, and `config/routes.rb`. It must recognize Rails engines
inside a repository.

Ida must respect `.gitignore`, stay inside the project root after resolving
symlinks, and apply Rails-aware defaults.

Included by default:

- `app/` Ruby, templates, JavaScript/TypeScript, JSX/TSX, and authored CSS;
- `config/routes.rb`, application/environment configuration, and initializers;
- `lib/`, Rails tasks, `test/`, and `spec/`;
- `db/schema.rb` or `db/structure.sql` and migrations;
- `Gemfile`, lockfiles, import maps, JavaScript manifests, and Tailwind config;
- Markdown, AsciiDoc, HTML, and plain-text documentation in the repository.

Always excluded by default:

- `.git/`, `.ida/`, `log/`, `tmp/`, `storage/`, `coverage/`;
- `node_modules/`, `vendor/bundle/`, package caches, and editor metadata;
- compiled/minified assets, source maps, `public/assets/`, `public/packs/`,
  `public/vite/`, `app/assets/builds/`, `dist/`, and `build/`;
- sockets, database contents, credentials, binary blobs, and uploaded files.

A committed `ida.json` may add `include`, `exclude`, documentation paths,
framework overrides, and LSP commands. Explicit excludes always win. `ida
scope` must explain why a path is included or excluded.

### Extraction and Rails understanding

Every node and edge must carry its source file, line range, extractor, and
confidence:

- `exact`: directly present in syntax or configuration;
- `convention`: resolved using an unambiguous Rails convention;
- `lsp`: reported by a language server;
- `ambiguous`: plausible but not safe to present as fact.

Ambiguous relationships may be returned as candidates but must never be mixed
silently with exact relationships.

The graph must represent:

- files, classes, modules, methods, constants, calls, inheritance, and mixins;
- routes, controller actions, helpers, views, layouts, and partials;
- models, tables, columns, associations, validations, callbacks, scopes, enums,
  and migrations;
- jobs, mailers, channels, concerns, services, components, and Rails engines;
- render, redirect, enqueue, mail, broadcast, and channel relationships;
- tests/specs and the application symbols they exercise.

Ruby metaprogramming must fail safely: no edge is better than a confidently
wrong edge.

### Hotwire and Turbo

Ida must understand both Ruby helpers and HTML/template attributes for:

- Turbo Frames and frame navigation;
- Turbo Stream responses, templates, actions, targets, and custom actions;
- `broadcast_*` and model broadcast declarations;
- Turbo visits and form behavior where statically visible;
- controller response formats and the views they select.

An agent should be able to trace a model update to a broadcast, stream action,
DOM target, partial, and Stimulus controller when those links exist.

### Stimulus

Ida must represent:

- controller identifiers and registration;
- `data-controller`, `data-action`, targets, values, classes, and outlets;
- lifecycle methods and action methods;
- links between templates and their controller modules.

### React

For `.js`, `.jsx`, `.ts`, and `.tsx`, Ida must represent modules, imports,
exports, components, hooks, JSX component use, and mount points visible from
Rails templates or JavaScript entrypoints. TypeScript LSP results may enrich
resolution but are not required for a usable index.

### Tailwind CSS

Ida must find authored Tailwind classes in templates and components, recognize
the project's Tailwind entry/configuration, and connect custom theme tokens,
plugins, and authored utilities to their uses.

Individual built-in utility classes must not become graph nodes; that would
overwhelm useful application structure. They remain searchable attributes on
the template or component that uses them. Dynamically constructed class names
must be marked unresolved.

### Documentation

Ida must ingest local Markdown, AsciiDoc, HTML, and plain text. A user may add an
HTTP(S) URL explicitly with `ida docs add`.

Documents are split by heading into addressable sections. Ida must preserve the
source path or URL, title, heading path, content hash, and fetch time. It must
extract links and explicit code-symbol mentions, then search document sections
alongside code.

Remote fetching must enforce HTTP(S), timeouts, redirect limits, response-size
limits, and private-network blocking. JavaScript rendering and recursive
crawling are out of scope for the first release.

### Refresh and consistency

After the initial index, Ida must watch included directories, debounce editor
save bursts, and re-extract only changed, added, or removed files. A periodic
hash scan must recover missed filesystem events.

Updates for one file must commit atomically: queries see either the previous or
new version, never a half-written file graph. Renames and directory deletions
must remove old nodes and edges. Configuration, lockfile, schema, route, and
autoload-path changes may trigger a wider resolution pass.

MCP results must say when relevant files are pending, an update failed, the
watcher degraded, or the full index is incomplete. `ida sync` is the manual
recovery path on every platform.

### LSP integration

Ida must work without an LSP. During `ida init`, it detects Ruby LSP, Ruby LSP
Rails, TypeScript Language Server, and configured alternatives. `ida doctor`
reports what is active and gives install commands.

Installation is opt-in (`ida init --install-lsp`) and must show the command
before running it. Ida must not silently modify a Gemfile, lockfile, package
manifest, or global toolchain.

LSP failures and timeouts degrade to deterministic extraction and appear in
status; they do not make the graph unavailable.

### CLI

The first release must provide:

```text
ida init [path]          configure and build the first index
ida sync [path]          reconcile the index with disk
ida watch [path]         keep an interactive CLI process current
ida status [path]        report scope, freshness, counts, and degraded features
ida doctor [path]        check Rails, database, watcher, and LSP integration
ida search <query>       search symbols, Rails artifacts, files, and docs
ida context <task>       return bounded source excerpts and relationships
ida node <name-or-id>    explain one node and its direct relationships
ida path <from> <to>     find a short relationship path
ida impact <name-or-id>  show likely upstream and downstream effects
ida docs add <path|url>  add an explicit documentation source
ida mcp                  serve MCP over stdio
```

Read commands must support `--json`. Human output goes to stdout, diagnostics to
stderr, and MCP mode must reserve stdout for protocol messages.

### MCP

The MCP server must expose the same core operations with stable schemas:

- `ida_status`
- `ida_search`
- `ida_context`
- `ida_node`
- `ida_path`
- `ida_impact`
- `ida_refresh`

Responses must be bounded by caller-supplied limits with conservative defaults.
`ida_context` returns complete, line-numbered excerpts rather than forcing an
agent to make a second file-read call. Results include stable node IDs, repo
relative paths, relationships, confidence, and freshness.

The installer must print configuration snippets for supported agents; it does
not need to edit every agent's global configuration in the first release.

## Configuration

`ida.json` is optional. Zero configuration must work for a conventional Rails
application.

```json
{
  "exclude": ["app/legacy_vendor/**"],
  "include": ["engines/payments/**"],
  "docs": ["docs/**"],
  "lsp": {
    "ruby": ["bundle", "exec", "ruby-lsp"],
    "typescript": ["typescript-language-server", "--stdio"]
  }
}
```

Environment variables are reserved for secrets and machine-local values, such
as `TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN`. They must not be written to the
project database or logs.

## Quality attributes

- **Portability:** release binaries and smoke tests for current macOS, Windows,
  and Linux on amd64 and arm64 where supported by the database runtime.
- **Performance:** initial index of a 10,000-file Rails repository in under 60
  seconds on a typical developer laptop; ordinary one-file refresh visible to
  queries within two seconds.
- **Context efficiency:** default MCP responses stay below 20,000 characters.
- **Reliability:** interrupted indexing leaves the previous complete generation
  queryable and status marked incomplete.
- **Privacy:** local indexing and querying require no account or network.
- **Security:** paths and URLs are validated at trust boundaries; source text is
  never sent to an LSP outside the configured local process.
- **Observability:** `status --json` reports index generation, timestamps,
  pending files, last error, extractor versions, and enabled integrations.

## Success measures

Before release, maintain a small fixture corpus covering a conventional Rails
app, an engine, Hotwire, Stimulus, React, and Tailwind.

On a labeled task set:

- at least 90% of route-to-action and action-to-view links are correct;
- at least 85% of declared model associations and broadcasts are connected;
- at least 90% of Stimulus controllers/actions/targets are connected;
- the correct implementation file appears in the first five `ida_context`
  files for at least 80% of tasks;
- false exact/convention edges stay below 2%;
- file create/change/delete refresh tests pass on all supported operating
  systems.

Performance numbers must be measured and published; they are targets, not
marketing claims.

## Delivery order

1. **Foundation:** project scope, Turso storage, Ruby/Rails core, docs, CLI
   search/context, atomic full and incremental indexing.
2. **Agent loop:** filesystem watch, freshness reporting, MCP, installation
   snippets, bounded source context.
3. **Rails frontend:** Turbo, Stimulus, ERB, React, and Tailwind relationships.
4. **Enrichment:** optional Ruby/TypeScript LSPs, engines/worktrees hardening,
   impact quality, benchmarks, and release packaging.

Cloud sync, embeddings, graph visualization, and automatic global agent
configuration require evidence from real usage before entering scope.
