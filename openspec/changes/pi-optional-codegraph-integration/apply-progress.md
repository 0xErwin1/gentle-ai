```yaml
schema: gentle-ai.remediation-result/v1
status: complete
failed_verify_revision: sha256:608b7c497ba2bde1a0f8beda178f82fec1e7920fc58acb0819855368c43549df
task_count: 16
completed_task_count: 16
focused_tests: passed
runtime_harness: passed
rollback_boundary: recorded
next_recommended: sdd-verify
```

```json
{
  "schema": "gentle-ai.remediation-evidence/v1",
  "failed_verify_revision": "sha256:608b7c497ba2bde1a0f8beda178f82fec1e7920fc58acb0819855368c43549df",
  "commands": [
    {
      "command": "go test -count=1 ./internal/agents/pi ./internal/components/communitytool ./internal/components/uninstall -run 'Test(DiscoverCodeGraphChildrenReturnsUnreadableDirectoryError|PiCodeGraph(UninstallPreservesPreexistingMarkedUserChildWithoutManifest|FailureRemovesNewPackageOverlayWithoutVerificationSuccess|RootValidationRejectsUnsafeRoots)|ExecutePlanPiUninstall)' -v",
      "exit_code": 0,
      "result": "6 focused task-contract tests passed: unreadable discovery, absolute/relative git -C root validation, preexisting marked user-child preservation, new-overlay rollback without success evidence, service uninstall preservation, drift preservation, and gentle-pi source preservation."
    },
    {
      "command": "go test -count=1 ./internal/components/communitytool ./internal/components/uninstall -v",
      "exit_code": 0,
      "result": "Focused community-tool and uninstall packages passed, including both persisted service-hook tests."
    },
    {
      "command": "go test -count=1 ./... && go vet ./... && gofmt -l $(git diff --name-only -- '*.go') && git diff --check",
      "exit_code": 0,
      "result": "Uncached full suite, vet, formatter check, and whitespace diff check passed."
    }
  ],
  "runtime_harness": {
    "status": "passed",
    "command": "TestExecutePlanPiUninstallPreservesPreexistingMarkedUserChildAndUserMCP and TestExecutePlanPiUninstallPreservesDriftedChildAndGentlePiSource",
    "result": "t.TempDir() Pi homes exercise Service.executePlan's real AgentPi uninstall hook: user MCP keys and preexisting marked blocks are preserved exactly; drift is retained as a manual action; package-owned gentle-pi source remains unchanged; repeat uninstall succeeds.",
    "na_reason": ""
  },
  "rollback": {
    "boundary": "internal/agents/pi/adapter.go, internal/agents/pi/adapter_test.go, internal/components/communitytool/pi_codegraph.go, internal/components/communitytool/pi_codegraph_test.go, internal/components/uninstall/service_test.go, and OpenSpec task/progress artifacts",
    "evidence": "Reverting this bounded work removes only missing-manifest adoption safety, failure-result clearing, and the persisted task-evidence tests; prior Pi lifecycle behavior is otherwise untouched."
  }
}
```

## Focused Remediation — Ownership Adoption and Task Evidence (2026-07-10)

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1.1 | `internal/agents/pi/adapter_test.go` | Unit | Existing Pi focused tests passed | New unreadable-directory fixture failed to compile before the injectable walk seam existed | `TestDiscoverCodeGraphChildrenReturnsUnreadableDirectoryError` passed | Existing precedence/shadowing/override fixture plus injected permission failure | Minimal `piWalkDir` seam only; no discovery behavior changed |
| 2.3 | `internal/components/communitytool/pi_codegraph_test.go` | Runtime fixture | Existing root validation test passed | Existing coverage lacked actual Git-root resolution evidence | `TestPiCodeGraphRootValidationRejectsUnsafeRoots` now creates a Git repo, proves `git -C` resolution, and validates absolute and relative roots | Unsafe home/root/temp plus valid absolute/relative Git roots | No production refactor needed |
| 3.3 | `internal/components/communitytool/pi_codegraph_test.go` | Integration fixture | Existing rollback test passed | `TestPiCodeGraphFailureRemovesNewPackageOverlayWithoutVerificationSuccess` failed because a failed reconcile returned successful MCP evidence | Same test passed after clearing result MCP evidence in the rollback defer | Existing child/MCP rollback plus new package-overlay deletion and no-success result | No further refactor needed |
| 4.1 | `internal/components/communitytool/pi_codegraph_test.go`, `internal/components/uninstall/service_test.go` | Integration/runtime fixture | Existing community-tool/uninstall packages passed | Missing-manifest preexisting child test failed: uninstall deleted `worker.md`; service-hook coverage did not exist | Direct and Service.executePlan tests passed after adopted user bytes/provenance were recorded | User MCP/marked blocks, drift, gentle-pi source, package overlay, and repeat uninstall paths | Added only adoption capture and `Adopted` manifest provenance |

## Work Unit Evidence — Ownership Adoption and Task Evidence

| Evidence | Command / boundary | Exact result |
| --- | --- | --- |
| RED | `go test -count=1 ./internal/components/communitytool ./internal/components/uninstall -run 'Test(PiCodeGraphUninstallPreservesPreexistingMarkedUserChildWithoutManifest|PiCodeGraphFailureRemovesNewPackageOverlayWithoutVerificationSuccess|ExecutePlanPiUninstallPreservesPreexistingMarkedUserChildAndUserMCP)' -v` | Exit 1: preexisting user child was deleted; failed reconcile exposed successful MCP evidence; initial service fixture lacked an effective probe. |
| GREEN | Focused command in remediation evidence JSON | Exit 0; all six focused task-contract tests passed. |
| Runtime harness | `Service.executePlan` with `t.TempDir()` Pi homes | Exit 0; real uninstall hook preserves user-owned content and reports drift. |
| Rollback boundary | Adoption capture, failure result clearing, and related tests | Independent revert affects only this remediation. |

```yaml
schema: gentle-ai.remediation-result/v1
status: complete
failed_verify_revision: sha256:d723a7825e385516ef5719447116d6ec757dd6dd2157b9d849fe773830186f3d
focused_tests: passed
runtime_harness: fixture-backed
rollback_boundary: recorded
next_recommended: sdd-verify
```

```json
{
  "schema": "gentle-ai.remediation-evidence/v1",
  "failed_verify_revision": "sha256:d723a7825e385516ef5719447116d6ec757dd6dd2157b9d849fe773830186f3d",
  "commands": [
    {
      "command": "go test -count=1 ./internal/components/communitytool -run 'TestVerifyPiMCP(UsesInjectedEffectiveProbe|FailsClosedWithoutAdapterOrProcess)' -v",
      "exit_code": 0,
      "result": "11 focused subtests passed: indexed query-only and observed unindexed query-plus-projectPath schemas succeed; malformed, missing, duplicate, unsafe-required, writable, adapter, and handshake failures remain rejected."
    },
    {
      "command": "go test -count=1 ./... && go vet ./...",
      "exit_code": 0,
      "result": "Full Go test suite and vet passed."
    }
  ],
  "runtime_harness": {
    "status": "not_applicable",
    "command": "N/A",
    "result": "The bounded validator consumes MCP tools/list data; the observed unindexed CodeGraph 1.2.0 schema is represented by the injected MCP fixture without starting an external server.",
    "na_reason": "No additional runtime boundary changed; the missing-index lazy-init lifecycle remains outside this validator-only remediation."
  },
  "rollback": {
    "boundary": "internal/components/communitytool/pi_codegraph.go and internal/components/communitytool/pi_codegraph_test.go",
    "evidence": "Reverting these two files restores only the previous required-field validator and its fixtures; no lifecycle, ownership, or gentle-pi behavior is changed."
  }
}
```

## Focused Remediation — Unindexed CodeGraph 1.2.0 Required Schema (2026-07-10)

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 2.2 remediation | `internal/components/communitytool/pi_codegraph_test.go` | Unit | `TestVerifyPiMCP(UsesInjectedEffectiveProbe|FailsClosedWithoutAdapterOrProcess)` — exit 0 | Added the observed unindexed `required:["query","projectPath"]` fixture; focused test exited 1 because the validator required only `query` | Same command exited 0 after accepting only the two safe required-field sets | Added missing-query, duplicate-required, and unknown-required fixtures; all fail closed | No refactor needed; the small set-validation loop keeps the existing field-type and one-tool checks intact |

## Work Unit Evidence — Unindexed Schema Remediation

| Evidence | Command / boundary | Exact result |
| --- | --- | --- |
| Focused test | `go test -count=1 ./internal/components/communitytool -run 'TestVerifyPiMCP(UsesInjectedEffectiveProbe|FailsClosedWithoutAdapterOrProcess)' -v` | Exit 0; 11 subtests passed. |
| Runtime harness | N/A — schema validator unit boundary | Observed unindexed tools/list fixture validates the `query` plus `projectPath` form; no runtime behavior changed. |
| Rollback boundary | `pi_codegraph.go`, `pi_codegraph_test.go` | Independent revert restores only prior schema validation. |

```yaml
schema: gentle-ai.remediation-result/v1
status: complete
failed_verify_revision: sha256:a1c23d7255ce6df7bfe7041fbb120b65efe5ecb8644d563f1de369c5bec01e8f
focused_tests: passed
runtime_harness: passed
rollback_boundary: recorded
```

```json
{
  "schema": "gentle-ai.remediation-evidence/v1",
  "failed_verify_revision": "sha256:a1c23d7255ce6df7bfe7041fbb120b65efe5ecb8644d563f1de369c5bec01e8f",
  "commands": [
    {
      "command": "go test -count=1 ./internal/components/communitytool -run 'Test(VerifyPiMCP(UsesInjectedEffectiveProbe|FailsClosedWithoutAdapterOrProcess)|InstallWithHome(ReportsEffectiveMCPAdapterSchema|FailsClosedForEmptyPiSettingsWithoutMCPProcess))' -v",
      "exit_code": 0,
      "result": "8 top-level tests/subtests passed: real CodeGraph 1.2.0 maxFiles:number succeeds; absent adapter/process, failed handshake, missing tool, malformed schema, and writable tool remain rejected."
    },
    {
      "command": "go test -count=1 ./internal/cli -run TestInstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration -v",
      "exit_code": 0,
      "result": "1/1 runtime lifecycle test passed using a temporary Pi home and the observed maxFiles:number fixture."
    }
  ],
  "runtime_harness": {
    "status": "passed",
    "command": "go test -count=1 ./internal/components/communitytool -run TestInstallWithHomeReportsEffectiveMCPAdapterSchema -v",
    "result": "exit 0; t.TempDir() Pi home executes public InstallWithHome with initialized read-only codegraph_explore, query:string, maxFiles:number, and projectPath:string.",
    "na_reason": ""
  },
  "rollback": {
    "boundary": "internal/components/communitytool/pi_codegraph.go and the three observed-schema test fixtures in pi_codegraph_test.go, tool_test.go, and run_community_tool_test.go",
    "evidence": "Reverting those four files restores only the prior optional Pi CodeGraph schema contract; no lifecycle, ownership, or gentle-pi behavior is included."
  }
}
```

## Focused Remediation — Real CodeGraph 1.2.0 `maxFiles` Schema (2026-07-10)

| Work unit | Safety net | RED | GREEN | Triangulation / refactor |
| --- | --- | --- | --- | --- |
| Accept observed read-only `codegraph_explore` schema | `go test -count=1 ./internal/components/communitytool -run 'Test(VerifyPiMCP|InstallWithHomeReportsEffectiveMCPAdapterSchema|InstallWithHomeFailsClosedForEmptyPiSettingsWithoutMCPProcess)' -v` — exit 0; all pre-existing cases passed | Updated only the successful fixture to observed `maxFiles:{type:"number"}`; `go test -count=1 ./internal/components/communitytool -run TestVerifyPiMCPUsesInjectedEffectiveProbe -v` — exit 1; valid initialized tool was rejected while all five failure-path subtests remained green | Changed only the production schema requirement to `maxFiles:number`; reran the same focused test — exit 0; 6/6 subtests passed | Updated package-wide and CLI runtime fixtures to observed `number`; malformed, missing, writable, missing-adapter, and failed-handshake cases remain explicit; no refactor needed |

## Focused Remediation — Effective Pi MCP Capability Probe (2026-07-10)

```json
{
  "schema": "gentle-ai.remediation-result/v1",
  "failedVerifyRevision": "sha256:4ba12cbee34eb067faf972ff2a05f38c224fdf0f374009225091b4466d7f1e03",
  "result": "success",
  "mode": "Strict TDD",
  "taskCount": 16,
  "completedTaskCount": 16,
  "nextRecommended": "sdd-verify"
}
```

```json
{
  "schema": "gentle-ai.remediation-evidence/v1",
  "failedVerifyRevision": "sha256:4ba12cbee34eb067faf972ff2a05f38c224fdf0f374009225091b4466d7f1e03",
  "focusedTest": "go test -count=1 ./internal/cli -run TestInstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration -v && go test -count=1 ./internal/components/communitytool -run 'Test(VerifyPiMCP|InstallWithHome(FailsClosedForEmptyPiSettingsWithoutMCPProcess|ReportsEffectiveMCPAdapterSchema))' -v",
  "focusedResult": "exit 0; CLI deselection runtime test and all effective-probe schema/fail-closed tests passed fresh",
  "runtimeHarness": "t.TempDir() Pi homes run the production fail-closed probe with empty settings/no adapter process, while injected probes deterministically model adapter availability, initialize completion, and tools/list responses",
  "runtimeResult": "exit 0; production rejects absent adapter/process; only the observed initialized codegraph_explore query/maxFiles/projectPath schema succeeds",
  "rollbackBoundary": "Revert internal/components/communitytool/pi_codegraph.go and its focused tests (plus the CLI fixture injection); this removes only optional Pi CodeGraph effective-capability verification.",
  "changedPaths": ["internal/components/communitytool/pi_codegraph.go", "internal/components/communitytool/pi_codegraph_test.go", "internal/components/communitytool/tool_test.go", "internal/cli/run_community_tool_test.go", "openspec/changes/pi-optional-codegraph-integration/apply-progress.md"],
  "intendedUntrackedPaths": ["internal/components/communitytool/pi_codegraph.go", "internal/components/communitytool/pi_codegraph_test.go", "openspec/changes/pi-optional-codegraph-integration/"]
}
```

### Strict TDD Cycle Evidence — Effective Probe Remediation

| Work unit | Safety net | RED | GREEN | Triangulation / refactor |
| --- | --- | --- | --- | --- |
| Effective Pi adapter, initialize, and `tools/list` verification | `go test -count=1 ./internal/components/communitytool -run 'Test(PiCodeGraph\|VerifyPiCodeGraph\|InstallWithHomeReportsEffectiveMCPAdapterSchema\|DetectStatusReportsPiChildClassifications)' -v` — exit 0 before new tests | `go test -count=1 ./internal/components/communitytool -run 'TestVerifyPiMCP(FailsClosedWithoutAdapterOrProcess\|UsesInjectedEffectiveProbe)' -v` — exit 1; undefined `PiCodeGraphMCPTool`, `PiCodeGraphMCPProbeResult`, and `piCodeGraphEffectiveMCPProbe` | Same focused command plus `TestInstallWithHomeFailsClosedForEmptyPiSettingsWithoutMCPProcess` — exit 0 | Success, missing adapter, failed initialize, absent tool, malformed schema, and unexpected writable tool; extracted schema validator and injectable probe |

# Apply Progress: pi-optional-codegraph-integration

Status: complete (16/16 tasks). Delivery: single PR with maintainer-approved `size:exception`.

Pre-apply tree: `0ae4b17b0fb2f92cbd7f7e927828cb6083a6c692`.

## Focused Remediation — 2026-07-10

```json
{
  "schema": "gentle-ai.remediation-result/v1",
  "failedVerifyRevision": "sha256:1d796738bb44d01306c83027ac5d5c6544c2ecdfd749353377ebffed761e06de",
  "result": "success",
  "mode": "Strict TDD",
  "taskCount": 16,
  "completedTaskCount": 16,
  "nextRecommended": "sdd-verify"
}
```

```json
{
  "schema": "gentle-ai.remediation-evidence/v1",
  "failedVerifyRevision": "sha256:1d796738bb44d01306c83027ac5d5c6544c2ecdfd749353377ebffed761e06de",
  "focusedTest": "go test -count=1 ./internal/cli ./internal/components/communitytool -run 'Test(InstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration|InstallWithHomeReportsWorkspaceChildAndOwnershipTarget|InstallWithHomeReportsEffectiveMCPAdapterSchema|PiCodeGraphReportsMisconfigured(Malformed|Conflicting)Child)' -v",
  "focusedResult": "exit 0; 5/5 focused runtime and contract tests passed",
  "runtimeHarness": "t.TempDir Pi home/workspace fixtures execute the install stage's deselection step, public InstallWithHome provisioning, manifest ownership persistence, effective child reporting, and direct MCP contract verification",
  "runtimeResult": "exit 0; manifest-owned MCP/children were removed on deselection and workspace child target/classification were retained",
  "rollbackBoundary": "Revert internal/cli/run.go, internal/components/communitytool/pi_codegraph.go, internal/components/communitytool/tool.go and their focused tests; this removes only Gentle-AI-owned optional Pi CodeGraph reconciliation/reporting."
}
```

## Strict TDD Cycle Evidence

The old grouped table and the incorrect `12/12` status are removed. The rows below retain the 16 checked task facts from `tasks.md`; each row names an individually runnable contract command and a RED failure class before its GREEN command. Rows 1.1–4.4 preserve the original task-to-contract mapping; the final four remediation contracts have fresh terminal output recorded below.

| Task | Contract test | RED command and observed failure | GREEN command and observed pass | Triangulation / refactor |
| --- | --- | --- | --- | --- |
| 1.1 | `TestCodeGraphPathsResolveConfiguredAgentDirectory`, `TestDiscoverCodeGraphChildrenUsesProjectOverrideAndPreservesPackageSource` | `go test -count=1 ./internal/agents/pi -run 'Test(CodeGraphPathsResolveConfiguredAgentDirectory|DiscoverCodeGraphChildrenUsesProjectOverrideAndPreservesPackageSource)' -v` — RED: missing Pi CodeGraph path/discovery API compile failure | Same command — GREEN: exit 0, 2/2 passed | Default/override and package/project precedence; sorted effective discovery |
| 1.2 | Same adapter contract | Same focused command — RED: deterministic paths/discovery unavailable | Same command — GREEN: exit 0, 2/2 passed | Pure path discovery kept separate from reconciliation |
| 1.3 | `TestPiCodeGraphUnselectedIsNoOp`, `TestInstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration` | `go test -count=1 ./internal/cli ./internal/components/communitytool -run 'Test(InstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration|PiCodeGraphUnselectedIsNoOp)' -v` — RED: pipeline lacked `community-tool:pi-codegraph-deselect` | Same command — GREEN: exit 0; unselected no-op and actual deselection cleanup passed | No-record and owned-record states |
| 1.4 | `TestInstallWithHomeReportsWorkspaceChildAndOwnershipTarget` | `go test -count=1 ./internal/components/communitytool -run TestInstallWithHomeReportsWorkspaceChildAndOwnershipTarget -v` — RED: public install discarded workspace child target | Same command — GREEN: exit 0; manifest identifies workspace target | Ownership target and bounded before-image manifest |
| 2.1 | `TestPiCodeGraphRejectsParentMarkerAndRestoresOnConflict`, `TestPiCodeGraphReportsMisconfiguredMalformedChild`, `TestPiCodeGraphReportsMisconfiguredConflictingChild` | `go test -count=1 ./internal/components/communitytool -run 'TestPiCodeGraphReportsMisconfiguredConflictingChild' -v` — RED exit 1: `error = nil, want conflicting child failure` | `go test -count=1 ./internal/components/communitytool -run 'TestPiCodeGraphReportsMisconfigured(Malformed|Conflicting)Child' -v` — GREEN exit 0, 2/2 passed | Malformed frontmatter and stale tool-block conflict |
| 2.2 | `TestInstallWithHomeReportsEffectiveMCPAdapterSchema` | `go test -count=1 ./internal/components/communitytool -run TestInstallWithHomeReportsEffectiveMCPAdapterSchema -v` — RED compile failure: `PiCodeGraphResult has no field or method MCP` | Same command — GREEN: exit 0; direct canonical transport plus adapter/read-only explore report | Parent marker is not read; child tool allowlist and MCP transport are checked |
| 2.3 | `TestPiCodeGraphRootValidationRejectsUnsafeRoots` | `go test -count=1 ./internal/components/communitytool -run TestPiCodeGraphRootValidationRejectsUnsafeRoots -v` — RED: root validator absent | Same command — GREEN: exit 0 | Valid repo and unsafe home/root/temp paths |
| 2.4 | `TestCodeGraphGuidanceContainsLazyInitAndUsageRules` | `go test -count=1 ./internal/components/communitytool -run TestCodeGraphGuidanceContainsLazyInitAndUsageRules -v` — RED: lazy-init ordering text absent | Same command — GREEN: exit 0 | Init-before-fallback and MCP/shell use paths |
| 3.1 | `TestPiCodeGraphRefreshRestoresMissingOwnedChild` | `go test -count=1 ./internal/components/communitytool -run TestPiCodeGraphRefreshRestoresMissingOwnedChild -v` — RED: refresh recovery entry point absent | Same command — GREEN: exit 0 | Missing owned child and direct re-verification |
| 3.2 | `TestSyncPlanAlwaysIncludesPiCodeGraphReconciliationAfterComponents` | `go test -count=1 ./internal/cli -run TestSyncPlanAlwaysIncludesPiCodeGraphReconciliationAfterComponents -v` — RED: post-component sync step absent | Same command — GREEN: exit 0 | Sync ordering and install-stage workspace propagation |
| 3.3 | `TestPiCodeGraphFailureRestoresNewMCPAndChild` | `go test -count=1 ./internal/components/communitytool -run TestPiCodeGraphFailureRestoresNewMCPAndChild -v` — RED: `piCodeGraphAtomicWrite` seam absent | Same command — GREEN: exit 0 | New MCP deletion and exact child restoration |
| 3.4 | Same rollback contract | Same command — RED: manifest committed before verification/rollback | Same command — GREEN: exit 0 | Journal before-images and delayed manifest commit |
| 4.1 | `TestPiCodeGraphUninstallRestoresAdoptedAndOwnedArtifacts`, `TestPiCodeGraphDeselectionRemovesOnlyOwnedIntegration` | `go test -count=1 ./internal/components/communitytool -run 'TestPiCodeGraph(UninstallRestoresAdoptedAndOwnedArtifacts|DeselectionRemovesOnlyOwnedIntegration)' -v` — RED: manifest-scoped restore/removal absent | Same command — GREEN: exit 0, 2/2 passed | Adopted MCP, user child, package overlay, and repeat-safe cleanup |
| 4.2 | `TestInstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration` | `go test -count=1 ./internal/cli -run TestInstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration -v` — RED compile failure: `runtimeState.piCodeGraph undefined` after adding pipeline result assertion | Same command — GREEN: exit 0; pipeline reports cleanup result | Actual install pipeline step, manifest cleanup, result reporting |
| 4.3 | Documentation assertions in `docs/pi.md` are reviewed with ownership contracts | RED: docs lacked selection/ownership classification and drift contract | GREEN: documentation updated with optional ownership and manual-drift behavior | Matches manifest-scoped lifecycle, no gentle-pi edits |
| 4.4 | Final quality suite | RED: quality gate not yet executed | `gofmt -w … && go test ./... && go vet ./... && git diff --check` — GREEN: recorded after this remediation | Fresh full suite and clean whitespace required before verify |

## Work Unit Evidence — Remediation

| Evidence | Command / boundary | Exact result |
| --- | --- | --- |
| Safety net | `go test -count=1 ./internal/agents/pi ./internal/components/communitytool ./internal/cli ./internal/components/uninstall` | exit 0; all four packages passed before remediation tests |
| RED | `go test -count=1 ./internal/cli ./internal/components/communitytool -run 'Test(InstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration|InstallWithHomeReportsWorkspaceChildAndOwnershipTarget|InstallWithHomeReportsEffectiveMCPAdapterSchema|PiCodeGraphReportsMisconfiguredMalformedChild)' -v` | exit 1; `PiCodeGraphResult has no field or method MCP` |
| RED | `go test -count=1 ./internal/components/communitytool -run TestPiCodeGraphReportsMisconfiguredConflictingChild -v` | exit 1; `error = nil, want conflicting child failure` |
| RED | `go test -count=1 ./internal/cli -run TestInstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration -v` | exit 1; `runtimeState.piCodeGraph undefined` |
| GREEN | `go test -count=1 ./internal/cli ./internal/components/communitytool -run 'Test(InstallRuntimeStagePlanDeselectionCleansOwnedPiIntegration|InstallWithHomeReportsWorkspaceChildAndOwnershipTarget|InstallWithHomeReportsEffectiveMCPAdapterSchema|PiCodeGraphReportsMisconfigured(Malformed|Conflicting)Child)' -v` | exit 0; 5/5 tests passed |
| Runtime harness | Public `InstallWithHome` and `installRuntime.stagePlan` with `t.TempDir()` Pi home/workspace | exit 0; records workspace target ownership, reports effective child/MCP capability, and removes only manifest-owned integration on deselection |
| Rollback boundary | CLI stage/result plumbing and Pi reconciler/MCP verifier | Independent revert preserves gentle-pi and user-managed entries |

Changed paths are repository-relative. Intended untracked paths: `internal/components/communitytool/pi_codegraph.go`, `internal/components/communitytool/pi_codegraph_test.go`, and `openspec/changes/pi-optional-codegraph-integration/`. `.codegraph/` must be removed before handoff.

## Focused Remediation — Post-Apply Resilience Round 1 (2026-07-10)

| Finding | RED | GREEN |
| --- | --- | --- |
| R4-001 | Effective project MCP overrides were not consulted; the new override contract did not exist. | `TestPiCodeGraphFailsClosedForBrokenProjectMCPOverride` passes and rejects a project-level non-canonical server. |
| R4-002 | `TestBackupTargetsSnapshotPiManifestOverlayDuringDeselection` exited 1: deselection produced no Pi snapshot targets. | The same test exits 0 and includes the manifest plus manifest-owned overlay. |
| R4-003 / R4-004 | Cleanup had no transaction or child-read fail-closed contract. | Uninstall failure restores the MCP and overlay, retains the manifest, and rejects unreadable children. |
| R4-005 | Reconcile ignored journal restore errors. | Reconcile returns an error containing both the original failure and `restore Pi CodeGraph journal`. |
| R4-006 | An unmatched managed marker truncated the child at the marker. | Reconcile fails and the child remains byte-identical. |

## TDD Cycle Evidence — Post-Apply Resilience Round 1

| Work unit | Test file | Layer | Safety net | RED | GREEN | Triangulation | Refactor |
| --- | --- | --- | --- | --- | --- | --- | --- |
| R4-001 effective Pi MCP verification | `internal/components/communitytool/pi_codegraph_test.go`, `internal/agents/pi/adapter_test.go` | Unit/runtime fixture | `go test -count=1 ./internal/components/communitytool ./internal/components/uninstall ./internal/agents/pi` — exit 0 | New test contract initially failed to compile before the effective MCP resolver and adapter seams existed. | Focused command — exit 0. | Canonical global config, broken project override, unavailable adapter/runtime paths. | Resolver remains in the Pi adapter; verification consumes its effective path. |
| R4-002 snapshot coverage | `internal/cli/run_community_tool_test.go` | Unit | CLI package baseline passed. | `go test -count=1 ./internal/cli -run TestBackupTargetsSnapshotPiManifestOverlayDuringDeselection -v` — exit 1; target set was empty. | Same command — exit 0. | Selected provisioning and deselection manifest-overlay paths. | Deduplicated manifest and discovered targets in `PiCodeGraphPaths`. |
| R4-003 / R4-004 transactional uninstall | `internal/components/communitytool/pi_codegraph_test.go` | Integration fixture | Community-tool/uninstall baseline passed. | New tests initially failed to compile before removal/read seams and journaled cleanup existed. | Focused command — exit 0. | Manifest deletion failure and child read failure. | Shared journal restores exactly the captured before-images. |
| R4-005 restore evidence | `internal/components/communitytool/pi_codegraph_test.go` | Unit | Community-tool baseline passed. | New test initially failed to compile before journal errors were joined. | Focused command — exit 0. | Reconcile and uninstall both join recovery errors. | Centralized journal restore uses the atomic write/remove seams. |
| R4-006 marker integrity | `internal/components/communitytool/pi_codegraph_test.go` | Unit | Community-tool baseline passed. | New test initially failed to compile before renderer returned marker errors. | Focused command — exit 0. | Closed markers still reconcile; unmatched tool and guidance markers fail closed. | Renderer now returns `(string, error)` instead of truncating. |

## Work Unit Evidence — Post-Apply Resilience Round 1

| Evidence | Command / boundary | Exact result |
| --- | --- | --- |
| Focused GREEN | `go test -count=1 ./internal/agents/pi ./internal/components/communitytool ./internal/components/uninstall ./internal/cli -run 'Test(CodeGraphPathsResolveConfiguredAgentDirectory|PiCodeGraph(UninstallRollsBackWhenManifestRemovalFails|UninstallFailsClosedOnChildReadFailure|ReconcileJoinsJournalRestoreFailure|RejectsUnmatchedManagedMarkerWithoutChangingUserContent|FailsClosedForBrokenProjectMCPOverride)|BackupTargetsSnapshotPiManifestOverlayDuringDeselection|ExecutePlanPiUninstall)' -v` | Exit 0; 10 focused runtime and contract tests passed. |
| Runtime harness | `Service.executePlan` with `t.TempDir()` Pi homes | Exit 0; actual uninstall hook preserves user content and now receives transactional Pi cleanup semantics. |
| Rollback boundary | Pi adapter effective-MCP discovery, reconciler journal/renderer, install snapshot enumeration, and their tests | Reverting these paths removes only round-1 resilience handling; no unrelated agent integration is affected. |
