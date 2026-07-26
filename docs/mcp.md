# Ida MCP reference

Ida exposes its graph to AI coding agents through a local Model Context
Protocol server. The MCP and CLI interfaces call the same index and query
functions.

Use MCP for an interactive coding session with repeated graph queries. Use the
[CLI](cli.md) for one-shot shell requests, scripts, diagnostics, and clients
without MCP support.

## Protocol and transport

Ida implements MCP revision `2025-06-18` over stdio:

- stdin and stdout carry newline-delimited UTF-8 JSON-RPC 2.0 messages;
- stdout contains protocol messages only;
- watcher, startup status (running/transport/project, exit instructions), and
  operational diagnostics go to stderr;
- the server advertises the `tools` capability with a stable tool list; and
- successful tool calls return both object-shaped `structuredContent` and its
  serialized JSON in a text content block.

The protocol behavior follows the
[MCP tools specification](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
and [stdio transport specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports).

## Setup

Initialize the Rails application once:

```sh
ida init /absolute/path/to/my_app
```

Configure an MCP client to launch Ida. The exact configuration file is
client-specific, but clients using the common `mcpServers` shape can use:

```json
{
  "mcpServers": {
    "ida": {
      "command": "/absolute/path/to/ida",
      "args": ["mcp", "/absolute/path/to/my_app"]
    }
  }
}
```

Run `ida mcp config` from inside the project to print this snippet with the
paths filled in, for Claude Code, Cursor, Codex, Pi, and OpenCode. Pass one or
more agent names to print only those, e.g. `ida mcp config claude-code`. Ida
only prints the snippet; it never edits agent configuration itself.

The MCP process starts an in-process filesystem watcher. It debounces normal
save bursts and periodically reconciles file metadata and hashes. It does not
start a shared daemon.

`ida mcp` is a single self-contained process: it does its own watching, so do
not also run `ida watch` against the same project. The two would race each
other for the same `.ida/ida.db` file lock. Use `ida watch` on its own only
when you want the index kept fresh for CLI use (`ida search`, `ida context`,
…) with no MCP client attached at all.

## Tools

Unknown arguments are rejected. String inputs declared below are limited to
1,000 characters by their MCP schemas.

### `ida_status`

Report index state, generation, indexed time, file/node/edge counts, and the
last indexing error.

Input:

```json
{}
```

### `ida_search`

Search indexed node names, qualified names, and paths.

| Parameter | Required | Default | Bounds |
| --- | --- | --- | --- |
| `query` | yes | — | 1–1,000 characters |
| `limit` | no | `20` | 1–100 |

```json
{"query":"ArticlesController","limit":10}
```

### `ida_context`

Return bounded, line-numbered source excerpts and direct relationships for a
task.

| Parameter | Required | Default | Bounds |
| --- | --- | --- | --- |
| `task` | yes | — | 1–1,000 characters |
| `file_limit` | no | `5` | 1–20 |
| `byte_limit` | no | `12000` | 1–20,000 |

```json
{"task":"How does GET /articles render its view?","file_limit":5}
```

The result reports whether source content is stale and whether a response was
truncated.

### `ida_node`

Explain an exact node and its direct incoming and outgoing relationships.
Ambiguous names return a tool error; retry with the stable ID or qualified
name.

| Parameter | Required | Default |
| --- | --- | --- |
| `name` | yes | — |

```json
{"name":"GET /articles"}
```

### `ida_path`

Find a short directed relationship path.

| Parameter | Required | Default | Bounds |
| --- | --- | --- | --- |
| `from` | yes | — | 1–1,000 characters |
| `to` | yes | — | 1–1,000 characters |
| `depth` | no | `4` | 1–6 |

```json
{
  "from":"GET /articles",
  "to":"app/views/articles/index.html.erb",
  "depth":4
}
```

### `ida_impact`

Return direct (one-hop) incoming and outgoing relationships — everything
that could plausibly break if `name` changes.

| Parameter | Required | Default | Bounds |
| --- | --- | --- | --- |
| `name` | yes | — | 1–1,000 characters |
| `depth` | no | `1` | 1–4 |
| `limit` | no | `50` | 1–100 |

```json
{"name":"index","depth":1,"limit":50}
```

Impact is a static graph traversal, not a claim of complete runtime behavior.
The default depth is deliberately `1`: a shared hub node (e.g. a Rails model
that many unrelated models `belongs_to`, like a multi-tenant `Organization`)
makes depth `2`+ surface most of the app as "impacted." Raise `depth` only
when you specifically want to walk past direct relationships and accept
that risk.

### `ida_refresh`

Refresh explicit changed paths or rebuild the complete scoped index.

| Parameter | Required | Default | Bounds |
| --- | --- | --- | --- |
| `paths` | no | full rebuild | at most 1,000 paths |

```json
{"paths":["app/models/article.rb","config/routes.rb"]}
```

Paths are validated against the project root and the shared project scope.
Omit `paths` to perform the same complete rebuild as `ida sync --rebuild`.

### `ida_unused`

List partials or view components with no resolved render edge.

| Parameter | Required | Default | Bounds |
| --- | --- | --- | --- |
| `kind` | yes | — | `partial` or `view_component` |

```json
{"kind":"partial"}
```

### `ida_duplicates`

List method or Stimulus controller declarations sharing one qualified name.

| Parameter | Required | Default | Bounds |
| --- | --- | --- | --- |
| `kind` | yes | — | `method` or `stimulus_controller` |

```json
{"kind":"method"}
```

### `ida_env`

List ENV variable reads, grouped by name. Takes no parameters.

```json
{}
```

## JSON-RPC example

Request:

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ida_search","arguments":{"query":"Article","limit":5}}}
```

Response shape:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [{"type":"text","text":"{\"result\":[...]}"}],
    "structuredContent": {"result":[...]}
  }
}
```

Invalid tool input is returned as a tool result with `isError: true`, allowing
the calling agent to correct its request. Malformed JSON-RPC and unknown
methods use JSON-RPC errors.
