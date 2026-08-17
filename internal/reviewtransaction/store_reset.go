package reviewtransaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Clone-scoped review store reset.
//
// Review authority accumulates without bound under <git-common-dir>/gentle-ai:
// every candidate leaves a lineage behind, every lineage stays after delivery,
// and nothing removes a delivered one. `review abandon` refuses terminal
// states and `review reclaim` refuses any entry holding authoritative
// artifacts, so a store that has degraded has no exit at all. This is that
// exit, and it is deliberately the bluntest operation in the package: it does
// not repair, migrate, or reconcile anything. It removes the lineage state for
// one clone so the next review starts from nothing.
//
// Three rules shape the whole file.
//
// It is an allowlist, never a denylist. Only paths named in
// storeResetRemovableTargets are ever removed. Anything else -- including a
// directory a future release adds -- is reported and left exactly where it is.
// The failure mode of a denylist here is destroying state nobody remembered to
// exclude, and that failure is silent.
//
// The kill switch is not lineage state. It lives in two places, and one of them
// (review-transactions/rar-authority/v1/rdd-mode) is *inside* the authority
// tree, still written for gentle-ai builds installed before #2882 that read
// only that location. A reset that removed review-transactions/ wholesale would
// switch reviews back on for a user who had turned them off, which is the worst
// thing this command could possibly do.
//
// Honest accounting beats a clean-looking report. Bytes are counted as freed
// only after they are actually gone. A category that could not be moved is
// reported with its reason, is not counted, and makes the whole run incomplete.

// StoreResetReportSchema identifies the clone-scoped review store reset
// projection.
const StoreResetReportSchema = "gentle-ai.review-store-reset/v1"

const (
	// StoreResetPreview names a read-only survey that removes nothing.
	StoreResetPreview = "preview"
	// StoreResetApplied names a run that attempted removal.
	StoreResetApplied = "reset"
)

// storeResetLockTimeout bounds the exclusive maintenance lease. A reset that
// cannot get the lease promptly is competing with a live review, and waiting
// longer only makes the collision worse.
const storeResetLockTimeout = 30 * time.Second

// storeResetStagingPrefix names the directory a run moves categories into
// before deleting them. Staging is what makes the store consistent at one
// instant instead of during a long recursive walk: a concurrent reader either
// sees a category or does not, never half of one.
const storeResetStagingPrefix = ".store-reset-"

// storeResetRename is the rename seam, mirroring reclaimQuarantineResidue in
// compact_reclaim.go so partial-failure paths stay testable.
var storeResetRename = os.Rename

// storeResetAcquireLease is the lease seam. Production always takes the real
// exclusive maintenance lease; a test substitutes a wrapper around it to prove,
// from a barrier rather than from timing, that the store is classified only
// after the lease is held.
var storeResetAcquireLease = AcquireReviewMaintenanceExclusive

// storeResetTarget names one path under <git-common-dir>/gentle-ai and why it
// is in the list it is in. The reason is not decoration: this command destroys
// data, and every entry it touches or spares has to be able to justify itself
// to the person reading the preview.
type storeResetTarget struct {
	name   string
	parts  []string
	reason string
	// candidateViews marks the one category that is not merely files.
	// Candidate views are registered Git worktrees written read-only, so the
	// administrative directories that register them go with the checkouts and
	// the permissions have to be relaxed before anything can be unlinked.
	candidateViews bool
}

// storeResetRemovableTargets is the complete inclusion list. Every entry is
// state derived from reviewing candidates; none of it is a user preference, and
// none of it can be authored by hand.
var storeResetRemovableTargets = []storeResetTarget{
	{
		name: "candidate-views", parts: []string{"candidate-views"}, candidateViews: true,
		reason: "per-candidate repository checkouts materialized for reviewer inspection; regenerated on demand and the largest consumer of disk",
	},
	{
		name: "review-transactions/v1", parts: []string{"review-transactions", "v1"},
		reason: "legacy per-lineage authority event chains; read-only in current builds",
	},
	{
		name: "review-transactions/v2", parts: []string{"review-transactions", "v2"},
		reason: "compact per-lineage authority records, receipts, and captured reviewer results",
	},
	{
		name: "review-transactions/quarantine", parts: []string{"review-transactions", "quarantine"},
		reason: "audited residue of lineages already discarded by reclaim or abandon",
	},
	{
		name: "review-transactions/effect-markers", parts: []string{"review-transactions", "effect-markers"},
		reason: "per-lineage delivery idempotence markers; meaningless once their lineage is gone",
	},
	{
		name: "review-transactions/incidents", parts: []string{"review-transactions", "incidents"},
		reason: "per-lineage preserved raw reviewer results",
	},
	{
		name: "reviews", parts: []string{"reviews"},
		reason: "superseded review graph store written by the gentle-pi adapter; per-lineage authority in an earlier format",
	},
}

// storeResetPreservedTargets is the complete exclusion list. Two of these are
// the kill switch, two are state another component owns, and two are paths no
// gentle-ai code has ever written -- which is itself the reason.
var storeResetPreservedTargets = []storeResetTarget{
	{
		name: "review-mode", parts: []string{"review-mode"},
		reason: "the receipt-driven-development kill switch and its consent latch: a user preference, not lineage state",
	},
	{
		name: "review-transactions/rar-authority", parts: []string{"review-transactions", "rar-authority"},
		reason: "holds the pre-#2882 mirror of that same kill switch, still written for installed builds that read only this location",
	},
	{
		name: "sdd-runtime", parts: []string{"sdd-runtime"},
		reason: "SDD attempt state keyed by change name rather than by review lineage; removing it would lose in-flight SDD work",
	},
	{
		name: "defect-reports", parts: []string{"defect-reports"},
		reason: "diagnostic reports that may not have been filed upstream yet; evidence this command cannot regenerate",
	},
	{
		name: "review-artifacts", parts: []string{"review-artifacts"},
		reason: "no gentle-ai code writes or reads this path, so its contents were placed by hand; a reset never removes what the product did not create",
	},
	{
		name: "incidents", parts: []string{"incidents"},
		reason: "same: nothing in this product writes this path, and per-lineage incidents live under review-transactions/incidents instead",
	},
	{
		name: "REVIEW-MAINTENANCE.lock", parts: []string{"REVIEW-MAINTENANCE.lock"},
		reason: "the advisory maintenance lease this operation itself holds; it carries no lineage state and is recreated on demand",
	},
}

// StoreResetEntry is one category's line in the report.
type StoreResetEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Present bool   `json:"present"`
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
	Removed bool   `json:"removed"`
	// Skipped carries why a present removable category was not removed. It is
	// only ever set on a run that tried.
	Skipped string `json:"skipped,omitempty"`
}

// StoreResetLineage names one review that is still open, or one whose record
// this command could not read.
type StoreResetLineage struct {
	LineageID string `json:"lineage_id"`
	State     State  `json:"state,omitempty"`
	// Unreadable carries the parse failure for a record that could not be
	// classified. Such a record is treated as in-flight, because guessing the
	// other way destroys work.
	Unreadable string `json:"unreadable,omitempty"`
}

// StoreResetReport is what both the preview and the reset return. The two carry
// the same shape on purpose: the preview is not a different report, it is the
// same report with nothing removed yet.
type StoreResetReport struct {
	Schema     string `json:"schema"`
	Operation  string `json:"operation"`
	Repository string `json:"repository"`
	StoreRoot  string `json:"store_root"`
	// Removable, Preserved, and Unrecognized together account for every entry
	// found under the store root. Nothing is left out of the report because
	// this command did not recognize it -- that is precisely what Unrecognized
	// is for.
	Removable    []StoreResetEntry   `json:"removable"`
	Preserved    []StoreResetEntry   `json:"preserved"`
	Unrecognized []StoreResetEntry   `json:"unrecognized"`
	InFlight     []StoreResetLineage `json:"in_flight"`
	Settled      int                 `json:"settled_lineages"`
	RemovedFiles int                 `json:"removed_files"`
	RemovedBytes int64               `json:"removed_bytes"`
	// Residue names a staging directory a failed run could not delete. Its
	// contents are already out of the store, but the bytes are still on disk
	// and are not counted as freed.
	Residue string `json:"residue,omitempty"`
	// Complete is false whenever any present removable category was not
	// removed, for any reason. A partial run never reports success.
	Complete bool `json:"complete"`
}

// StoreResetRequest carries the one decision the caller owns.
type StoreResetRequest struct {
	// IncludeInFlight permits removing lineages that have not reached a
	// terminal state. It exists so the refusal has an exit, not so callers can
	// set it by default.
	IncludeInFlight bool
}

// StoreResetInFlightError refuses a reset that would destroy open reviews.
type StoreResetInFlightError struct {
	Repository string
	Lineages   []StoreResetLineage
}

func (err *StoreResetInFlightError) Error() string {
	names := make([]string, 0, len(err.Lineages))
	for _, lineage := range err.Lineages {
		if lineage.Unreadable != "" {
			names = append(names, fmt.Sprintf("%s (unreadable: %s)", lineage.LineageID, lineage.Unreadable))
			continue
		}
		names = append(names, fmt.Sprintf("%s (%s)", lineage.LineageID, lineage.State))
	}
	return fmt.Sprintf(
		"review store reset refused: %d review(s) have not reached a terminal state: %s; finish or abandon them, or run `gentle-ai review store-reset --cwd %s --confirm --include-in-flight` to remove them anyway",
		len(err.Lineages), strings.Join(names, ", "), err.Repository,
	)
}

// StoreResetIncompleteError reports a run that removed some categories and not
// others. It carries no continuation of its own because the answer is always
// the same one: read the report, fix what the skip reason names, run it again.
type StoreResetIncompleteError struct {
	Skipped []StoreResetEntry
	Residue string
}

func (err *StoreResetIncompleteError) Error() string {
	if len(err.Skipped) == 0 && err.Residue != "" {
		return fmt.Sprintf("review store reset removed every category but could not delete its staging directory %s; the bytes are not reclaimed until it is removed", err.Residue)
	}
	names := make([]string, 0, len(err.Skipped))
	for _, entry := range err.Skipped {
		names = append(names, fmt.Sprintf("%s (%s)", entry.Name, entry.Skipped))
	}
	return fmt.Sprintf("review store reset was incomplete: %s", strings.Join(names, "; "))
}

// storeResetRoot resolves <git-common-dir>/gentle-ai for one clone.
//
// It deliberately does not apply the receipt-driven-development gate. The kill
// switch decides whether reviews happen; it does not decide whether a user may
// delete their own accumulated state. Gating cleanup on it would recreate
// exactly the dead end this command exists to remove: a user whose store has
// degraded turns reviews off, and then discovers the only tool that could clean
// up refuses because reviews are off.
func storeResetRoot(ctx context.Context, repo string) (root, repository string, err error) {
	lease, err := OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return "", "", err
	}
	identity := lease.Identity()
	return filepath.Join(identity.GitCommonDir, "gentle-ai"), identity.RepositoryRoot, nil
}

// SurveyReviewStore reports what a reset would remove without touching
// anything. It creates no directories, takes no lock, and is safe to run
// against a store no other operation can open.
func SurveyReviewStore(ctx context.Context, repo string) (StoreResetReport, error) {
	report, _, err := surveyReviewStore(ctx, repo)
	return report, err
}

func surveyReviewStore(ctx context.Context, repo string) (StoreResetReport, string, error) {
	if err := ctx.Err(); err != nil {
		return StoreResetReport{}, "", err
	}
	root, repository, err := storeResetRoot(ctx, repo)
	if err != nil {
		return StoreResetReport{}, "", err
	}
	report := StoreResetReport{
		Schema: StoreResetReportSchema, Operation: StoreResetPreview,
		Repository: repository, StoreRoot: root,
		Removable: []StoreResetEntry{}, Preserved: []StoreResetEntry{},
		Unrecognized: []StoreResetEntry{}, InFlight: []StoreResetLineage{},
		Complete: true,
	}
	for _, target := range storeResetRemovableTargets {
		report.Removable = append(report.Removable, storeResetMeasure(root, target))
	}
	for _, target := range storeResetPreservedTargets {
		report.Preserved = append(report.Preserved, storeResetMeasure(root, target))
	}
	report.Unrecognized = storeResetUnrecognized(root)
	report.InFlight, report.Settled, err = storeResetLineages(root)
	if err != nil {
		return report, root, err
	}
	return report, root, nil
}

// storeResetMeasure sizes one target without following symlinks into it.
func storeResetMeasure(root string, target storeResetTarget) StoreResetEntry {
	path := filepath.Join(append([]string{root}, target.parts...)...)
	entry := StoreResetEntry{
		Name: target.name, Path: strings.Join(target.parts, "/"), Reason: target.reason,
	}
	info, err := os.Lstat(path)
	if err != nil {
		return entry
	}
	entry.Present = true
	if !info.IsDir() {
		entry.Files, entry.Bytes = 1, info.Size()
		return entry
	}
	entry.Files, entry.Bytes = storeResetUsage(path)
	return entry
}

// storeResetUsage sums one tree. Errors are absorbed rather than propagated:
// this is a report about a store that may well be damaged, and refusing to
// print a number because one subdirectory is unreadable would withhold the
// whole survey over a detail.
func storeResetUsage(path string) (files int, bytes int64) {
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is skipped, not fatal. The count that
			// results is a floor, which is the honest direction for a preview.
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		files++
		if info.Mode().IsRegular() {
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes
}

// storeResetUnrecognized reports every entry under the store root that neither
// list covers. This is the allowlist made visible: a future directory shows up
// here instead of quietly disappearing.
func storeResetUnrecognized(root string) []StoreResetEntry {
	known := map[string]bool{}
	nested := map[string]bool{}
	for _, target := range append(append([]storeResetTarget{}, storeResetRemovableTargets...), storeResetPreservedTargets...) {
		if len(target.parts) == 1 {
			known[target.parts[0]] = true
			continue
		}
		// A target one level down means its parent is only partly claimed, so
		// the parent itself is not "known" -- its other children still have to
		// be reported.
		nested[target.parts[0]] = true
		known[strings.Join(target.parts, "/")] = true
	}
	unrecognized := []StoreResetEntry{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return unrecognized
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, storeResetStagingPrefix) {
			// Residue from an interrupted run of this same command. It is
			// reported, never silently reused.
			unrecognized = append(unrecognized, storeResetUnrecognizedEntry(root, name,
				"staging residue from an interrupted review store reset"))
			continue
		}
		if known[name] {
			continue
		}
		if nested[name] {
			children, err := os.ReadDir(filepath.Join(root, name))
			if err != nil {
				continue
			}
			for _, child := range children {
				relative := name + "/" + child.Name()
				if known[relative] {
					continue
				}
				unrecognized = append(unrecognized, storeResetUnrecognizedEntry(root, relative,
					"not covered by this command's inclusion or exclusion list"))
			}
			continue
		}
		unrecognized = append(unrecognized, storeResetUnrecognizedEntry(root, name,
			"not covered by this command's inclusion or exclusion list"))
	}
	sort.Slice(unrecognized, func(i, j int) bool { return unrecognized[i].Name < unrecognized[j].Name })
	return unrecognized
}

func storeResetUnrecognizedEntry(root, relative, reason string) StoreResetEntry {
	entry := StoreResetEntry{Name: relative, Path: relative, Reason: reason, Present: true}
	entry.Files, entry.Bytes = storeResetUsage(filepath.Join(root, filepath.FromSlash(relative)))
	return entry
}

// storeResetStateRecord is the smallest shape that answers "is this lineage
// still open". The full record parser is deliberately not used: it validates
// far more than this question needs, and a record it rejects is exactly the
// record this command has to be able to classify.
type storeResetStateRecord struct {
	State struct {
		LineageID string `json:"lineage_id"`
		State     State  `json:"state"`
	} `json:"state"`
}

// storeResetLineages partitions compact-v2 lineages into settled and in-flight.
//
// Terminal is the same set the rest of the package uses -- approved, escalated,
// invalidated (authorityStatusForState) -- and everything else is an open
// review. A record that cannot be read counts as in-flight, because a damaged
// store is precisely when that call gets made and the safe direction is to keep
// the work and make the user say the word.
//
// Legacy v1 lineages are not classified. Their state lives in an append-only
// event chain no current build can advance, so there is no open v1 review to
// protect; the directory is removed as settled residue.
func storeResetLineages(root string) ([]StoreResetLineage, int, error) {
	inFlight := []StoreResetLineage{}
	settled := 0
	base := filepath.Join(root, "review-transactions", "v2")
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return inFlight, 0, nil
		}
		return inFlight, 0, fmt.Errorf("read compact review authority: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record := filepath.Join(base, entry.Name(), compactStateFileName)
		payload, err := os.ReadFile(record)
		if err != nil {
			if os.IsNotExist(err) {
				// No state record at all: crash residue, not a review.
				settled++
				continue
			}
			inFlight = append(inFlight, StoreResetLineage{LineageID: entry.Name(), Unreadable: err.Error()})
			continue
		}
		var decoded storeResetStateRecord
		if err := json.Unmarshal(payload, &decoded); err != nil {
			inFlight = append(inFlight, StoreResetLineage{LineageID: entry.Name(), Unreadable: "state record does not parse"})
			continue
		}
		state := decoded.State.State
		if state == "" {
			inFlight = append(inFlight, StoreResetLineage{LineageID: entry.Name(), Unreadable: "state record names no state"})
			continue
		}
		switch authorityStatusForState(state) {
		case AuthorityStatusApproved, AuthorityStatusEscalated, AuthorityStatusInvalidated:
			settled++
		default:
			inFlight = append(inFlight, StoreResetLineage{LineageID: entry.Name(), State: state})
		}
	}
	sort.Slice(inFlight, func(i, j int) bool { return inFlight[i].LineageID < inFlight[j].LineageID })
	return inFlight, settled, nil
}

// ResetReviewStore removes the clone's review lineage state.
//
// This is irreversible. Nothing is copied aside for later recovery: the
// package's existing quarantine (quarantineCompactStoreEntry) renames entries
// into review-transactions/quarantine, which is inside the very store being
// reset, so reusing it would free no disk and would refuse every lineage
// holding authoritative artifacts -- which is all of them. Reclaiming the space
// is the point, so the space is reclaimed and the report says so plainly.
func ResetReviewStore(ctx context.Context, repo string, request StoreResetRequest) (StoreResetReport, error) {
	// The lease is taken before the store is read, and everything that decides
	// what happens is decided under it.
	//
	// Surveying first and leasing afterwards would make the in-flight refusal
	// advisory against exactly the population the lease exists to exclude. A
	// review started, or transitioned out of a terminal state, in the window
	// between the read and the lease is invisible to a decision already made,
	// and what follows is not a per-lineage delete that would simply miss it:
	// it renames whole categories, so the new lineage is destroyed by a run
	// that reported zero reviews in flight. Every review writer takes the
	// shared side of this same lease before touching a per-lineage store
	// (acquireStoreLock), so holding it exclusively is what makes the
	// classification and the removal one atomic step against other processes.
	lockCtx, cancel := context.WithTimeout(ctx, storeResetLockTimeout)
	defer cancel()
	lock, err := storeResetAcquireLease(lockCtx, repo)
	if err != nil {
		// No report is returned with this. Nothing was read under the lease, so
		// there is no classification this command is entitled to state, and a
		// report rendered from an unleased read is the same stale picture the
		// ordering above exists to refuse.
		return StoreResetReport{}, fmt.Errorf("acquire review maintenance lease: %w", err)
	}
	defer func() { _ = lock.Release() }()

	report, root, err := surveyReviewStore(ctx, repo)
	if err != nil {
		return report, err
	}
	report.Operation = StoreResetApplied
	if len(report.InFlight) > 0 && !request.IncludeInFlight {
		report.Complete = false
		return report, &StoreResetInFlightError{Repository: report.Repository, Lineages: report.InFlight}
	}

	return storeResetExecute(ctx, report, root)
}

func storeResetExecute(ctx context.Context, report StoreResetReport, root string) (StoreResetReport, error) {
	staging, err := os.MkdirTemp(root, storeResetStagingPrefix)
	if err != nil {
		report.Complete = false
		return report, fmt.Errorf("create review store reset staging directory: %w", err)
	}

	targets := map[string]storeResetTarget{}
	for _, target := range storeResetRemovableTargets {
		targets[target.name] = target
	}
	staged := int64(0)
	for index, entry := range report.Removable {
		if err := ctx.Err(); err != nil {
			report.Complete = false
			return report, err
		}
		if !entry.Present {
			continue
		}
		target := targets[entry.Name]
		source := filepath.Join(append([]string{root}, target.parts...)...)
		var stagedAdmin []storeResetStagedAdminDir
		restoreMode := func() {}
		if target.candidateViews {
			// Candidate views are registered worktrees, so which administrative
			// directories belong to them has to be established before the move:
			// the gitdir linkage that proves ownership names each view at its
			// current path. They are moved aside rather than deleted, so this
			// preparation is undoable right up until the rename below commits.
			var err error
			stagedAdmin, err = storeResetStageCandidateViewAdminDirs(root, source, staging)
			if err != nil {
				report.Removable[index].Skipped = storeResetSkipReason(err, storeResetRestoreAdminDirs(stagedAdmin))
				report.Complete = false
				continue
			}
			// Moving a directory to a new parent needs write permission on the
			// directory itself, and the adapter writes candidate views
			// read-only. Only this one directory is relaxed, and only until the
			// rename either commits or is undone.
			restoreMode = storeResetRelaxForRename(source)
		}
		destination := filepath.Join(staging, strings.ReplaceAll(entry.Path, "/", "-"))
		if err := storeResetRename(source, destination); err != nil {
			restoreMode()
			report.Removable[index].Skipped = storeResetSkipReason(err, storeResetRestoreAdminDirs(stagedAdmin))
			report.Complete = false
			continue
		}
		report.Removable[index].Removed = true
		report.RemovedFiles += entry.Files
		staged += entry.Bytes
	}
	_ = SyncReviewDirectory(root)

	// Bytes count as freed only once they are actually gone. If the staging
	// directory survives, the categories are out of the store but the disk is
	// not back, and saying otherwise would be the exact dishonest report this
	// command must not produce.
	if err := storeResetRemoveAll(staging); err != nil {
		_, residual := storeResetUsage(staging)
		report.RemovedBytes = staged - residual
		report.Residue = staging
		report.Complete = false
		return report, &StoreResetIncompleteError{Skipped: storeResetSkipped(report), Residue: staging}
	}
	report.RemovedBytes = staged
	if !report.Complete {
		return report, &StoreResetIncompleteError{Skipped: storeResetSkipped(report)}
	}
	return report, nil
}

func storeResetSkipped(report StoreResetReport) []StoreResetEntry {
	skipped := []StoreResetEntry{}
	for _, entry := range report.Removable {
		if entry.Skipped != "" {
			skipped = append(skipped, entry)
		}
	}
	return skipped
}

// storeResetStagedAdminDir records one Git worktree administrative directory
// moved out of the way, and where it came from.
type storeResetStagedAdminDir struct{ original, staged string }

// storeResetStageCandidateViewAdminDirs moves aside the Git worktree
// administrative directories that point into a candidate-views tree.
//
// It moves rather than deletes because the category is not actually removed
// until the rename that follows succeeds, and until then the store has to be
// exactly as it was found. Deleting first is unrepairable: a rename that then
// fails leaves the checkouts on disk with their registrations gone, so Git
// commands inside them fail, the repository's worktree list no longer names
// them, and a later review cannot register the same view again. A rename into
// the same staging directory the categories go to costs nothing, is undone by
// renaming back, and is deleted along with everything else once the category is
// really gone.
//
// An admin directory is moved only when its own gitdir file proves it belongs
// to this exact candidate view. That check is what keeps the operation from
// touching a worktree the user created: a global `git worktree prune` would
// also clear unrelated stale entries, and this command has no business
// deciding that.
func storeResetStageCandidateViewAdminDirs(root, views, staging string) ([]storeResetStagedAdminDir, error) {
	commonDir := filepath.Dir(root)
	entries, err := os.ReadDir(views)
	if err != nil {
		return nil, err
	}
	staged := []storeResetStagedAdminDir{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		view := filepath.Join(views, entry.Name())
		admin := filepath.Join(commonDir, "worktrees", entry.Name())
		if !storeResetAdminDirBelongsTo(admin, view) {
			continue
		}
		destination := filepath.Join(staging, "worktrees-"+entry.Name())
		if err := storeResetRename(admin, destination); err != nil {
			return staged, fmt.Errorf("stage worktree administrative directory for candidate view %q: %w", entry.Name(), err)
		}
		staged = append(staged, storeResetStagedAdminDir{original: admin, staged: destination})
	}
	return staged, nil
}

// storeResetRestoreAdminDirs puts back every administrative directory staged
// for a category that was then not removed.
func storeResetRestoreAdminDirs(staged []storeResetStagedAdminDir) error {
	failures := []error{}
	for _, entry := range staged {
		if err := storeResetRename(entry.staged, entry.original); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// storeResetSkipReason names why a category was left alone, and says so louder
// when undoing the preparation for it did not fully work: a restore that failed
// is the one case where the store is not exactly as this run found it.
func storeResetSkipReason(cause, restore error) string {
	if restore == nil {
		return cause.Error()
	}
	return fmt.Sprintf(
		"%v; and the worktree administrative directories moved aside for it could not be restored: %v",
		cause, restore,
	)
}

// storeResetRelaxForRename grants write permission on one directory so it can
// be moved to a new parent, and returns the undo. It changes nothing when the
// directory is already writable, missing, or not a directory.
func storeResetRelaxForRename(path string) func() {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return func() {}
	}
	mode := info.Mode().Perm()
	if mode&0o200 != 0 {
		return func() {}
	}
	if err := os.Chmod(path, mode|0o700); err != nil {
		return func() {}
	}
	return func() { _ = os.Chmod(path, mode) }
}

// storeResetAdminDirBelongsTo reports whether a worktree admin directory points
// at exactly this candidate view.
func storeResetAdminDirBelongsTo(admin, view string) bool {
	payload, err := os.ReadFile(filepath.Join(admin, "gitdir"))
	if err != nil {
		return false
	}
	recorded := filepath.Clean(strings.TrimSpace(string(payload)))
	return recorded == filepath.Clean(filepath.Join(view, ".git"))
}

// storeResetMakeWritable relaxes directory permissions across a tree so its
// contents can be unlinked. The adapter writes candidate views at 0o555, which
// forbids removing anything inside them.
func storeResetMakeWritable(path string) error {
	return filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o200 != 0 {
			return nil
		}
		return os.Chmod(current, info.Mode().Perm()|0o700)
	})
}

// storeResetRemoveAll deletes a tree, relaxing permissions once if the first
// attempt is refused. Retrying exactly once keeps a genuine fault (a busy file,
// a full disk, a vanished mount) from turning into a loop.
func storeResetRemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || !errors.Is(err, os.ErrPermission) {
		return err
	}
	if writableErr := storeResetMakeWritable(path); writableErr != nil {
		return err
	}
	return os.RemoveAll(path)
}
