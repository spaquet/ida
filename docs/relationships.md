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

The current route extractor recognizes one-line declarations using `get`,
`post`, `put`, `patch`, or `delete`, a quoted path, and a quoted
`controller#action` target:

```ruby
get "/articles", to: "articles#index"
post "/articles" => "articles#create"
```

The route declaration becomes an `exact` route node such as `GET /articles`.
Its target evidence retains `articles#index`.

Route macros such as `resources`, nested namespace expansion, mounts, concerns,
constraints, redirects, and dynamically computed targets are not resolved yet.

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
