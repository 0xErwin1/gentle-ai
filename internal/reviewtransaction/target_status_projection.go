package reviewtransaction

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// targetStatusAuthorityView is the immutable authority side of one status
// request. Compact records, graph edges, receipts, and finalize journals are
// loaded once before any candidate is projected against the live snapshot.
type targetStatusAuthorityView struct {
	compact map[string]targetStatusCandidate
	legacy  map[string]targetStatusCandidate
}

func loadTargetStatusAuthorityView(ctx context.Context, repo string, request TargetStatusRequest) (targetStatusAuthorityView, error) {
	compact, err := loadCompactTargetStatusCandidates(ctx, repo, request.LineageID)
	if err != nil {
		return targetStatusAuthorityView{}, fmt.Errorf("load compact target status authority: %w", err)
	}
	legacy, err := loadLegacyTargetStatusCandidates(ctx, repo, request.LineageID)
	if err != nil {
		return targetStatusAuthorityView{}, fmt.Errorf("load legacy target status authority: %w", err)
	}
	for lineage := range compact {
		if _, mixed := legacy[lineage]; mixed {
			return targetStatusAuthorityView{}, fmt.Errorf("lineage %q has mixed compact and legacy authority", lineage)
		}
	}
	return targetStatusAuthorityView{compact: compact, legacy: legacy}, nil
}

func loadCompactTargetStatusCandidates(ctx context.Context, repo, lineageID string) (map[string]targetStatusCandidate, error) {
	stores, err := DiscoverCompactStores(ctx, repo)
	if err != nil {
		return nil, err
	}
	storeByLineage := make(map[string]CompactStore, len(stores))
	for _, store := range stores {
		storeByLineage[store.lineageID] = store
	}

	records := make(map[string]CompactRecord, len(stores))
	selected := []CompactStore{}
	if lineageID == "" {
		for _, store := range stores {
			record, loadErr := store.Load()
			if loadErr != nil {
				return nil, loadErr
			}
			records[record.State.LineageID] = record
		}
		selected, err = compactAuthorityLeaves(records, storeByLineage)
		if err != nil {
			return nil, err
		}
	} else if store, ok := storeByLineage[lineageID]; ok {
		// An explicit selector keeps unrelated inventory isolated, while still
		// validating every recovery edge in the selected lineage's ancestry.
		cursor := store
		selected = append(selected, store)
		for {
			if _, seen := records[cursor.lineageID]; seen {
				return nil, errors.New("invalid compact authority graph: recovery cycle")
			}
			record, loadErr := cursor.Load()
			if loadErr != nil {
				return nil, loadErr
			}
			records[record.State.LineageID] = record
			if record.State.Recovery == nil {
				break
			}
			predecessor, exists := storeByLineage[record.State.Recovery.PredecessorLineageID]
			if !exists {
				return nil, fmt.Errorf("invalid compact authority graph: dangling predecessor for %q", record.State.LineageID)
			}
			cursor = predecessor
		}
		chainStores := make(map[string]CompactStore, len(records))
		for lineage := range records {
			chainStores[lineage] = storeByLineage[lineage]
		}
		if _, graphErr := compactAuthorityLeaves(records, chainStores); graphErr != nil {
			return nil, graphErr
		}
	}

	candidates := make(map[string]targetStatusCandidate, len(selected))
	for _, store := range selected {
		record := records[store.lineageID]
		identity, published, replayable, receiptErr := inspectCompactTargetReceipt(store, record.State)
		if receiptErr != nil {
			return nil, receiptErr
		}
		pending, pendingErr := store.PendingFinalizeAttemptReadOnly()
		if pendingErr != nil {
			return nil, pendingErr
		}
		copy := record
		candidates[record.State.LineageID] = targetStatusCandidate{
			version: AuthorityVersionCompact, lineage: record.State.LineageID, compact: &copy,
			receiptIdentity: identity, receiptPublished: published, receiptReplayable: replayable,
			pendingFinalize: pending != nil,
		}
	}
	return candidates, nil
}

func loadLegacyTargetStatusCandidates(ctx context.Context, repo, lineageID string) (map[string]targetStatusCandidate, error) {
	stores, err := DiscoverAuthoritativeStores(ctx, repo)
	if err != nil {
		return nil, err
	}
	candidates := make(map[string]targetStatusCandidate, len(stores))
	for _, store := range stores {
		if lineageID != "" && store.lineageID != lineageID {
			continue
		}
		chain, loadErr := store.LoadChain()
		if loadErr != nil {
			return nil, loadErr
		}
		transaction := chain.Records[len(chain.Records)-1].Transaction
		identity := ""
		if transaction.State == StateApproved {
			identity, err = inspectLegacyTargetReceipt(store, transaction)
			if err != nil {
				return nil, err
			}
		}
		copy := chain
		candidates[transaction.LineageID] = targetStatusCandidate{
			version: AuthorityVersionLegacy, lineage: transaction.LineageID, legacy: &copy,
			receiptIdentity: identity, receiptPublished: identity != "",
		}
	}
	return candidates, nil
}

type compactTerminalHistoryProjection uint8

const (
	compactTerminalHistoryUnrelated compactTerminalHistoryProjection = iota
	compactTerminalHistoryScopeChanged
)

// projectCompactTerminalHistory compares receipt-validated historical
// authority with the one request-scoped live snapshot. Frozen intended paths
// remain historical proof; they are never replayed against current tracking or
// filesystem membership.
func projectCompactTerminalHistory(state CompactState, live Snapshot) compactTerminalHistoryProjection {
	if live.BaseTree == state.CurrentSnapshot.CandidateTree {
		// The reviewed bytes and modes are now the immutable HEAD base. A clean
		// target or a disjoint next slice is not an applicability claim on the
		// historical receipt.
		if len(live.Paths) == 0 || classifyCompactPathSetRelation(state.GenesisPaths, live.Paths) == compactPathsDisjoint {
			return compactTerminalHistoryUnrelated
		}
		return compactTerminalHistoryScopeChanged
	}

	relation := classifyCompactTargetRelation(state.CurrentSnapshot, live, state.GenesisPaths, compactTargetRelationEvidence{})
	if relation.Kind != compactTargetUnsafe {
		return compactTerminalHistoryScopeChanged
	}
	// A projection, kind, or base mismatch can make the aggregate relation
	// unsafe even when live work still contracts or overlaps immutable genesis
	// scope. That is related evolution and must not be claimed as unrelated.
	if len(live.Paths) > 0 && relation.Paths != compactPathsDisjoint && relation.Paths != compactPathsInvalid {
		return compactTerminalHistoryScopeChanged
	}
	return compactTerminalHistoryUnrelated
}

func compactLiveTargetMatchesValidatedSnapshot(state CompactState, live Snapshot, requireCurrentCandidate bool) bool {
	initial := state.InitialSnapshot
	proof := initial.IntendedUntrackedProof
	if requireCurrentCandidate {
		proof = state.CurrentSnapshot.IntendedUntrackedProof
	}
	return initial.Projection == live.Projection && compactStartTargetKindsCompatible(initial.Kind, live.Kind) &&
		initial.BaseTree == live.BaseTree && (!requireCurrentCandidate || state.CurrentSnapshot.CandidateTree == live.CandidateTree) &&
		pathsAreSubset(live.Paths, state.GenesisPaths) == nil && equalStrings(initial.IntendedUntracked, live.IntendedUntracked) &&
		proof == live.IntendedUntrackedProof && len(live.LedgerIDs) == 0
}

func legacyLiveTargetMatchesValidatedSnapshot(transaction Transaction, live Snapshot) bool {
	genesis := transaction.GenesisPaths
	if len(genesis) == 0 {
		genesis = transaction.Snapshot.Paths
	}
	kindsMatch := compactStartTargetKindsCompatible(transaction.Snapshot.Kind, live.Kind) ||
		transaction.Snapshot.Kind == TargetFixDiff && (live.Kind == TargetCurrentChanges || live.Kind == TargetBaseDiff)
	return transaction.Snapshot.Projection == live.Projection && kindsMatch && transaction.BaseTree == live.BaseTree &&
		transaction.FinalCandidateTree == live.CandidateTree && pathsAreSubset(live.Paths, genesis) == nil &&
		equalStrings(transaction.Snapshot.IntendedUntracked, live.IntendedUntracked) &&
		transaction.Snapshot.IntendedUntrackedProof == live.IntendedUntrackedProof && len(live.LedgerIDs) == 0
}

func classifyCompactCorrectionTargetForStatus(ctx context.Context, repo string, existing CompactState, live Snapshot) (compactCorrectionTargetClaim, error) {
	if existing.State != StateCorrectionRequired || existing.InitialSnapshot.Projection != live.Projection ||
		!compactStartTargetKindsCompatible(existing.InitialSnapshot.Kind, live.Kind) ||
		existing.InitialSnapshot.BaseTree != live.BaseTree || len(live.LedgerIDs) != 0 {
		return compactCorrectionTargetUnclaimed, nil
	}
	if compactHistoricalFailedValidator(existing) {
		if compactEscalatedRecoveryTargetChanged(existing.CurrentSnapshot, live) {
			return compactCorrectionTargetRecover, nil
		}
		return compactCorrectionTargetBlocked, nil
	}
	if compactRecoveryAddsGenesisPath(existing, live) {
		return compactCorrectionTargetRecover, nil
	}
	if pathsAreSubset(live.Paths, existing.GenesisPaths) != nil {
		return compactCorrectionTargetUnclaimed, nil
	}
	if compactLiveTargetMatchesValidatedSnapshot(existing, live, false) {
		if live.CandidateTree == existing.CurrentSnapshot.CandidateTree {
			return compactCorrectionTargetResume, nil
		}
		requested := existing
		requested.InitialSnapshot = live
		matches, err := compactStatusCorrectionCandidateMatches(ctx, repo, existing, requested)
		if err != nil {
			return compactCorrectionTargetUnclaimed, err
		}
		if matches {
			return compactCorrectionTargetResume, nil
		}
	}
	if compactRecoveryContractsGenesisPaths(existing, live) {
		return compactCorrectionTargetRecover, nil
	}
	return compactCorrectionTargetBlocked, nil
}

func compactStatusCorrectionCandidateMatches(ctx context.Context, repo string, existing, requested CompactState) (bool, error) {
	if existing.ProposedCorrectionLines == nil {
		return false, nil
	}
	fix, err := (SnapshotBuilder{Repo: repo}).Build(ctx, Target{
		Kind: TargetFixDiff, Projection: existing.InitialSnapshot.Projection, BaseRef: existing.CurrentSnapshot.CandidateTree,
		IntendedUntracked: existing.InitialSnapshot.IntendedUntracked, LedgerIDs: existing.FixFindingIDs,
	})
	if err != nil {
		if targetStatusOperationalFailure(err) {
			return false, err
		}
		return false, nil
	}
	if fix.CandidateTree != requested.InitialSnapshot.CandidateTree || pathsAreSubset(fix.Paths, existing.GenesisPaths) != nil {
		return false, nil
	}
	lines, err := (SnapshotBuilder{Repo: repo}).ChangedLines(ctx, fix)
	if err != nil {
		if targetStatusOperationalFailure(err) {
			return false, err
		}
		return false, nil
	}
	return lines <= existing.CorrectionBudget-existing.CumulativeCorrectionLines, nil
}

func targetStatusFailure(base TargetStatusResult, err error) (TargetStatusResult, error) {
	if targetStatusOperationalFailure(err) {
		return TargetStatusResult{}, err
	}
	return corruptedTargetStatus(base), nil
}

func targetStatusOperationalFailure(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout *GitCommandTimeoutError
	var command *GitCommandError
	var processControl *GitProcessControlError
	if errors.As(err, &timeout) || errors.As(err, &command) || errors.As(err, &processControl) {
		return true
	}
	var pathErr *os.PathError
	return errors.As(err, &pathErr) && !errors.Is(err, os.ErrNotExist)
}
