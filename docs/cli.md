# Ida CLI reference

Ida provides a command-line interface for developers, scripts, and AI coding
agents with shell access. Use the CLI for one-shot queries and maintenance. Use
the [MCP server](mcp.md) when an agent will make repeated queries during a
long-running session.

## Setup

Build Ida and initialize it from anywhere inside a Rails application:

```sh
go build -o ida ./cmd/ida
./ida init .
```

Ida discovers the nearest Rails root by walking upward until it finds
`Gemfile`, `config/application.rb`, and `config/routes.rb`. The index is stored
at `.ida/ida.db` in that Rails root.

Run `ida init` before read commands or `ida mcp`.

## Output

Read commands print human-readable output by default. Add `--json` anywhere in
the command to receive JSON:

```sh
ida search Article --json
ida --json context "How are articles displayed?"
```

Normal output uses stdout. Diagnostics and watcher messages use stderr. A
successful command exits with status `0`; invalid input or an operational error
exits with status `1`.

`ida --help` (or `ida -h`) prints the command summary. `ida --version` prints
the installed Ida version. The `help` and `version` command aliases are also
accepted.

## Implemented commands

### `ida init [path] [--install-lsp]`

Discover the Rails root, create `.ida/ida.db`, and build a complete index.
`path` defaults to the current directory. With `--install-lsp`, Ida first
detects configured, project-local, and executable Ruby and TypeScript language
servers. For each missing server it prints the installation command and asks
for confirmation before running it.

```sh
ida init ~/code/my_app
ida init --install-lsp
```

Ruby LSP is installed with `gem install ruby-lsp`, following its composed-bundle
model rather than editing the application Gemfile. If `package.json` exists,
TypeScript Language Server is offered as a project development dependency.

### `ida sync [path] [--rebuild]`

Reconcile the index with disk: hash-check every scoped file and re-extract
only what changed, added, or was removed. Use this as the manual recovery
command after missed filesystem events or degraded status. `path` defaults to
the current directory.

Pass `--rebuild` to force a complete rebuild instead, discarding the existing
index and re-extracting every scoped file. `ida sync` also rebuilds
automatically the first time it runs against a project with no index yet.

```sh
ida sync
ida sync --rebuild
```

### `ida watch [path]`

Build the current index, watch scoped directories, and refresh changed files
until interrupted. Create, modify, rename, and delete events are debounced.
A periodic metadata/hash reconciliation recovers missed events.

Watcher diagnostics go to stderr. `path` defaults to the current directory.

```sh
ida watch
```

Use `ida watch` only for CLI-only usage (running `ida search`/`ida context`/
etc. from a terminal with no MCP client attached). `ida mcp` already runs
this same watcher internally — see [`ida mcp`](#ida-mcp-path) below. Running
both `ida watch` and `ida mcp` against the same project at once is redundant
and can make them contend for the same `.ida/ida.db` file lock; run one or
the other, not both.

### `ida status [path]`

Report index state, generation, indexed time, file count, node count, edge
count, and the last indexing error. `path` defaults to the current directory.

```sh
ida status --json
```

### `ida doctor [path]`

Check Rails discovery, database accessibility, index completeness, watcher
health, pending files, recognized Rails engines, and configured or detected
language servers. Missing optional LSPs include an installation command but do
not make the deterministic index unavailable.

```sh
ida doctor
ida doctor --json
```

### `ida scope <path>`

Explain whether a project-relative or absolute path is included and why.
Paths inside a recognized Rails engine (a `lib/<name>/engine.rb` declaring a
class under `Rails::Engine`) are included using the same default rules Ida
applies at the project root, reported as `Rails engine default`.

```sh
ida scope app/models/article.rb
ida scope spec/models/article_spec.rb
```

Tests under `test/` and `spec/` are excluded by default. Opt in through
`ida.json`:

```json
{
  "include": ["test/**", "spec/**"]
}
```

### `ida search <query>`

Search node names, qualified names, and project-relative paths. Results are
bounded to 20 entries in the current CLI.

```sh
ida search ArticlesController
```

### `ida context <task>`

Return line-numbered excerpts and relevant direct relationships for a task.
The current CLI returns at most five files and 12,000 source bytes.

```sh
ida context "How does GET /articles render its view?"
```

### `ida node <name-or-id>`

Explain one exact node and its incoming and outgoing relationships. If a name
is ambiguous, use its stable node ID or qualified name.

```sh
ida node "GET /articles"
```

To find where a Stimulus controller is used, look up its identifier (e.g.
`date-picker` for `controllers/date_picker_controller.js`): incoming
`stimulus_controller` edges are the templates with a matching
`data-controller` attribute (see [relationships.md](relationships.md#stimulus)).

```sh
ida node date-picker
```

`ida search <identifier>` also works and returns both the controller
definition and any use sites in one pass.

### `ida path <from> <to>`

Find a directed relationship path up to four edges long.

```sh
ida path "GET /articles" app/views/articles/index.html.erb
```

### `ida impact <name-or-id>`

List every direct incoming and outgoing relationship one hop from the named
node — everything that could plausibly break if it changes, and nothing
further removed. Bounded to at most 50 relationships.

```sh
ida impact index
```

Deliberately shallow: a two-hop traversal through a shared hub node (e.g. a
Rails model that dozens of unrelated models `belongs_to`, like a
multi-tenant `Organization`) would surface most of the app as "impacted,"
which is not useful. Use `ida path <from> <to>` to check a specific
multi-hop connection instead of widening `impact`.

### `ida unused <partial|view_component|method>`

List nodes of the given kind with no resolved incoming use.

```sh
ida unused partial
ida unused view_component
ida unused method
```

For `partial`/`view_component`, "used" means a resolved `renders_partial`/
`renders_component` edge.

For `method`, coverage is broader but heuristic: "used" means a resolved
`calls`/`routes_to`/`stimulus_action` edge, or a same-named `.method`/
`:method` reference anywhere in the codebase — matched by name only, since
most Ruby calls are on a local/instance variable whose class Ida cannot
determine (`task.archive!`, `before_action :dev_only`). `initialize`,
Pundit-style `*Policy`/`*Policy::Scope` methods, and `?`/`!`-suffixed
predicate/bang methods are excluded outright, since those are conventionally
invoked by the framework or from templates in ways this heuristic cannot
see. Treat `method` results as a lead to check, not a dead-code list — a
method called only from an ERB template as a bare `<%= helper_method %>`,
for example, will still appear here.

### `ida duplicates <method|stimulus_controller>`

List method or Stimulus controller declarations sharing one qualified name.

```sh
ida duplicates method
ida duplicates stimulus_controller
```

### `ida env`

List ENV variable reads, grouped by name.

```sh
ida env
```

### `ida docs add <path|url>`

Add an explicit local documentation file/directory or fetch one HTTP(S)
document. Local sources are recorded in `ida.json` and refreshed with the
normal index. Remote sources are stored in the local Ida database and split
into searchable sections.

```sh
ida docs add handbook/
ida docs add https://example.com/team-handbook.md
```

Remote fetching blocks private/local networks and credentials in URLs, follows
at most five redirects, times out after ten seconds, and accepts at most 2 MiB
of text. It does not crawl links or render JavaScript.

Every document section records its outbound links (Markdown, HTML) and its
explicit code-symbol mentions (backtick-quoted spans with no whitespace, such
as `` `ArticlesController` `` or `` `app/models/article.rb` ``). A local
section's unambiguous mentions become `mentions` edges to the matching graph
node.

### `ida mcp [path]`

Serve the ten Ida MCP tools over stdio and keep the index current with an
in-process watcher — the same watcher `ida watch` runs standalone. `path`
defaults to the current directory.

This is a single self-contained process: an agent client only needs to
launch `ida mcp`. Do not also run `ida watch` against the same project — it
would be a redundant second watcher racing the same `.ida/ida.db`.

```sh
ida mcp /absolute/path/to/my_app
```

On start, Ida prints its status, transport, and project to stderr, and how to
exit (Ctrl+C); stdout is reserved for protocol messages. There is no network
port: transport is stdio only.

See [MCP reference](mcp.md) for client configuration and tool schemas.

### `ida mcp config [agent...]`

Print MCP client configuration snippets for the running Ida binary and
current project. Supports `claude-code`, `cursor`, `codex`, `pi`, and
`opencode`. With no agent named, prints snippets for all supported agents.

```sh
ida mcp config claude-code
ida mcp config cursor codex
```
