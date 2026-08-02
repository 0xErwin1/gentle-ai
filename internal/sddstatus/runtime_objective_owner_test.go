package sddstatus

import (
	"reflect"
	"testing"
)

// This file is the Wave 4 S2 guard for design.md decision 9 (maintainer-
// confirmed 2026-08-02): RuntimeObjective / BeginAttemptRequest is the sole
// owner of work-unit scope (WorkUnit, EvidenceGoal, MaxAttempts,
// MaxChangedLines). Before this slice, CompactAcquireRequest independently
// redeclared the same four fields — exactly the "two request structs over
// one concept" shape that produced issue #2133/#2151 (change-level Complete
// contradicting a work-unit-scoped objective). This test proves the
// duplication cannot silently reappear: CompactAcquireRequest must be a thin
// projection (an embedded BeginAttemptRequest plus nothing else of scope
// substance), never a parallel struct.

// TestRuntimeObjectiveIsSoleWorkUnitScopeOwner fails if CompactAcquireRequest
// carries any field of its own other than the embedded BeginAttemptRequest —
// i.e. if work-unit scope (WorkUnit/EvidenceGoal/MaxAttempts/MaxChangedLines)
// is ever redeclared outside BeginAttemptRequest.
func TestRuntimeObjectiveIsSoleWorkUnitScopeOwner(t *testing.T) {
	acquireType := reflect.TypeOf(CompactAcquireRequest{})
	if acquireType.NumField() != 1 {
		t.Fatalf("CompactAcquireRequest has %d fields, want exactly 1 (an embedded BeginAttemptRequest) — a parallel work-unit-scope struct must not exist", acquireType.NumField())
	}
	field := acquireType.Field(0)
	if !field.Anonymous || field.Type != reflect.TypeOf(BeginAttemptRequest{}) {
		t.Fatalf("CompactAcquireRequest.Field(0) = %+v, want an embedded (anonymous) BeginAttemptRequest — BeginAttemptRequest is the single work-unit-scope owner per decision 9", field)
	}
}
