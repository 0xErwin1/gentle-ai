```yaml
schema: gentle-ai.verify-result/v1
evidence_revision: sha256:ccd70f8ad34793423fce17e5756c5399263e81f80d6705d18c833b6e1446751c
verdict: pass
blockers: 0
critical_findings: 0
requirements: 5/5
scenarios: 12/12
test_command: go test -count=1 ./...
test_exit_code: 0
test_output_hash: sha256:acc303ba8f43714d402706a105b20b4954cc8df6f8f8407e8c9cc8404eabcdcf
build_command: go vet ./...
build_exit_code: 0
build_output_hash: sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

## Verification Report

**Change**: `pi-optional-codegraph-integration`  
**Version**: N/A  
**Mode**: Strict TDD  
**Result**: PASS WITH WARNINGS — all 5 requirements, all 12 scenarios, and all 16 actual tasks are complete. Fresh focused and full tests, both real CodeGraph 1.2.0 schemas, fail-closed capability cases, vet, formatting, and diff checks passed.

### Terminal Result Contract

| Field | Result |
| --- | --- |
| Terminal schema verdict | `pass` |
| Blockers / critical findings | 0 / 0 |
| Requirements | 5/5 |
| Scenarios | 12/12 |
| Actual numbered tasks | 16/16 |
| Product code modified during verification | No; only this report was replaced |
| Commit / push / delegation | None |
| Final changed-line audit | 2,677: 637 tracked additions + 33 tracked deletions + 2,007 untracked lines |
| `.codegraph/` final state | Absent |
| `gentle-pi` changed paths | None |
| Unexpected untracked paths | None |

### Completeness

| Metric | Value |
| --- | ---: |
| Tasks total | 16 |
| Tasks complete | 16 |
| Tasks incomplete | 0 |
| Requirements compliant | 5/5 |
| Scenarios compliant | 12/12 |

`go run ./cmd/gentle-ai sdd-status pi-optional-codegraph-integration --json` independently reported `taskProgress.total=16`, `completed=16`, `allComplete=true`, `verify=all_done`, and `archive=ready`. The unavailable external `openspec` command was not used as evidence.

### Build, Tests, and Quality Execution

| Command | Exit | Evidence |
| --- | ---: | --- |
| Focused lifecycle/capability command across Pi adapter, community tool, CLI, and uninstall packages with `-count=1 -v` | 0 | Required edge cases and 10 fail-closed/accepted schema subcases passed; output `sha256:2091eee5c79baf71327742f9475f445f663cd8f057a16731d89d4445fd75a37a` |
| Real indexed/unindexed CodeGraph overlay harness with `-count=1 -v` | 0 | Both real production probes, reconciliations, and repeat no-op checks passed; output `sha256:8968914cd8f662136a68e6c6c3f4eec130fe61057014738c8d994edad3c8822e` |
| `go test -count=1 ./...` | 0 | Full repository suite passed uncached; output `sha256:acc303ba8f43714d402706a105b20b4954cc8df6f8f8407e8c9cc8404eabcdcf` |
| `go vet ./...` | 0 | No output; `sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| Focused package coverage with `-count=1` | 0 | 75.5% selected-package aggregate; command output `sha256:a9c950bc9b68a813fa495918b7f1d90a2886adc203e0a9bb237fd1fe0cda2f23` |
| `gofmt -l` over every changed Go path | 0 | No output |
| `git diff --check` | 0 | No output |

### Effective MCP Capability Verdict

| Capability case | Fresh runtime evidence | Verdict |
| --- | --- | --- |
| Real indexed CodeGraph 1.2.0 | Actual `tools/list` exposed one `codegraph_explore` with `required:["query"]`, `query:string`, `maxFiles:number`, and `projectPath:string` | ✅ Accepted |
| Real unindexed CodeGraph 1.2.0 | Actual server in a temporary Git repository without `.codegraph/` exposed `required:["query","projectPath"]` and the same safe property types | ✅ Accepted |
| Production reconciliation with each real schema | Real `probePiCodeGraphMCP` passed; child classified compatible; MCP adapter/read-only evidence reported; second reconciliation was byte-stable | ✅ Accepted |
| Missing adapter or process | Production probe executed without an adapter package | ✅ Fails closed |
| Adapter unavailable / initialize incomplete | Injected capability results | ✅ Fail closed |
| Missing or malformed explore tool | Empty tools and malformed schema | ✅ Fail closed |
| Unknown, missing-query, or duplicate required fields | Three distinct invalid schemas | ✅ Fail closed |
| Additional writable tool | Valid explore plus `codegraph_write` | ✅ Fails closed |
| Parent marker / substring transport | Parent marker and noncanonical MCP entry | ✅ Rejected as proof |

**Terminal schema verdict**: **PASS**. Both real CodeGraph schemas are accepted without weakening the one-tool, read-only, field-type, required-field, adapter, initialization, or canonical-transport checks.

### Ownership and Lifecycle Evidence

| Requested boundary | Fresh evidence | Result |
| --- | --- | --- |
| Preexisting marked user child | Direct test and real `Service.executePlan` hook restore the exact before-image after reconcile/uninstall | ✅ Preserved |
| True owned package overlay | Normal uninstall deletes the generated overlay while preserving the package-owned gentle-pi source | ✅ Deleted safely |
| Uninstall service hook | Two persisted service tests execute the actual Pi uninstall hook | ✅ Covered |
| Drift | Drifted child remains and produces a manual action | ✅ Preserved and reported |
| Repeat uninstall | Direct and service-hook second runs succeed without unrelated removal | ✅ Idempotent |
| Unreadable discovery | Injected `fs.PathError` propagates the permission failure instead of silently omitting discovery | ✅ Fails closed |
| Git root evidence | Test creates a real Git repository, proves `git -C ... rev-parse`, accepts absolute/relative roots, and rejects home/root/temp | ✅ Covered |
| Rollback | Failed manifest persistence restores prior child/MCP bytes, removes a newly created package overlay, and clears success evidence | ✅ Covered |
| Sync drift | Final post-component reconciliation restores and re-verifies a missing owned child | ✅ Covered |
| gentle-pi isolation | Package source remains byte-identical and no changed path belongs to gentle-pi | ✅ Preserved |

### Spec Compliance Matrix

| Requirement | Scenario | Covering runtime test/evidence | Result |
| --- | --- | --- | --- |
| Optional selection boundary | Pi unselected | `TestPiCodeGraphUnselectedIsNoOp`; install-pipeline deselection test | ✅ COMPLIANT |
| Optional selection boundary | Selection records ownership | `TestInstallWithHomeReportsWorkspaceChildAndOwnershipTarget` | ✅ COMPLIANT |
| Selected provisioning and direct verification | Compatible child | Child classification test plus both real production-schema reconciliations | ✅ COMPLIANT |
| Selected provisioning and direct verification | Marker-only evidence | Parent-marker and noncanonical MCP rejection tests | ✅ COMPLIANT |
| Selected provisioning and direct verification | Unsupported child | Compatible/guidance-only test proves guidance without unsupported tool injection | ✅ COMPLIANT |
| Root-scoped lazy initialization | Index absent | Production guidance ordering test plus real unindexed Git-root schema harness | ✅ COMPLIANT |
| Root-scoped lazy initialization | Init fails | Guidance test requires attempted init/use before a reasoned fallback | ✅ COMPLIANT |
| Reconciliation, idempotence, and recovery | Sync drift | `TestPiCodeGraphRefreshRestoresMissingOwnedChild` and final sync-step ordering | ✅ COMPLIANT |
| Reconciliation, idempotence, and recovery | Repeat provisioning | Persisted byte-idempotence test and both real-schema second runs | ✅ COMPLIANT |
| Reconciliation, idempotence, and recovery | Update fails | Existing-child/MCP rollback and new-overlay/no-success rollback tests | ✅ COMPLIANT |
| Ownership-safe removal | User-config uninstall | Direct adoption, overlay, drift, and both real service-hook tests | ✅ COMPLIANT |
| Ownership-safe removal | Repeat uninstall | Direct and service repeat-uninstall assertions | ✅ COMPLIANT |

**Compliance summary**: 12/12 scenarios compliant.

### Requirement Correctness

| Requirement | Status | Static and runtime evidence |
| --- | --- | --- |
| Optional selection boundary | ✅ Implemented | Selection gates reconciliation; deselection invokes owned cleanup; no index is created by install |
| Selected provisioning and direct verification | ✅ Implemented | Canonical MCP merge, four classifications, child guidance/tools, real schema probe, and strict negative cases |
| Root-scoped lazy initialization | ✅ Implemented | Generated contract resolves and validates Git root, checks index first, initializes once, and gates fallback |
| Reconciliation, idempotence, and recovery | ✅ Implemented | Reconcile-last lifecycle, byte no-op, missing-child repair, before-image rollback, delayed manifest commit |
| Ownership-safe removal | ✅ Implemented | Adoption provenance/before-image, overlay deletion, drift preservation, service hook, repeat-safe removal |

### Task Verification

| Phase | Tasks | Evidence verdict |
| --- | --- | --- |
| Pi contracts and selection | 1.1–1.4 | ✅ 4/4 complete, including persisted unreadable-discovery and ownership-target tests |
| Provisioning and direct verification | 2.1–2.4 | ✅ 4/4 complete, including real schemas, fail-closed checks, and Git-root evidence |
| Reconciliation and recovery | 3.1–3.4 | ✅ 4/4 complete, including sync ordering, idempotence, exact rollback, overlay deletion, and no-success evidence |
| Ownership-safe removal and evidence | 4.1–4.4 | ✅ 4/4 complete, including real uninstall service-hook tests, drift/repeat behavior, docs, and quality gates |

**Task summary**: the artifact contains **16 actual numbered tasks**, all 16 are checked, all 16 objectives now have matching source/runtime/documentation evidence, and apply progress records `task_count: 16` / `completed_task_count: 16`.

### Design Coherence

| Decision | Followed? | Evidence |
| --- | --- | --- |
| Keep gentle-pi CodeGraph-agnostic | ✅ Yes | No gentle-pi changed path; package source preservation executes at runtime |
| Use observed Pi MCP contract | ✅ Yes | Canonical `mcpServers.codegraph` plus both actual CodeGraph 1.2.0 schemas |
| Discover effective user/project/package children | ✅ Yes | Precedence, workspace target, package overlay, and unreadable-discovery tests |
| Inject least-privilege tools | ✅ Yes | Only children with explicit `bash` become compatible and receive `mcp` |
| Never accept a parent marker as capability | ✅ Yes | Direct verification rejects marker-only evidence |
| Reconcile after refresh and remain idempotent | ✅ Yes | Sync final-step and repeated-provision tests pass |
| Restore before-images and remove only owned artifacts | ✅ Yes | Adoption, drift, overlay, rollback, deselection, and service-hook tests pass |

### TDD Compliance

| Check | Result | Details |
| --- | --- | --- |
| TDD evidence reported | ✅ | Apply progress contains the corrected 16-row task matrix and focused remediation RED/GREEN records |
| Task objectives mapped | ✅ | 16/16 tasks map to executable tests, documentation review, or the final quality gate |
| RED evidence present | ✅ | Original and remediation failure classes are recorded with exact focused commands |
| GREEN confirmed fresh | ✅ | Every mapped persisted test passes with `-count=1`; full suite also passes uncached |
| Triangulation adequate | ✅ | Success/failure schemas, user/overlay ownership, drift/repeat, root safety, and rollback variants all execute |
| Safety net and rollback boundary | ✅ | Apply progress records pre-remediation safety nets and bounded rollback evidence |

**TDD compliance**: 6/6 checks passed.

### Test Layer Distribution

| Layer | Change-related top-level tests | Files | Tool |
| --- | ---: | ---: | --- |
| Unit/structural | 12 | 3 | Go `testing` |
| Integration/runtime fixture | 22 | 4 | Go `testing`, `t.TempDir`, filesystem and service lifecycle |
| E2E | 0 | 0 | Not used |
| **Persisted total** | **34** | **5** | |

Fresh verification additionally ran one external two-case real MCP harness without adding it to the repository.

### Changed File Coverage

Go reports statement rather than branch coverage. Selected-package aggregate is **75.5%**.

| Changed production file | Statement coverage | Rating |
| --- | ---: | --- |
| `internal/agents/pi/adapter.go` | 66.9% | ⚠️ Low |
| `internal/cli/run.go` | 79.5% | ⚠️ Low |
| `internal/cli/sync.go` | 82.6% | ⚠️ Acceptable |
| `internal/components/communitytool/codegraph_guidance.go` | 72.4% | ⚠️ Low |
| `internal/components/communitytool/pi_codegraph.go` | 71.7% | ⚠️ Low |
| `internal/components/communitytool/tool.go` | 77.1% | ⚠️ Low |
| `internal/components/uninstall/service.go` | 61.6% | ⚠️ Low |

Coverage is informational and non-blocking under the Strict TDD verification contract.

### Assertion Quality

All five created/modified change-related test files were inspected. Tests call production boundaries and assert concrete bytes, classifications, errors, state, ordering, tools, ownership, and side effects. No tautologies, ghost loops, assertion-free production paths, smoke-only tests, or mock-heavy files were found.

**Assertion quality**: ✅ All assertions verify real behavior.

### Quality Metrics

**Formatter**: ✅ No changed Go path reported by `gofmt -l`  
**Linter**: ➖ No configured linter  
**Static checker**: ✅ `go vet ./...` passed  
**Whitespace integrity**: ✅ `git diff --check` passed  
**Full tests**: ✅ Uncached full suite passed  
**Generated index residue**: ✅ `.codegraph/` absent

### Issues Found

**CRITICAL**: None.

**WARNING**:
1. Six of seven changed production files remain below the informational 80% statement-coverage threshold.
2. The actual MCP process/schema harness is verification-only; persisted tests pin both observed schemas and all fail-closed cases, but the subprocess probe itself still has low repository coverage.

**SUGGESTION**: Persist a hermetic subprocess fixture only if maintaining the real MCP process boundary in CI becomes practical; do not replace the current deterministic schema tests with a network/tool-install dependency.

### Verdict

**PASS WITH WARNINGS**

Archive is unblocked. The machine-readable terminal verdict is `pass`: all requirements, scenarios, and 16 tasks are proven, with only informational coverage warnings remaining.
