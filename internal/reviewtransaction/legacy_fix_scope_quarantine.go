package reviewtransaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	legacyFixScopeQuarantineAuthorizationSchema = "gentle-ai.review-legacy-fix-scope-quarantine-authorization/v1"
	LegacyFixScopeQuarantineDisposition         = "quarantine-historical-complete-fix-scope-expansion"
	legacyFixScopeQuarantineDiagnostic          = "historical complete-fix expanded correction paths beyond frozen genesis scope"
)

var legacyFixScopeBeforeMaintenance = func() {}

// LegacyFixScopeQuarantineRequest identifies one exact historical legacy-v1
// complete-fix scope-expansion lineage. It is a quarantine, never a repair.
type LegacyFixScopeQuarantineRequest struct {
	LineageID               string
	ExpectedRevision        string
	ExpectedDiagnostic      string
	Disposition             string
	Reason                  string
	Actor                   string
	MaintainerAuthorization string
	QuarantinedAt           time.Time
}

// LegacyFixScopeQuarantineProof records the exact immutable event and the
// non-empty path expansion that native replay re-derived from it.
type LegacyFixScopeQuarantineProof struct {
	LineageID               string   `json:"lineage_id"`
	HeadRevision            string   `json:"head_revision"`
	ChainIdentity           string   `json:"chain_identity"`
	EventRevision           string   `json:"event_revision"`
	PreviousRevision        string   `json:"previous_revision"`
	Operation               string   `json:"operation"`
	FrozenGenesisPaths      []string `json:"frozen_genesis_paths"`
	ExpandedCorrectionPaths []string `json:"expanded_correction_paths"`
	FixDeltaHash            string   `json:"fix_delta_hash"`
	Diagnostic              string   `json:"diagnostic"`
	Disposition             string   `json:"disposition"`
}

func legacyFixScopeQuarantineAuthorizationBinding(repository, lineage, revision, diagnostic, disposition, actor, reason string) string {
	return legacyFixScopeQuarantineAuthorizationSchema + "\nrepository=" + repository +
		"\nlineage=" + lineage + "\nrevision=" + revision +
		"\ndiagnostic=" + diagnostic + "\ndisposition=" + disposition +
		"\nactor=" + strings.TrimSpace(actor) + "\nreason=" + strings.TrimSpace(reason)
}

// QuarantineHistoricalLegacyFixScope moves one immutable legacy-v1 lineage
// whose only replay failure is the approved complete-fix scope expansion. It
// never makes the history valid, broadens alias repair, or rewrites records.
func QuarantineHistoricalLegacyFixScope(ctx context.Context, repo string, request LegacyFixScopeQuarantineRequest) (CompactReclaimRecord, error) {
	if err := ctx.Err(); err != nil {
		return CompactReclaimRecord{}, err
	}
	if err := validateLineageID(request.LineageID); err != nil {
		return CompactReclaimRecord{}, err
	}
	if !validSHA256(request.ExpectedRevision) {
		return CompactReclaimRecord{}, errors.New("review quarantine-legacy-fix-scope requires an exact current HEAD revision")
	}
	if strings.TrimSpace(request.Reason) == "" || strings.TrimSpace(request.Actor) == "" {
		return CompactReclaimRecord{}, errors.New("review quarantine-legacy-fix-scope requires a non-empty reason and actor")
	}
	if strings.ContainsAny(request.Reason, "\r\n") || strings.ContainsAny(request.Actor, "\r\n") {
		return CompactReclaimRecord{}, errors.New("review quarantine-legacy-fix-scope requires LF-only single-line actor and reason authorization fields")
	}
	if request.ExpectedDiagnostic != legacyFixScopeQuarantineDiagnostic || request.Disposition != LegacyFixScopeQuarantineDisposition {
		return CompactReclaimRecord{}, errors.New("review quarantine-legacy-fix-scope supports only the historical complete-fix scope-expansion diagnostic and disposition")
	}
	base, repository, err := reviewAuthorityRoot(ctx, repo)
	if err != nil {
		return CompactReclaimRecord{}, err
	}
	dir, exists, err := canonicalLegacyFixScopeDir(base, request.LineageID)
	if err != nil {
		return CompactReclaimRecord{}, err
	}
	if !exists {
		return replayCommittedLegacyFixScopeQuarantine(base, repository, request)
	}
	legacyFixScopeBeforeMaintenance()

	maintenance, err := acquireMaintenanceLock(ctx, compactMaintenanceLockPath(base), maintenanceExclusive)
	if err != nil {
		return CompactReclaimRecord{}, err
	}
	defer maintenance.Release()
	// Recheck under exclusive maintenance before creating a local lock file:
	// another invocation may have quarantined the source after the first check.
	dir, exists, err = canonicalLegacyFixScopeDir(base, request.LineageID)
	if err != nil {
		return CompactReclaimRecord{}, err
	}
	if !exists {
		return replayCommittedLegacyFixScopeQuarantine(base, repository, request)
	}
	// The local handle must close before renaming dir on Windows. The exclusive
	// maintenance lock remains held above dir throughout the atomic move.
	localLock, err := acquireLocalStoreLock(filepath.Join(dir, "LOCK"))
	if err != nil {
		return CompactReclaimRecord{}, fmt.Errorf("review quarantine-legacy-fix-scope refused active ownership: %w", err)
	}
	localLockReleased := false
	releaseLocalLock := func() error {
		if localLockReleased {
			return nil
		}
		localLockReleased = true
		return localLock.release()
	}
	defer releaseLocalLock()
	if _, err := os.Stat(filepath.Join(base, "v2", request.LineageID)); err == nil {
		return CompactReclaimRecord{}, fmt.Errorf("review quarantine-legacy-fix-scope refused: lineage %q also exists in compact-v2 authority", request.LineageID)
	} else if !os.IsNotExist(err) {
		return CompactReclaimRecord{}, fmt.Errorf("inspect same-lineage compact authority: %w", err)
	}

	store := Store{Dir: dir, lineageID: request.LineageID, repo: repository, readOnly: true}
	proof, err := inspectHistoricalLegacyFixScope(store, request.ExpectedRevision)
	if err != nil {
		return CompactReclaimRecord{}, err
	}
	proof.Disposition = request.Disposition
	if request.MaintainerAuthorization != legacyFixScopeQuarantineAuthorizationBinding(
		repository, request.LineageID, request.ExpectedRevision, request.ExpectedDiagnostic,
		request.Disposition, request.Actor, request.Reason,
	) {
		return CompactReclaimRecord{}, fmt.Errorf("review quarantine-legacy-fix-scope requires an exact maintainer authorization binding (schema %s over repository, lineage, revision, diagnostic, disposition, actor, and reason)", legacyFixScopeQuarantineAuthorizationSchema)
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return CompactReclaimRecord{}, fmt.Errorf("inspect legacy fix-scope quarantine target: %w", err)
	}
	residue := make([]string, 0, len(items))
	for _, item := range items {
		residue = append(residue, item.Name())
	}
	sort.Strings(residue)
	if request.QuarantinedAt.IsZero() {
		request.QuarantinedAt = time.Now().UTC()
	}
	if err := releaseLocalLock(); err != nil {
		return CompactReclaimRecord{}, fmt.Errorf("release legacy lineage lock before fix-scope quarantine: %w", err)
	}
	record, err := quarantineCompactStoreEntry(base, dir, CompactReclaimRecord{
		Schema: CompactReclaimRecordSchema, Status: CompactReclaimPrepared,
		LineageID: request.LineageID, Reason: strings.TrimSpace(request.Reason), Actor: strings.TrimSpace(request.Actor),
		ReclaimedAt: request.QuarantinedAt.UTC(), SourcePath: dir, Residue: residue, LegacyFixScopeQuarantine: &proof,
	})
	if err != nil {
		return record, err
	}
	if err := verifyHistoricalLegacyFixScopeReadback(ctx, repo, request.LineageID, record); err != nil {
		return record, err
	}
	return record, nil
}

func canonicalLegacyFixScopeDir(base, lineage string) (string, bool, error) {
	versionRoot := filepath.Join(base, "v1")
	dir := filepath.Join(versionRoot, lineage)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return dir, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect legacy fix-scope quarantine target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, fmt.Errorf("review quarantine-legacy-fix-scope refused: lineage directory %q is symlinked or not a real directory", dir)
	}
	canonical, err := filepath.EvalSymlinks(dir)
	canonicalRoot, rootErr := filepath.EvalSymlinks(versionRoot)
	relative, relativeErr := filepath.Rel(canonicalRoot, canonical)
	if err != nil || rootErr != nil || relativeErr != nil || relative != lineage {
		return "", false, fmt.Errorf("review quarantine-legacy-fix-scope refused: lineage directory %q is not canonical", dir)
	}
	return canonical, true, nil
}

func inspectHistoricalLegacyFixScope(store Store, expectedHead string) (LegacyFixScopeQuarantineProof, error) {
	head, err := readRevision(filepath.Join(store.Dir, "HEAD"))
	if err != nil {
		return LegacyFixScopeQuarantineProof{}, err
	}
	if head != expectedHead {
		return LegacyFixScopeQuarantineProof{}, fmt.Errorf("%w: expected legacy HEAD %q, current %q", ErrConcurrentUpdate, expectedHead, head)
	}
	visited := map[string]bool{}
	reverseRecords := []Record{}
	reverseRevisions := []string{}
	for revision := head; revision != ""; {
		if visited[revision] {
			return LegacyFixScopeQuarantineProof{}, errors.New("review quarantine-legacy-fix-scope refused: predecessor cycle")
		}
		visited[revision] = true
		record, loaded, err := store.loadRevision(revision)
		if err != nil {
			return LegacyFixScopeQuarantineProof{}, fmt.Errorf("review quarantine-legacy-fix-scope refused: load revision %s: %w", revision, err)
		}
		reverseRecords = append(reverseRecords, record)
		reverseRevisions = append(reverseRevisions, loaded)
		revision = record.PreviousRevision
	}
	records := make([]Record, len(reverseRecords))
	revisions := make([]string, len(reverseRevisions))
	for index := range reverseRecords {
		reverse := len(reverseRecords) - 1 - index
		records[index], revisions[index] = reverseRecords[reverse], reverseRevisions[reverse]
	}
	if len(records) == 0 || !validInitialStoreRecord(records[0]) || records[0].Transaction.LineageID != store.lineageID {
		return LegacyFixScopeQuarantineProof{}, errors.New("review quarantine-legacy-fix-scope refused: chain genesis is invalid")
	}
	var proof *LegacyFixScopeQuarantineProof
	for index := 1; index < len(records); index++ {
		if records[index].PreviousRevision != revisions[index-1] {
			return LegacyFixScopeQuarantineProof{}, errors.New("review quarantine-legacy-fix-scope refused: predecessor revision is discontinuous")
		}
		err := validatePersistedV1Successor(records[index-1].Transaction, records[index].Transaction, records[index].Operation, index)
		if err == nil {
			continue
		}
		if proof != nil && validateHistoricalFixScopeSuccessor(records[index-1].Transaction, records[index].Transaction, records[index].Operation) == nil {
			continue
		}
		expanded, frozen, scopeErr := historicalCompleteFixScopeExpansion(records[index-1].Transaction, records[index].Transaction, records[index].Operation)
		if scopeErr != nil || proof != nil {
			return LegacyFixScopeQuarantineProof{}, fmt.Errorf("review quarantine-legacy-fix-scope refused unsupported successor anomaly at %s: %w", revisions[index], err)
		}
		proof = &LegacyFixScopeQuarantineProof{
			LineageID: store.lineageID, HeadRevision: head, ChainIdentity: chainIdentity(revisions),
			EventRevision: revisions[index], PreviousRevision: revisions[index-1], Operation: records[index].Operation,
			FrozenGenesisPaths: frozen, ExpandedCorrectionPaths: expanded, FixDeltaHash: records[index].Transaction.FixDeltaHash,
			Diagnostic: legacyFixScopeQuarantineDiagnostic,
		}
	}
	if proof == nil {
		return LegacyFixScopeQuarantineProof{}, errors.New("review quarantine-legacy-fix-scope refused: lineage has no supported historical complete-fix scope expansion")
	}
	return *proof, nil
}

func validateHistoricalFixScopeSuccessor(previous, next Transaction, operation string) error {
	if operation != "review/validate-fix" || previous.Mode != ModeOrdinary4R || previous.State != StateFixValidating ||
		(next.State != StateReadyFinalVerification && next.State != StateEscalated) || next.OriginalCriteria != nil || next.CorrectionRegression != nil {
		return errors.New("not a historical fix-scope successor")
	}
	return validateSuccessor(previous, next, "review/validate-fix-delta")
}

// historicalCompleteFixScopeExpansion validates every complete-fix invariant
// with a temporary derived union only to bypass the one historical scope check.
// It never changes the persisted transaction or accepts aliases or other modes.
func historicalCompleteFixScopeExpansion(previous, next Transaction, operation string) ([]string, []string, error) {
	if operation != "review/complete-fix" || previous.Mode != ModeOrdinary4R ||
		previous.State != StateFixing || next.State != StateFixValidating ||
		!equalStrings(previous.GenesisPaths, next.GenesisPaths) {
		return nil, nil, errors.New("not an ordinary_4r complete-fix scope expansion")
	}
	frozen := previous.GenesisPaths
	if frozen == nil {
		frozen = previous.Snapshot.Paths
	}
	canonicalFrozen, err := canonicalPaths(frozen)
	if err != nil || !equalStrings(canonicalFrozen, frozen) {
		return nil, nil, errors.New("frozen genesis paths are malformed")
	}
	canonicalNext, err := canonicalPaths(next.Snapshot.Paths)
	if err != nil || !equalStrings(canonicalNext, next.Snapshot.Paths) {
		return nil, nil, errors.New("correction paths are malformed")
	}
	allowed := make(map[string]struct{}, len(frozen))
	for _, path := range frozen {
		allowed[path] = struct{}{}
	}
	expanded := make([]string, 0)
	for _, path := range next.Snapshot.Paths {
		if _, ok := allowed[path]; !ok {
			expanded = append(expanded, path)
		}
	}
	if len(expanded) == 0 {
		return nil, nil, errors.New("correction does not expand frozen genesis scope")
	}
	derivedScope := append(append([]string{}, frozen...), expanded...)
	derivedScope, err = canonicalPaths(derivedScope)
	if err != nil {
		return nil, nil, err
	}
	replayPrevious, replayNext := previous, next
	replayPrevious.GenesisPaths = derivedScope
	replayNext.GenesisPaths = derivedScope
	if err := validateSuccessor(replayPrevious, replayNext, operation); err != nil {
		return nil, nil, err
	}
	return expanded, append([]string{}, frozen...), nil
}

func verifyHistoricalLegacyFixScopeReadback(ctx context.Context, repo, lineage string, record CompactReclaimRecord) error {
	if record.Status != CompactReclaimCommitted || record.LegacyFixScopeQuarantine == nil {
		return errors.New("review quarantine-legacy-fix-scope did not commit an auditable quarantine record")
	}
	report, err := InventoryAuthority(ctx, repo)
	if err != nil {
		return fmt.Errorf("read back authority inventory after fix-scope quarantine: %w", err)
	}
	for _, entry := range report.Entries {
		if entry.LineageID == lineage {
			return fmt.Errorf("read back authority inventory still contains quarantined lineage %q", lineage)
		}
	}
	return nil
}

func replayCommittedLegacyFixScopeQuarantine(base, repository string, request LegacyFixScopeQuarantineRequest) (CompactReclaimRecord, error) {
	quarantineRoot := filepath.Join(base, "quarantine")
	entries, err := os.ReadDir(quarantineRoot)
	if err != nil && !os.IsNotExist(err) {
		return CompactReclaimRecord{}, fmt.Errorf("inspect quarantine for legacy fix-scope replay: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		payload, readErr := os.ReadFile(filepath.Join(quarantineRoot, entry.Name(), "reclaim-record.json"))
		if readErr != nil {
			continue
		}
		var record CompactReclaimRecord
		if json.Unmarshal(payload, &record) != nil || record.Status != CompactReclaimCommitted || record.LegacyFixScopeQuarantine == nil {
			continue
		}
		proof := record.LegacyFixScopeQuarantine
		if proof.LineageID != request.LineageID || proof.HeadRevision != request.ExpectedRevision ||
			proof.Diagnostic != request.ExpectedDiagnostic || proof.Disposition != request.Disposition ||
			record.Reason != strings.TrimSpace(request.Reason) || record.Actor != strings.TrimSpace(request.Actor) {
			continue
		}
		if request.MaintainerAuthorization != legacyFixScopeQuarantineAuthorizationBinding(
			repository, proof.LineageID, proof.HeadRevision, proof.Diagnostic, proof.Disposition, request.Actor, request.Reason,
		) {
			continue
		}
		return record, nil
	}
	return CompactReclaimRecord{}, fmt.Errorf("review quarantine-legacy-fix-scope refused: lineage %q is absent and no committed quarantine record matches; inspect prepared records under %s", request.LineageID, quarantineRoot)
}
