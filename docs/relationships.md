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
unresolved rather than selecting one. Explicit `render` calls, layouts,
partials, redirects, Turbo Stream selection, and component rendering are
planned but not implemented.

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
