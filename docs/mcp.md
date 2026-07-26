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
- watcher and operational diagnostics go to stderr;
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

Return bounded likely upstream and downstream relationships.

| Parameter | Required | Default | Bounds |
| --- | --- | --- | --- |
| `name` | yes | — | 1–1,000 characters |
| `depth` | no | `2` | 1–4 |
| `limit` | no | `50` | 1–100 |

```json
{"name":"index","depth":2,"limit":50}
```

Impact is a static graph traversal, not a claim of complete runtime behavior.

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
