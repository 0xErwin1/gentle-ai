# RDD Single Lifecycle Specification

## Purpose

Define the exit gate for removing `GENTLE_AI_RDD_NEW_LINEAGE` so exactly one
review lifecycle (v3) remains. Grounded at post-W6 chain tip `40176a8f`
(pending Wave 6 verify); re-validate line references at task time.

## Requirements

### Requirement: Byte-Equivalence Exit Evidence Precedes Switch Removal

Before the switch and its legacy start branch are deleted, the wave MUST
prove a `GENTLE_AI_RDD_NEW_LINEAGE=1` build and a switch-free build produce
byte-identical goldens, envelopes, and receipts, via same-fixture on/off
double-evaluation across the full journey set. A golden diff during this
proof MUST be treated as a defect signal, never a golden-update task.

#### Scenario: Double-evaluation proves equivalence before deletion

- GIVEN the same fixture run once with the switch on and once switch-free
- WHEN goldens, envelopes, and receipts are compared
- THEN they are byte-identical, and only then does removal proceed

### Requirement: Preconditions W-9, W-10, W-11 Close Before Removal

(Recommended default — Wave 5 verify #10186 cycle 3.) The switch-removal
slice MUST NOT start until W-9 (unknown `causal_disposition` on a severe
finding escalates on v3, matching v2), W-10 (v3 capture refuses a severe
finding missing `evidence_class`/`causal_disposition`, mirroring v2), and
W-11 (WARNING-severity candidate-causal findings stay non-blocking on v3,
matching v2) are all closed.

#### Scenario: Any open precondition blocks the slice

- GIVEN W-9, W-10, or W-11 is still open
- WHEN the switch-removal slice is proposed
- THEN it does not start; it starts only once all three are closed

### Requirement: Exactly One Lifecycle After Removal

After removal, no `GENTLE_AI_RDD_NEW_LINEAGE` reference, legacy start
branch, or legacy mutation path MUST remain reachable.

#### Scenario: Every start request takes the v3 path

- GIVEN the switch-removal slice has landed
- WHEN any `start` is requested, or the codebase is searched for the switch
- THEN it always proceeds through v3, and zero switch references remain
  outside historical/archived change specs
