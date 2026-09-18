# Acceptance fixtures

Fixed workspaces for the Agent Hub 2×10 suite (`docs/AGENT-HUB-ACCEPTANCE-2x10.md`).

| Dir | Tasks |
|-----|-------|
| `fixture-bug/` | #2 — failing `TestAcceptanceBug` (expectation wrong; `Add` is correct) |
| `fixture-rename/` | #4 — `shared.Helper` used from `alpha` and `beta` |
| (in-tree) | #3 — `domain.WithEffectiveModel` already covered by `internal/domain/run_configuration_test.go` |
| `fixture-secret-review/` | #5 — patch with intentional secret leak for review-only |

Live runner: `POINT_ACCEPTANCE_LIVE=1 node scripts/run-hub-acceptance.mjs --pass=1`
