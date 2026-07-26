# Relationship resolution

Ida records only relationships supported by direct syntax or an unambiguous
Rails convention. Every edge includes its source location, extractor evidence,
and confidence.

This page describes the relationships implemented today. The broader resolver
coverage in the [product requirements](product-requirements.md) remains the
roadmap.

## Confidence

- `exact` means the fact is directly present in source syntax.
- `convention` means one unique target follows a Rails naming convention.
- `lsp` means an available LSP server (`ruby-lsp`, `typescript-language-server`)
  resolved a `textDocument/definition` request to exactly one already-indexed
  node, after deterministic resolution left the node unresolved. LSP
  enrichment is additive and best-effort: a missing server, a failed
  request, a timeout, or an ambiguous (zero/multiple-location) result never
  produces an edge and never fails the index. It only runs on a full
  `ida sync`, not on watch-triggered incremental refreshes, to keep the
  interactive watch loop fast. Today it covers unresolved associations
  (Ruby), and unresolved `js_import`/`jsx_use` (TypeScript). Resolving
  Active Record association macros specifically also requires the Ruby LSP
  Rails add-on with a booted application; plain `ruby-lsp` does not treat a
  bareword symbol like `:comments` as pointing at a class, so without the
  Rails add-on this enrichment safely no-ops rather than guessing.
- If resolution has zero or multiple plausible targets, Ida omits the edge.

Ambiguous-candidate reporting is planned but is not implemented yet.

## Route declarations

The route extractor recognizes:

- one-line declarations using `get`, `post`, `put`, `patch`, or `delete`, a
  quoted path, and a quoted `controller#action` target;
- `root`, with or without an explicit `to:`;
- `resources`/`resource` macros, expanded into their conventional actions
  (`index`, `create`, `new`, `show`, `edit`, `update`, `destroy` for
  `resources`; the same set without `index` for the singular `resource`,
  which is pluralized for its controller name per Rails convention);
  `only:`, `except:`, and `controller:` options are honored;
- `namespace :name do ... end` blocks, which prefix both the URL path and the
  controller module for everything nested inside;
- any other `do ... end` block (`scope`, `member`, `collection`,
  `constraints`, and similar) is tracked only to keep nesting balanced; routes
  declared inside are not path-prefixed beyond their enclosing namespace.

```ruby
get "/articles", to: "articles#index"
post "/articles" => "articles#create"
root to: "pages#home"

namespace :admin do
  resources :articles, only: [:index, :show]
end
```

Each route becomes an `exact` route node such as `GET /articles` or
`GET /admin/articles/:id`. Its target evidence retains the resolved
`controller#action`, e.g. `admin/articles#show`.

Mounts, concerns, constraints, redirects, and dynamically computed targets are
not resolved yet.

## `routes_to`

For a route targeting `articles#index`, Ida looks for:

```text
app/controllers/articles_controller.rb
```

It adds a `routes_to` edge only when that file contains exactly one directly
declared method named `index`. The edge confidence is `convention`.

Namespaced controller paths such as `admin/articles#index` map to
`app/controllers/admin/articles_controller.rb`.

## `renders`

After resolving an action, Ida looks for files beginning with the conventional
view path:

```text
app/views/articles/index.
```

It adds a `renders` edge only when exactly one indexed file matches. For
example:

```text
GET /articles
  --routes_to-->
index (app/controllers/articles_controller.rb)
  --renders-->
app/views/articles/index.html.erb
```

Multiple matching formats, variants, or templates are intentionally left
unresolved rather than selecting one. Layouts, redirects, and Turbo Stream
selection are still planned but not implemented.

## Partials

Any file under `app/views/` whose basename starts with `_` becomes a
`partial` node, e.g. `app/views/articles/_form.html.erb` -> qualified name
`articles/form`.

`render "name"` and `render partial: "name"` calls in ERB/HTML templates,
and `render partial: "name"` calls in Ruby files (controllers, helpers),
become a `partial_use` node. The bare `render "name"` shorthand is only
scanned in templates, since the identical syntax in a controller/helper
renders a full template rather than a partial.

Ida then applies Rails' own lookup convention: a name containing a `/` is
rooted at `app/views/` (`"shared/flash"` -> `app/views/shared/_flash.*`);
otherwise it is looked up next to the referencing template (`"form"` from
`app/views/articles/index.html.erb` -> `app/views/articles/_form.*`). It adds
a `renders_partial` edge only when exactly one indexed partial matches.

Object-based shorthand (`render @article`, `render @comments`) and
dynamically computed partial names are not resolved, since they require
type information Ida does not track.

## ViewComponent

A class declared as `class Foo < ViewComponent::Base` (or
`< ApplicationComponent`) becomes a `view_component` node, in addition to
the `class` node every Ruby class declaration already produces.

`render FooComponent.new(...)` — including the `.with_collection.new`
collection form — becomes a `view_component_use` node wherever it appears
(Ruby or template), resolved by unqualified class name the same way
`has_many`/`belongs_to` associations resolve to their target class. A
`renders_component` edge is added only when exactly one `view_component`
matches.

## Finding unused partials and components

`ida unused partial` and `ida unused view_component` (MCP tool
`ida_unused`, argument `kind`) list every `partial`/`view_component` node
with no incoming `renders_partial`/`renders_component` edge. Because
object-based `render @model` and dynamically computed render targets are
never resolved, a partial or component rendered only that way will appear
here even though it is actually used — treat the result as a lead to check,
not a guaranteed-dead-code list.

## Associations

`has_many`, `has_one`, `belongs_to`, and `has_and_belongs_to_many`
declarations inside a class become an association node, attributed to the
nearest enclosing class by indentation:

```ruby
class Article
  has_many :comments
end
```

Ida looks for a unique class node named by the Rails singularization/
camelization convention (`comments` -> `Comment` for `has_many`/`habtm`;
the symbol camelized as-is for `has_one`/`belongs_to`) and adds an edge whose
kind is the macro itself (`has_many`, `has_one`, `belongs_to`,
`has_and_belongs_to_many`) at `convention` confidence. Zero or multiple
matching classes leave the association unresolved.

`validates`/`validate` declarations and `scope :name` declarations become
`validation` and `scope` nodes on the enclosing class; they are not yet
resolved to edges.

## Stimulus

JS/TS/JSX/TSX files are parsed with tree-sitter. A file under a `controllers/`
directory named `*_controller.{js,ts}` whose default-exported class extends
something named `Controller` becomes a `stimulus_controller` node. Its
identifier follows Stimulus's own convention: underscores become dashes, and
nested `controllers/` subdirectories join with `--`
(`controllers/nested/date_picker_controller.js` -> `nested--date-picker`).

Inside that class, `static targets/values/classes/outlets = [...]`/`{...}`
become `stimulus_target`/`stimulus_value`/`stimulus_class`/`stimulus_outlet`
nodes, and its methods become `method` nodes qualified as
`<identifier>#<method>` (no distinction is made between lifecycle and action
methods).

Templates (ERB, HTML, JSX/TSX) are scanned by regex/line-scan, matching
`extract.go`'s existing route/association style rather than a full
ERB/HTML grammar, for `data-controller="a b"` and
`data-action="click->identifier#method"` attributes, recorded as
`stimulus_controller_use`/`stimulus_action_use` nodes.

`stimulus_controller_use` resolves, when its identifier uniquely names one
`stimulus_controller`, to a `stimulus_controller` edge at `convention`
confidence. `stimulus_action_use` resolves the same way to a
`stimulus_action` edge targeting the matching `method` node. Ambiguous
identifiers (two controllers registering the same name) are omitted, not
guessed.

`data-*-target` attributes and Stimulus outlets/classes are recorded as
declaration nodes but not yet resolved to template uses.

## Turbo

`broadcasts_to`, `broadcasts`, and the `broadcast_*_to`/`broadcast_refresh*`
model macros become `turbo_broadcast` nodes on the enclosing class (same
indentation-stack attribution as `association`/`scope`). `turbo_frame_tag
"name"`/`<turbo-frame id="name">` and `turbo_stream_from "name"` become
`turbo_frame`/`turbo_stream_from` nodes wherever they appear in a template.

These are searchable nodes, not yet resolved to edges — pairing a frame or
broadcast declaration to the specific navigation/stream response that
targets it is heuristic across files and is deferred, the same way
`validates`/`scope` are recorded without an edge today.

## React

Tree-sitter parses `.js`/`.jsx`/`.ts`/`.tsx` modules for `import` statements
(`js_import`, qualified name = the raw specifier) and top-level
function/class/const declarations (`js_export`), narrowed to `js_component`
when the name is PascalCase and the body contains JSX, or `js_hook` when the
name matches `use[A-Z]...`. A capitalized JSX tag anywhere in the file
becomes a `jsx_use` node.

- `js_import` with a relative specifier (`./x`, `../x`) resolves to a `file`
  node by probing `.js/.jsx/.ts/.tsx` and `index.*` variants, the same way
  Node module resolution would; bare/package specifiers (`"react"`) are left
  unresolved. Edge kind `imports`.
- `jsx_use` resolves to a `js_component`/`js_export` node with the matching
  name, preferring one declared in the same file, then one reached through
  an already-resolved `imports` edge from the same file. Edge kind
  `jsx_renders`.
- `react_component("Name")` Ruby/ERB helper calls become `react_mount` nodes,
  resolved to a uniquely named `js_component`/`js_export` project-wide. Edge
  kind `mounts`.

All three omit the edge on zero or multiple candidates.

## Tailwind CSS

`tailwind.config.js`/`.ts` is parsed for `theme.extend.<category>.<name>`
keys, e.g. `colors.primary`, becoming a `tailwind_token` node (qualified name
`colors.primary`, name `primary`). Templates, JSX/TSX components, and
authored CSS `@apply` rules (tree-sitter-css) are scanned for static
`class="..."`/`className="..."`/`@apply ...` values, aggregated per file into
a `class_attr_use` node — dynamically interpolated class expressions
(`<%= %>`, `{...}`) are skipped rather than guessed at.

A token resolves to a `tailwind_uses` edge, at `convention` confidence, for
every file whose static class list contains a matching utility (an exact
match, or a common Tailwind prefix like `bg-`/`text-`/`border-` joined to the
token name). Unlike other resolvers this is not a unique-target match: a
design token legitimately fans out to many files, and each edge is
independently verified rather than chosen between competing candidates.
Individual built-in utility classes never become their own nodes.

## Document mentions

Every document section's explicit code-symbol mentions (backtick-quoted spans
with no whitespace) are recorded. For a locally indexed document, a mention
that uniquely matches a graph node's name or qualified name becomes a
`mentions` edge from the document section to that node, at `convention`
confidence. Remote documents record mentions but are not attached to the
graph, since they carry no source file. Outbound links (Markdown and HTML) are
recorded per section but do not become edges.

## Querying relationships

Use either interface:

```sh
ida node "GET /articles"
ida path "GET /articles" app/views/articles/index.html.erb
ida impact index
ida context "How does GET /articles render its view?"
```

The equivalent MCP tools are `ida_node`, `ida_path`, `ida_impact`, and
`ida_context`.

Stable IDs are derived from project-relative path, node kind, qualified name,
and source line. Absolute filesystem paths and database row IDs are not graph
identities.
