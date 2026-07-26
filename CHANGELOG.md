# Changelog

Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
