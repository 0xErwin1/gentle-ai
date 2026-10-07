# Doctor executable-health delivery
Feature-branch chain for issue #5127; no merge, main changes, or force-push.
Preserve the verified final functional candidate while reviewing children within 400 changed lines.

## Specs
S1. Publish the correction: "hagamos eso" (commit and PR).
S2. Delivery strategy: "Cadena con tracker (recomendada)".
S3. Final integration reference: "Sí, usar Closes #5127".
S4. Publication authority: "Autorizar entrega, sin merge".
S5. Base-repository branches: "Autorizar esas tres ramas en el repositorio base" — `fix/doctor-executable-health`, `fix/doctor-executable-health-probe`, `fix/doctor-executable-health-cleanup` on `github.com/Gentleman-Programming/gentle-ai`, using `dnlrsls`; no merge, force-push, or changes to `main`.

## Tasks
- T1 | S1-S5 | parent: record chain, preserve tested snapshot, create tracker branch | done | commit: 6791e1557f1b37ed1e4861bd0016f45f89bea11d
- T2 | S1-S2 | parent + independent verifier: execution-health slice with covering tests/docs, <=400 lines | done | commit: 5e8e6c3cf831c0dc0b3a43adb9b9f6419969092b
- T3 | S1-S2 | parent + independent verifier: descendant-cleanup slice, <=400 lines, final functional snapshot equality | done | commit: cdefb9b4d3f0129cb94b6dc649fac81684276509
- T4 | S1-S5 | parent: publish draft tracker and dependent PRs, verify identities/bases/type labels/budgets | in_progress | commit: publication record pending

## Log
L1. Original delivery request: "hagamos eso".
L2. User selected "Cadena con tracker (recomendada)", "Sí, usar Closes #5127", and "Autorizar entrega, sin merge".
L3. User additionally selected "Autorizar esas tres ramas en el repositorio base" for the exact branches and destination in S5.
L4. Starting functional candidate: 12 paths, 537 changed lines; native review `review-562ea3fd1e9e8d00` approved and acknowledged. Complete clean Linux suite passed; focused Windows regressions passed; full Windows suite timed out. Splitting creates intermediate candidates needing their own checks; full-suite evidence applies only where functional bytes match.
L5. Verified GitHub actor `dnlrsls`, base-repository permission `MAINTAIN`, approved issue #5127, existing `type:bug` catalog label, and Git author Daniel Rosales with public GitHub noreply identity. Validated base `310ff35f4989a724876185a6538962ed157143a6` is an ancestor of current `origin/main`. Branches in S5 did not exist locally or remotely at admission.
L6. Tracker commit `6791e1557f1b37ed1e4861bd0016f45f89bea11d` records the plan. All 12 functional paths were copied and byte-verified under ignored diagnostics before reducing any slice. Python's Windows Store alias was unavailable; Node performed preservation. No installation or cleanup of Git's unreachable objects was attempted.
L7. T2 focused Windows doctor/refusal tests and `go run ./internal/gofmtcheck` passed. Independent source verification found the deferred process-tree guarantee still present in the first-slice docs; removed that sentence from T2 and will restore the original final docs in T3. Help text only promises bounded probes and already matches T2. Verifier had no shell tools; Linux full-suite and Git facts remain parent-observed evidence.
L8. T2 complete clean Linux suite passed (`go test -p 4 ./... -count=1 -timeout=10m`, job 10 exit 0). Independent doc/help readback resolved the finding. Native `review-d8d176ce8fb340fa` approved and acknowledged the 352-line slice; commit `5e8e6c3cf831c0dc0b3a43adb9b9f6419969092b`. Two native warnings were informational, with no correction route.
L9. T3 restored all 12 original functional paths and SHA256-verified equality with the approved final snapshot, including the deferred docs guarantee. This reuses the original full clean Linux suite for identical functional bytes; new chain-tracking metadata is additional, non-runtime documentation.
L10. T3 Windows and clean Linux focused doctor/refusal checks passed, as did formatting. Independent source verification found no blocker. Native `review-f0350108e95db5ad` approved and acknowledged the 211-line slice; commit `cdefb9b4d3f0129cb94b6dc649fac81684276509`. The docs warning about detached POSIX descendants remains informational; no correction was offered.
L11. Publication plan: draft/no-merge tracker to `main` with the closing reference, probe child to tracker, cleanup child to probe; both children link the approved issue without closing it. All three use `type:bug`. Required GitHub checks are the three issue/type checks, Unit Tests, and E2E Tests on ubuntu/arch/fedora; local E2E and full Windows suite are not claimed passed. Push only the three authorized branches, without force or merge.
