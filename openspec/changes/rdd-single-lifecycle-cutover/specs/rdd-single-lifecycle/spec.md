# Delta for RDD Single Lifecycle

Moved here from `rdd-root-simplification-wave7`'s delta, byte-identical to
the requirement and scenario text Wave 7 carried (verify finding C1 /
blocker B1) — Wave 7 deferred switch removal and never delivered this
requirement, so it does not belong in Wave 7's own delta. See this change's
`proposal.md` for the full re-entry brief this requirement's delivery
depends on.

## ADDED Requirements

### Requirement: Exactly One Lifecycle After Removal

After removal, no `GENTLE_AI_RDD_NEW_LINEAGE` reference, legacy start
branch, or legacy mutation path MUST remain reachable.

#### Scenario: Every start request takes the v3 path

- GIVEN the switch-removal slice has landed
- WHEN any `start` is requested, or the codebase is searched for the switch
- THEN it always proceeds through v3, and zero switch references remain
  outside historical/archived change specs
