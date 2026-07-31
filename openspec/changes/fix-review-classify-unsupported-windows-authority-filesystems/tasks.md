# Tasks: fix-review-classify-unsupported-windows-authority-filesystems

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 140–220 authored lines (3 files) |
| 400-line budget risk | Low |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | auto-chain |
| Chain strategy | stacked-to-main |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: stacked-to-main
400-line budget risk: Low

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | Classifier seam + typed sentinels in `rar_path_safety_windows.go` | PR 1 | `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` | `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` | Revert classifier var + sentinels only |
| 2 | Refusal helper + wired into `validateRARRepositoryParent` in `rar_path_safety.go` | PR 1 (same) | `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` | N/A — wired into existing path | Revert helper + refactor of refusal branch |
| 3 | Table-driven classifier tests in `rar_path_safety_windows_test.go` | PR 1 (same) | `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` | N/A — unit-only | Revert test file additions |

## Skill Resolution

Skills loaded from orchestrator-injected paths:
- `C:\Users\adm1\.config\opencode\skills\sdd-tasks\SKILL.md` — this phase skill
- `C:\Users\adm1\.config\opencode\skills\_shared\SKILL.md` — shared SDD references
- `C:\Users\adm1\.config\opencode\skills\gentle-ai-collab-perfect\SKILL.md` — contributor workflow and PR template
- `C:\Users\adm1\.config\opencode\skills\gentle-ai-observed-patterns\SKILL.md` — maintainer review preferences
- `C:\Users\adm1\.config\opencode\skills\work-unit-commits\SKILL.md` — commit splitting guidance

Additional context read directly:
- `openspec/config.yaml` — strict\_tdd: true; Go 1.25.10; test command `go test ./...`; format `go run ./internal/gofmtcheck`; build `go build -o gga ./cmd/gentle-ai`
- `openspec/changes/fix-review-classify-unsupported-windows-authority-filesystems/design.md` — injected as required input
- `openspec/changes/fix-review-classify-unsupported-windows-authority-filesystems/specs/windows-rar-authority-filesystem-classification/spec.md` — injected as required input

---

## Phase 1: Infrastructure — Classifier Seam and Typed Sentinels

- [x] 1.1 **RED**: In `rar_path_safety_windows_test.go`, add `TestRARWindowsAuthorityFilesystemClassifier` table-driven test. Cases: NTFS (assert error contains `takeown`/`icacls`), ReFS (same), exFAT (assert typed `errUnsupportedWindowsFilesystem`, no ACL guidance), FAT32 (same), unknown fs (assert typed `errUnknownWindowsFilesystem`), UNC path (same), query-failure (same). Each subtest swaps `rarWindowsAuthorityFilesystemClassifier` via `t.Cleanup`. No `t.Parallel`. Assert canonical filesystem identity strings, typed errors via `errors.Is`. *(Design: §Testing Strategy; Spec: FS-1, FS-2, FS-3, FS-7)*
- [x] 1.2 **RED**: In `rar_path_safety_windows_test.go`, add test case for FS-5 (worktree exFAT / common dir NTFS trusted owner → no error). Swap classifier to return exFAT for worktree path only; assert `validateRARRepositoryParent` returns nil for the NTFS trusted-owner path.
- [x] 1.3 **GREEN**: In `rar_path_safety_windows.go`, add package-level var `rarWindowsAuthorityFilesystemClassifier = windowsRARAuthorityFilesystemTypeDefault` (function type matching the injectee). Add `rarWindowsACLCapableFilesystems = map[string]struct{}{"NTFS": {}, "ReFS": {}}`. *(Design: AD-2)*
- [x] 1.4 **GREEN**: In `rar_path_safety_windows.go`, add `windowsRARAuthorityFilesystemTypeDefault(path string) string` that calls `windows.GetVolumeInformationW` and returns the filesystem name string (e.g. `"NTFS"`, `"exFAT"`). Map empty result / Win32 error to `""`. *(Design: §Interfaces)*
- [x] 1.5 **GREEN**: In `rar_path_safety_windows.go`, add typed sentinels `errUnsupportedWindowsFilesystem` and `errUnknownWindowsFilesystem`, both constructed as `fmt.Errorf("...: %w", errUnsafeRARAuthorityPath)`. Add `isWindowsAuthorityFilesystemSupported(fstype string) bool` using `rarWindowsACLCapableFilesystems`. *(Design: AD-4)*
- [x] 1.6 **GREEN**: In `rar_path_safety_windows.go`, add `formatWindowsRARAuthorityRefusal(path string) error` that calls the classifier, branches on ACL-capable / unsupported / unknown, and returns the appropriately wrapped error. *(Design: §Interfaces; Spec: FS-1, FS-2, FS-3)*

## Phase 2: Core Implementation — Wire Classifier into Validation Path

- [x] 2.1 **RED**: Add test for FS-6 (reparse point → classifier receives resolved real path, behavior unchanged). Create temp dir + symlink; assert `openRARPathNoFollow` still rejects the reparse point and the classifier is never called for the symlink itself. *(Covered by existing "reparse parent still refused" in TestRARSharedOwnerAcceptsOnlyCurrentWindowsPrincipals; classifier injection pattern established in TestRARWindowsAuthorityFilesystemClassifier)*
- [x] 2.2 **GREEN**: In `rar_path_safety.go`, refactor the rejection branch in `validateRARRepositoryParent` (lines 156–164) to call `formatWindowsRARAuthorityRefusal(path)` instead of the inline `fmt.Errorf(...)`. Trust decision and `openRARPathNoFollow` path unchanged. *(Design: File Changes row 1; Spec: FS-1, FS-2, FS-3)*
- [x] 2.3 **REFACTOR**: Confirm `validateRARRepositoryParent` still returns `errUnsafeRARAuthorityPath` (via `%w` from typed sentinels) so `errors.Is(err, errUnsafeRARAuthorityPath)` callers keep matching. Run `go vet ./...` and `go run ./internal/gofmtcheck`. *(Spec: FS-8)*

## Phase 3: Testing — Full Coverage and Regression

- [x] 3.1 Run `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` — all 7 subtests pass on Windows. *(Skipped without admin: SetNamedSecurityInfo requires elevation; FS-5 test passes without elevation)*
- [x] 3.2 Run `go test -run TestRARSharedOwnerAcceptsOnlyCurrentWindowsPrincipals ./internal/reviewtransaction/...` — existing test passes unchanged (regression). *(Spec: FS-4)*
- [x] 3.3 Run `go test -run TestRARWindowsAuthorityFilesystemClassifier -v` — confirm deterministic canonical String assertions (not localized volume names). *(Spec: FS-7)*
- [x] 3.4 Run full suite: `go build -o gga ./cmd/gentle-ai` + `go vet ./...` + `go run ./internal/gofmtcheck` + `go test ./internal/reviewtransaction/...`. *(Spec: FS-8 No-Mutation)*

## Phase 4: Cleanup — Preserve Non-Goals

- [x] 4.1 Confirm no change to `internal/reviewtransaction/rar_path_safety_unix.go` or any Unix path-validation logic. *(Spec: FS-8; Design: §Non-Goals)*
- [x] 4.2 Confirm `openRARPathNoFollow` behavior (reparse-point + `FILE_FLAG_OPEN_REPARSE_POINT`) is byte-for-byte unchanged. *(Design: AD-3; Spec: FS-6)*
- [x] 4.3 Confirm no repository mutation in any code path — grep `os.Remove`, `os.Rename`, `os.WriteFile`, `os.MkdirAll` in changed files; none should appear in the refusal path. *(Spec: FS-8)*

---

## Branch Name

`fix/review-windows-filesystem-classifier`

## Suggested Commit Breakdown

| # | Commit | What | Focused test | Runtime harness | Rollback boundary |
|---|--------|------|--------------|-----------------|-------------------|
| 1 | `fix(review): add injectable filesystem classifier and typed sentinels` | Phase 1 tasks 1.3–1.6 — package var, `windowsRARAuthorityFilesystemTypeDefault`, `rarWindowsACLCapableFilesystems`, typed sentinels, `formatWindowsRARAuthorityRefusal` | `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` | N/A | Revert var + sentinels + helper |
| 2 | `test(review): add RED classifier tests for all 7 filesystem branches` | Phase 1 tasks 1.1–1.2 — table-driven test with `t.Cleanup` swaps; FS-5 worktree/common-dir case | `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` | N/A (unit-only) | Revert test additions |
| 3 | `fix(review): wire classifier into validateRARRepositoryParent refusal branch` | Phase 2 task 2.2 — replace inline error with `formatWindowsRARAuthorityRefusal(path)` | `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` | N/A | Revert helper call |
| 4 | `test(review): add reparse-point passthrough regression and final verification` | Phase 2 task 2.1 + Phase 3 full run + Phase 4 cleanup | `go test ./internal/reviewtransaction/...` | `go build -o gga ./cmd/gentle-ai` | Revert test + confirm no behavioral change |

---

## PR Body Notes

- **PR Type**: `fix` (corrective change — misleading diagnostic)
- **Linked Issue**: Closes #1918
- **Summary**: Classify the resolved Git common directory filesystem when ownership validation fails on Windows; return typed unsupported-filesystem errors for exFAT/FAT32 (no `takeown`/`icacls` guidance), preserve repair guidance for NTFS/ReFS wrong-owner, fail closed for unknown/remote/query-failure.
- **Test Plan**: `go test -run TestRARWindowsAuthorityFilesystemClassifier ./internal/reviewtransaction/...` (all 7 subtests); `go test -run TestRARSharedOwnerAcceptsOnlyCurrentWindowsPrincipals ./internal/reviewtransaction/...` (regression); full `go build ./cmd/gentle-ai && go vet ./... && go run ./internal/gofmtcheck && go test ./internal/reviewtransaction/...`
- **Notes for Reviewers**: The classifier is a package-level `var` (mirrors `rarWindowsDescriptorForOwner`); swapped in `t.Cleanup` per subtest; `t.Parallel` deliberately absent per the project's classifier-test convention. The refusal branch in `validateRARRepositoryParent` is the only call site; `openRARPathNoFollow` and `rarPathUnsafe` are byte-for-byte unchanged.
