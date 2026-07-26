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

### `ida init [path]`

Discover the Rails root, create `.ida/ida.db`, and build a complete index.
`path` defaults to the current directory.

```sh
ida init ~/code/my_app
```

### `ida sync [path]`

Rebuild the complete index from the current project scope. Use this as the
manual recovery command after missed filesystem events or degraded status.
`path` defaults to the current directory.

```sh
ida sync
```

### `ida watch [path]`

Build the current index, watch scoped directories, and refresh changed files
until interrupted. Create, modify, rename, and delete events are debounced.
A periodic metadata/hash reconciliation recovers missed events.

Watcher diagnostics go to stderr. `path` defaults to the current directory.

```sh
ida watch
```

### `ida status [path]`

Report index state, generation, indexed time, file count, node count, edge
count, and the last indexing error. `path` defaults to the current directory.

```sh
ida status --json
```

### `ida scope <path>`

Explain whether a project-relative or absolute path is included and why.

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

### `ida path <from> <to>`

Find a directed relationship path up to four edges long.

```sh
ida path "GET /articles" app/views/articles/index.html.erb
```

### `ida impact <name-or-id>`

Traverse likely incoming and outgoing effects to depth two, returning at most
50 relationships.

```sh
ida impact index
```

### `ida mcp [path]`

Serve the seven Ida MCP tools over stdio and keep the index current with an
in-process watcher. `path` defaults to the current directory.

```sh
ida mcp /absolute/path/to/my_app
```

See [MCP reference](mcp.md) for client configuration and tool schemas.

## Planned commands

The product requirements include commands that are not implemented yet:

- `ida doctor`
- `ida docs add <path|url>`
- `ida init --install-lsp`
- `ida sync --rebuild` as a distinct mode

Until those commands exist, the CLI returns an unknown-command or unsupported
argument error rather than pretending the operation succeeded.
