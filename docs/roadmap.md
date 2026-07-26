# Roadmap

Ideas beyond first-release scope. Nothing here enters scope without evidence
from real usage (see docs/product-requirements.md delivery order and
out-of-scope list).

## v2 candidates

- **Optional LLM disambiguation pass.** Deterministic analysis stays primary.
  Dynamic dispatch (`send`, `method_missing`, metaprogrammed routes/actions)
  can't be resolved statically — deterministic pass flags these as
  "unknown". An optional LLM pass could disambiguate only the
  flagged-unknown cases, not replace core analysis. Ship deterministic
  detection first, measure the unknown/false-positive rate, add LLM only if
  that rate is too high to be useful.
- **Route/controller/dead-code checks.** Cross-reference `routes.rb` against
  controller actions (POST route with no matching action, action with no
  route, unused methods via call graph). Fully mechanical for static
  dispatch; falls back to the LLM disambiguation pass above for dynamic
  cases.
- **Runtime JS/TS capture (rejected for now).** Considered a Chrome
  extension to capture JS/TS actually executed in the browser, to cover
  Stimulus/Turbo/JS gaps static analysis might miss. Rejected: breaks
  local-first/no-daemon/no-network stance (AGENTS.md), and runtime DOM
  capture doesn't fit the deterministic, git-diffable Turso index model.
  Static Stimulus/Turbo/JSX mapping already covers `data-*` attrs,
  controller identifiers, exports (architecture.md:108-115). Revisit only
  if static coverage gaps (e.g. dynamic `import()`) are measured and large
  enough to matter — same flag-unknown/disambiguate pattern as the LLM
  pass above, not a new capture channel.
- **Self-hosted Go Report Card.** Run own instance of
  github.com/gojp/goreportcard (or a fork) against this repo for a
  gofmt/vet/lint/ineffassign/misspell quality score. Separate infra from the
  CI test pipeline (a hosted service, not a CI job) — revisit after
  first-release scope lands.
