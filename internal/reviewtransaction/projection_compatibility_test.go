package reviewtransaction

import (
	"context"
	"testing"
)

func TestCompactTargetProjectionsCompatible(t *testing.T) {
	tests := []struct {
		name                                    string
		existingKind, requestedKind             TargetKind
		existingProjection, requestedProjection Projection
		want                                    bool
	}{
		{
			name:                "exact projection remains compatible",
			existingKind:        TargetCurrentChanges,
			requestedKind:       TargetCurrentChanges,
			existingProjection:  ProjectionStaged,
			requestedProjection: ProjectionStaged,
			want:                true,
		},
		{
			name:                "staged current changes accepts workspace base diff",
			existingKind:        TargetCurrentChanges,
			requestedKind:       TargetBaseDiff,
			existingProjection:  ProjectionStaged,
			requestedProjection: ProjectionWorkspace,
			want:                true,
		},
		{
			name:                "staged current changes accepts default base diff",
			existingKind:        TargetCurrentChanges,
			requestedKind:       TargetBaseDiff,
			existingProjection:  ProjectionStaged,
			requestedProjection: "",
			want:                true,
		},
		{
			name:                "workspace base diff accepts staged current changes",
			existingKind:        TargetBaseDiff,
			requestedKind:       TargetCurrentChanges,
			existingProjection:  ProjectionWorkspace,
			requestedProjection: ProjectionStaged,
			want:                true,
		},
		{
			name:                "staged overlay does not accept workspace base diff",
			existingKind:        TargetBaseWorkspaceOverlay,
			requestedKind:       TargetBaseDiff,
			existingProjection:  ProjectionStaged,
			requestedProjection: ProjectionWorkspace,
			want:                false,
		},
		{
			name:                "staged current changes does not accept workspace fix diff",
			existingKind:        TargetCurrentChanges,
			requestedKind:       TargetFixDiff,
			existingProjection:  ProjectionStaged,
			requestedProjection: ProjectionWorkspace,
			want:                false,
		},
		{
			name:                "staged current changes does not accept workspace current changes",
			existingKind:        TargetCurrentChanges,
			requestedKind:       TargetCurrentChanges,
			existingProjection:  ProjectionStaged,
			requestedProjection: ProjectionWorkspace,
			want:                false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactTargetProjectionsCompatible(tt.existingKind, tt.existingProjection, tt.requestedKind, tt.requestedProjection); got != tt.want {
				t.Fatalf("compactTargetProjectionsCompatible() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestProjectionCompatibilityGovernancePredicates(t *testing.T) {
	base := Snapshot{
		Kind:                   TargetCurrentChanges,
		Projection:             ProjectionStaged,
		BaseTree:               "base",
		CandidateTree:          "candidate",
		PathsDigest:            digestPaths([]string{"tracked.go"}),
		Paths:                  []string{"tracked.go"},
		IntendedUntracked:      []string{"draft.md"},
		IntendedUntrackedProof: "proof",
	}
	tests := []struct {
		name    string
		initial Snapshot
		live    Snapshot
		want    bool
	}{
		{
			name:    "exact projection remains governing",
			initial: base,
			live:    base,
			want:    true,
		},
		{
			name:    "staged current changes governs workspace base diff",
			initial: base,
			live: Snapshot{
				Kind: TargetBaseDiff, Projection: ProjectionWorkspace,
				BaseTree: base.BaseTree, CandidateTree: base.CandidateTree,
				PathsDigest: base.PathsDigest, Paths: append([]string(nil), base.Paths...),
				IntendedUntracked: append([]string(nil), base.IntendedUntracked...), IntendedUntrackedProof: base.IntendedUntrackedProof,
			},
			want: true,
		},
		{
			name: "staged overlay recovery does not govern workspace base diff",
			initial: Snapshot{
				Kind: TargetBaseWorkspaceOverlay, Projection: ProjectionStaged,
				BaseTree: base.BaseTree, CandidateTree: base.CandidateTree,
				PathsDigest: base.PathsDigest, Paths: append([]string(nil), base.Paths...),
				IntendedUntracked: append([]string(nil), base.IntendedUntracked...), IntendedUntrackedProof: base.IntendedUntrackedProof,
			},
			live: Snapshot{
				Kind: TargetBaseDiff, Projection: ProjectionWorkspace,
				BaseTree: base.BaseTree, CandidateTree: base.CandidateTree,
				PathsDigest: base.PathsDigest, Paths: append([]string(nil), base.Paths...),
				IntendedUntracked: append([]string(nil), base.IntendedUntracked...), IntendedUntrackedProof: base.IntendedUntrackedProof,
			},
			want: false,
		},
		{
			name:    "unrelated staged workspace kinds do not govern",
			initial: base,
			live: Snapshot{
				Kind: TargetFixDiff, Projection: ProjectionWorkspace,
				BaseTree: base.BaseTree, CandidateTree: base.CandidateTree,
				PathsDigest: base.PathsDigest, Paths: append([]string(nil), base.Paths...),
				IntendedUntracked: append([]string(nil), base.IntendedUntracked...), IntendedUntrackedProof: base.IntendedUntrackedProof,
			},
			want: false,
		},
		{
			name:    "intended untracked mismatch remains rejected",
			initial: base,
			live: Snapshot{
				Kind: TargetBaseDiff, Projection: ProjectionWorkspace,
				BaseTree: base.BaseTree, CandidateTree: base.CandidateTree,
				PathsDigest: base.PathsDigest, Paths: append([]string(nil), base.Paths...),
				IntendedUntracked: []string{"other.md"}, IntendedUntrackedProof: base.IntendedUntrackedProof,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := CompactState{
				InitialSnapshot: tt.initial,
				CurrentSnapshot: tt.initial,
				GenesisPaths:    append([]string(nil), tt.initial.Paths...),
			}
			requested := state
			requested.InitialSnapshot = tt.live
			transaction := Transaction{
				Snapshot:           tt.initial,
				GenesisPaths:       append([]string(nil), tt.initial.Paths...),
				BaseTree:           tt.initial.BaseTree,
				FinalCandidateTree: tt.initial.CandidateTree,
			}

			if got := compactStartDeliveryScopeMatches(state, requested); got != tt.want {
				t.Fatalf("compactStartDeliveryScopeMatches() = %t, want %t", got, tt.want)
			}
			if got := compactLiveTargetMatchesValidatedSnapshot(state, tt.live, true); got != tt.want {
				t.Fatalf("compactLiveTargetMatchesValidatedSnapshot() = %t, want %t", got, tt.want)
			}
			if got := legacyLiveTargetMatchesValidatedSnapshot(transaction, tt.live); got != tt.want {
				t.Fatalf("legacyLiveTargetMatchesValidatedSnapshot() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestApprovedStagedCurrentChangesReceiptGovernsWorkspaceBaseDiffAfterCommit(t *testing.T) {
	repo, state, _, _ := approvedCurrentChangesScopeRecoveryFixtureForProjection(t, "projection-compatible-status", ProjectionStaged)
	gitSnapshot(t, repo, "commit", "-m", "deliver staged candidate")

	target := Target{
		Kind: TargetBaseDiff, BaseRef: state.InitialSnapshot.BaseTree,
		IntendedUntracked: []string{},
	}
	requested := newCompactStartStateForTarget(t, repo, "projection-compatible-start", target)
	requested.PolicyHash = state.PolicyHash
	started, err := StartCompactAuthority(context.Background(), repo, CompactStartRequest{State: requested})
	if err != nil || started.Action != CompactStartReuseReceipt || started.Record.State.LineageID != state.LineageID {
		t.Fatalf("START after commit = %#v, %v", started, err)
	}

	status, err := AssessTargetStatus(context.Background(), repo, TargetStatusRequest{
		Target: target, LineageID: state.LineageID,
	})
	if err != nil || status.Applicability != TargetApplicabilityCurrent || status.State != StateApproved ||
		status.Action != TargetStatusActionValidate || status.ReceiptIdentity == "" || status.LineageID != state.LineageID {
		t.Fatalf("STATUS after commit = %#v, %v", status, err)
	}
}
