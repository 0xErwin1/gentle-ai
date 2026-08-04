# Delta for RDD Shadow Evaluation

## REMOVED Requirements

### Requirement: Advisory-Only, Never Blocking

(Reason: Proposal D1 — the runtime shadow observer and `ShadowRelation`
alias retire outright; `ShadowRelation` lives under
`internal/reviewtransaction` so no external Go importer is possible.
Task-time confirmation of zero in-module consumers is still required.)
(Migration: None.)

### Requirement: Disable Switch Is the Observer's Rollback Boundary

(Reason: the observer it gates no longer exists.)
(Migration: None.)

### Requirement: Zero Live-Lifecycle Behavior Change

(Reason: no shadow code path remains to prove neutral against a live
decision; v3 behavior is now governed by `rdd-single-lifecycle`.)
(Migration: None.)

### Requirement: No Persisted Divergence Artifact (Assumption, pending maintainer confirmation)

(Reason: no divergence-recording code remains.)
(Migration: None.)

### Requirement: Off by Default in Live Paths (Assumption, pending maintainer confirmation)

(Reason: the observer is deleted, not merely defaulted off.)
(Migration: None.)

### Requirement: Unexplained Divergence Blocks Wave 2 (Assumption, pending maintainer confirmation)

(Reason: Wave 2 has long since landed; this was Wave 1's own boundary.)
(Migration: None.)

## ADDED Requirements

### Requirement: Historical Differential-Matrix Evidence Retained

(Recommended default — Proposal D1.) `shadow-differential-matrix.golden`
MUST remain byte-preserved as historical exit evidence after the observer
is deleted. It MUST NOT be regenerated, extended, or treated as
live-code-backed after this wave.

#### Scenario: Golden survives observer deletion unchanged

- GIVEN the shadow observer and alias have been deleted
- WHEN the repository is inspected
- THEN the golden still exists, byte-identical, and no code regenerates it
