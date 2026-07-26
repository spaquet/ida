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
