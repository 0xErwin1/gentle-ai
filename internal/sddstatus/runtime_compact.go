package sddstatus

import (
	"context"
	"errors"
)

type CompactAttemptState string

const (
	CompactStateProceed  CompactAttemptState = "proceed"
	CompactStateBlocked  CompactAttemptState = "blocked"
	CompactStateComplete CompactAttemptState = "complete"
)

type CompactBlockReason string

const (
	CompactBlockActiveAttempt       CompactBlockReason = "active_attempt"
	CompactBlockMaintainerDecision  CompactBlockReason = "maintainer_decision"
	CompactBlockCorruptAuthority    CompactBlockReason = "corrupt_authority"
	CompactBlockInvalidContinuation CompactBlockReason = "invalid_continuation"
	CompactBlockRemediationRequired CompactBlockReason = "remediation_required"
	CompactBlockWorktreeMismatch    CompactBlockReason = "worktree_mismatch"
	CompactBlockAuthorityFailure    CompactBlockReason = "authority_failure"
)

// CompactAttemptResult is the bounded orchestration projection. RuntimeStatus
// remains available through the legacy diagnostic operations only.
//
// Exit and Detail carry a wrapped mutation refusal's message text through to
// this compact boundary (#2249): compactMutationFailure populates both
// whenever it classifies a real error, so a well-constructed refusal that
// names a runnable continuation — like runtimeRemediationExitRefusal's — is
// never silently reduced to a bare Reason. Both stay empty on every happy
// path (proceed, complete-with-no-error).
type CompactAttemptResult struct {
	State  CompactAttemptState `json:"state"`
	Reason CompactBlockReason  `json:"reason,omitempty"`
	Token  string              `json:"token,omitempty"`
	Exit   string              `json:"exit,omitempty"`
	Detail string              `json:"detail,omitempty"`
}

// CompactAcquireRequest is the bounded orchestration projection of
// BeginAttemptRequest. Work-unit scope (WorkUnit, EvidenceGoal, MaxAttempts,
// MaxChangedLines) is owned exactly once, by the embedded BeginAttemptRequest
// (decision 9 / CON-08: RuntimeObjective's BeginAttemptRequest is the single
// work-unit-scope owner). ExpectedRevision is inert here — Acquire always
// derives it from ledger replay state before use — but embedding rather than
// re-declaring keeps the projection from drifting out of sync with its one
// source of truth the way the pre-Wave-4 parallel struct did (#2133/#2151).
type CompactAcquireRequest struct {
	BeginAttemptRequest

	// Token is optional ownership proof (#2291): a distinct call/process
	// (e.g. an actor launched by a parent that already holds a proceed-state
	// acquire) presents the token that acquire already returned to prove it
	// is continuing that SAME attempt rather than colliding with it. A Token
	// matching the ledger's currently active attempt short-circuits to
	// proceed with zero mutation — no store.Begin, no ledger chain touched.
	// A non-matching Token falls through to the ordinary active_attempt
	// block, naming the REAL active token. An empty Token leaves every
	// existing acquire/collide path byte-for-byte unchanged.
	Token string
}

type CompactSettleRequest struct {
	Token              string
	RequestID          string
	Outcome            AttemptOutcome
	EvidenceRevision   string
	Diagnosis          string
	HarnessDisposition HarnessDisposition
	CleanupEvidence    string
	ProcessEvidence    string
	SuccessorLineageID string

	RemediatesEvidenceRevision string
}

// Acquire claims one native attempt without exposing the growing runtime
// history. The returned token identifies that exact begin record for Settle.
func (store RuntimeStore) Acquire(ctx context.Context, request CompactAcquireRequest) (CompactAttemptResult, error) {
	begin, err := normalizeBeginAttemptRequest(request.BeginAttemptRequest)
	if err != nil {
		return CompactAttemptResult{}, err
	}

	replay, err := store.load()
	if err != nil {
		return compactBlocked(CompactBlockCorruptAuthority, ""), nil
	}
	if receipt, exists := replay.Requests[begin.RequestID]; exists {
		record, loadErr := store.loadRecord(receipt.Revision)
		if loadErr != nil {
			return compactBlocked(CompactBlockCorruptAuthority, ""), nil
		}
		begin.ExpectedRevision = record.PreviousRevision
		if !compactAcquireMatches(record, begin) {
			return compactBlocked(CompactBlockInvalidContinuation, ""), nil
		}
		if _, err := store.Begin(ctx, begin); err != nil {
			return store.compactMutationFailure(err, false, begin), nil
		}
		current, loadErr := store.load()
		if loadErr != nil {
			return compactBlocked(CompactBlockCorruptAuthority, ""), nil
		}
		return compactAcquireResult(current, begin, receipt.Revision), nil
	}

	// Zero-mutation ownership check (#2291): this is a pure read-then-compare
	// against the already-loaded replay, modeled after the request-ID replay
	// dedup above — neither branch calls store.Begin or touches the ledger
	// chain. It only applies when an attempt is actually live; an inert
	// Token with no active attempt falls through to the normal begin flow.
	if request.Token != "" && replay.Status.ActiveAttempt != nil {
		activeToken := replay.AttemptTokens[replay.Status.ActiveAttempt.Ordinal]
		if request.Token == activeToken {
			return CompactAttemptResult{State: CompactStateProceed, Token: activeToken}, nil
		}
		return compactForeignAcquireToken(activeToken), nil
	}

	if result, terminal := compactAcquireBlock(replay, begin); terminal {
		return result, nil
	}
	begin.ExpectedRevision = replay.Status.Revision
	started, err := store.Begin(ctx, begin)
	if err != nil {
		return store.compactMutationFailure(err, false, begin), nil
	}
	return CompactAttemptResult{State: CompactStateProceed, Token: started.Revision}, nil
}

// Settle closes the attempt selected by Token through the ordinary Finish
// transition. Current binding and failed-evidence revisions are derived inside
// the authority; callers name a successor only when review approved a distinct
// lineage.
func (store RuntimeStore) Settle(ctx context.Context, request CompactSettleRequest) (CompactAttemptResult, error) {
	if err := normalizeCompactSettleRequest(request); err != nil {
		return CompactAttemptResult{}, err
	}
	replay, err := store.load()
	if err != nil {
		return compactBlocked(CompactBlockCorruptAuthority, ""), nil
	}
	if receipt, exists := replay.Requests[request.RequestID]; exists {
		record, loadErr := store.loadRecord(receipt.Revision)
		if loadErr != nil {
			return compactBlocked(CompactBlockCorruptAuthority, ""), nil
		}
		finish, ok := compactSettleReplayRequest(replay, record, request)
		if !ok {
			return compactBlocked(CompactBlockInvalidContinuation, ""), nil
		}
		if _, err := store.Finish(ctx, finish); err != nil {
			return store.compactMutationFailure(err, true, BeginAttemptRequest{}), nil
		}
		return store.compactSettleResult()
	}

	status := replay.Status
	if status.Complete {
		return CompactAttemptResult{State: CompactStateComplete}, nil
	}
	if status.DecisionRequired {
		return compactBlocked(CompactBlockMaintainerDecision, ""), nil
	}
	if status.ActiveAttempt == nil {
		return compactBlocked(CompactBlockInvalidContinuation, ""), nil
	}
	activeToken := replay.AttemptTokens[status.ActiveAttempt.Ordinal]
	if request.Token != activeToken {
		return compactBlocked(CompactBlockActiveAttempt, activeToken), nil
	}

	finish := FinishAttemptRequest{
		ExpectedRevision: status.Revision, RequestID: request.RequestID, Outcome: request.Outcome,
		EvidenceRevision: request.EvidenceRevision, Diagnosis: request.Diagnosis,
		HarnessDisposition: request.HarnessDisposition, CleanupEvidence: request.CleanupEvidence,
		ProcessEvidence: request.ProcessEvidence,
	}
	explicitSuccessor := request.SuccessorLineageID != ""
	failedEvidence := status.EvidenceRevision
	if failedEvidence == "" {
		failedEvidence = request.RemediatesEvidenceRevision
	}
	if request.RemediatesEvidenceRevision != "" && failedEvidence != request.RemediatesEvidenceRevision {
		return compactBlocked(CompactBlockInvalidContinuation, ""), nil
	}
	if request.Outcome == AttemptPassed && status.Binding != nil && failedEvidence != "" && (!store.ReviewDisabled || explicitSuccessor || request.RemediatesEvidenceRevision != "") {
		finish.ExpectedBindingRevision = status.Binding.Revision
		finish.SuccessorLineageID = request.SuccessorLineageID
		if finish.SuccessorLineageID == "" {
			finish.SuccessorLineageID = status.Binding.Lineage
		}
		finish.RemediatesEvidenceRevision = failedEvidence
	} else if explicitSuccessor || request.RemediatesEvidenceRevision != "" {
		return compactBlocked(CompactBlockInvalidContinuation, ""), nil
	}
	if _, err := store.Finish(ctx, finish); err != nil {
		return store.compactMutationFailure(err, true, BeginAttemptRequest{}), nil
	}
	return store.compactSettleResult()
}

func normalizeCompactSettleRequest(request CompactSettleRequest) error {
	_, err := normalizeFinishAttemptRequest(FinishAttemptRequest{
		ExpectedRevision: request.Token, RequestID: request.RequestID, Outcome: request.Outcome,
		EvidenceRevision: request.EvidenceRevision, Diagnosis: request.Diagnosis,
		HarnessDisposition: request.HarnessDisposition, CleanupEvidence: request.CleanupEvidence,
		ProcessEvidence: request.ProcessEvidence,
	})
	if err != nil {
		return err
	}
	if request.SuccessorLineageID != "" && !validReviewBindingLineage(request.SuccessorLineageID) {
		return errors.New("successor_lineage_id must be a canonical lowercase lineage; rerun `gentle-ai sdd-attempt settle` with a lowercase --successor-lineage")
	}
	if request.RemediatesEvidenceRevision != "" && !runtimeRevisionPattern.MatchString(request.RemediatesEvidenceRevision) {
		return errors.New("remediates_evidence_revision must be sha256; rerun `gentle-ai sdd-attempt settle` with --remediates-evidence-revision sha256:<64-lowercase-hex>")
	}
	return nil
}

func compactAcquireMatches(record runtimeRecord, request BeginAttemptRequest) bool {
	if (record.Operation != runtimeOperationBegin && record.Operation != runtimeOperationAdvance) || record.Begin == nil {
		return false
	}
	event := record.Begin
	return request == (BeginAttemptRequest{
		ExpectedRevision: record.PreviousRevision, RequestID: record.RequestID, WorkUnit: event.WorkUnit,
		EvidenceGoal: event.EvidenceGoal, MaxAttempts: event.MaxAttempts, MaxChangedLines: event.MaxChangedLines,
	})
}

func compactSettleReplayRequest(replay runtimeReplay, record runtimeRecord, request CompactSettleRequest) (FinishAttemptRequest, bool) {
	if record.Finish == nil || (record.Operation != runtimeOperationFinish && record.Operation != runtimeOperationFinishRemediation) {
		return FinishAttemptRequest{}, false
	}
	event := record.Finish
	finish := FinishAttemptRequest{
		ExpectedRevision: record.PreviousRevision, RequestID: record.RequestID, Outcome: event.Outcome,
		EvidenceRevision: event.EvidenceRevision, Diagnosis: event.Diagnosis,
		HarnessDisposition: event.HarnessDisposition, CleanupEvidence: event.CleanupEvidence,
		ProcessEvidence: event.ProcessEvidence,
	}
	if record.Operation == runtimeOperationFinishRemediation {
		finish.ExpectedBindingRevision = record.Binding.ExpectedRevision
		finish.SuccessorLineageID = record.Binding.Current.Lineage
		finish.RemediatesEvidenceRevision = event.RemediatesEvidenceRevision
	}
	effectiveSuccessor := request.SuccessorLineageID
	if effectiveSuccessor == "" && record.Operation == runtimeOperationFinishRemediation {
		effectiveSuccessor = replay.Requests[record.RequestID].RemediationPredecessorLineage
	}
	matches := request.Token == replay.AttemptTokens[event.Ordinal] && request.RequestID == finish.RequestID &&
		request.Outcome == finish.Outcome && request.EvidenceRevision == finish.EvidenceRevision &&
		request.Diagnosis == finish.Diagnosis && request.HarnessDisposition == finish.HarnessDisposition &&
		request.CleanupEvidence == finish.CleanupEvidence && request.ProcessEvidence == finish.ProcessEvidence &&
		effectiveSuccessor == finish.SuccessorLineageID && request.RemediatesEvidenceRevision == finish.RemediatesEvidenceRevision
	return finish, matches
}

// compactAcquireBlock needs the request because completion is scoped to one
// objective: a passed apply is terminal for its own work unit while remaining an
// ordinary predecessor for the distinct verification the SDD graph still owes.
func compactAcquireBlock(replay runtimeReplay, request BeginAttemptRequest) (CompactAttemptResult, bool) {
	status := replay.Status
	switch {
	case status.Complete:
		if runtimeObjectiveAdvanceAdmissible(status, request) {
			return CompactAttemptResult{}, false
		}
		return CompactAttemptResult{State: CompactStateComplete}, true
	case status.DecisionRequired:
		return compactBlocked(CompactBlockMaintainerDecision, ""), true
	case status.ActiveAttempt != nil:
		return compactBlocked(CompactBlockActiveAttempt, replay.AttemptTokens[status.ActiveAttempt.Ordinal]), true
	default:
		return CompactAttemptResult{}, false
	}
}

func compactAcquireResult(replay runtimeReplay, request BeginAttemptRequest, ownedToken string) CompactAttemptResult {
	if result, terminal := compactAcquireBlock(replay, request); terminal {
		if result.Reason == CompactBlockActiveAttempt && result.Token == ownedToken {
			return CompactAttemptResult{State: CompactStateProceed, Token: ownedToken}
		}
		return result
	}
	return compactBlocked(CompactBlockInvalidContinuation, "")
}

func (store RuntimeStore) compactSettleResult(expected ...string) (CompactAttemptResult, error) {
	replay, err := store.load()
	if err != nil {
		return compactBlocked(CompactBlockCorruptAuthority, ""), nil
	}
	status := replay.Status
	if len(expected) == 1 && status.Revision != expected[0] {
		return compactBlocked(CompactBlockCorruptAuthority, ""), nil
	}
	switch {
	case status.Complete:
		return CompactAttemptResult{State: CompactStateComplete}, nil
	case status.DecisionRequired:
		return compactBlocked(CompactBlockMaintainerDecision, ""), nil
	case status.ActiveAttempt != nil:
		return compactBlocked(CompactBlockActiveAttempt, replay.AttemptTokens[status.ActiveAttempt.Ordinal]), nil
	default:
		return CompactAttemptResult{State: CompactStateProceed}, nil
	}
}

func (store RuntimeStore) compactMutationFailure(err error, settle bool, begin BeginAttemptRequest) CompactAttemptResult {
	var publication *RuntimePublicationError
	if errors.As(err, &publication) && publication.Committed {
		if settle {
			result, _ := store.compactSettleResult(publication.Revision)
			return result
		}
		replay, loadErr := store.load()
		if loadErr != nil {
			return compactBlocked(CompactBlockCorruptAuthority, "")
		}
		return compactAcquireResult(replay, begin, publication.Revision)
	}
	// Every branch below wraps a real error, so `detail` always has message
	// text to carry through to the compact JSON boundary instead of being
	// thrown away behind a bare classification (#2249). `exit` reuses the
	// same text: these refusals are constructed specifically to name a
	// runnable continuation inline (see runtimeRemediationExitRefusal), and
	// there is no reliable field-agnostic way to shorten that further.
	detail := err.Error()
	reason := CompactBlockAuthorityFailure
	switch {
	case errors.Is(err, ErrRuntimeObjectiveDone):
		return CompactAttemptResult{State: CompactStateComplete, Exit: detail, Detail: detail}
	case errors.Is(err, ErrRuntimeBudgetExhausted), errors.Is(err, ErrRuntimeObjectiveChange):
		reason = CompactBlockMaintainerDecision
	case errors.Is(err, ErrRuntimeAttemptActive):
		reason = CompactBlockActiveAttempt
	case errors.Is(err, ErrRuntimeRevisionConflict), errors.Is(err, ErrRuntimeConcurrentUpdate),
		errors.Is(err, ErrRuntimeRequestConflict), errors.Is(err, ErrRuntimeNoActiveAttempt),
		errors.Is(err, ErrBindingRevisionConflict):
		reason = CompactBlockInvalidContinuation
	// ErrRuntimeRemediationSuccessorRequired is the sentinel behind
	// runtimeRemediationExitRefusal (runtime_ledger.go:628-646): the candidate
	// moved after Begin, so a passing finish demands the remediation trio
	// naming its own runnable exit. It previously fell through to the
	// default authority_failure and lost that exit entirely (#2249).
	case errors.Is(err, ErrRuntimeRemediationSuccessorRequired):
		reason = CompactBlockRemediationRequired
	// ErrRuntimeWorktreeMismatch is the sentinel behind
	// runtimeWorktreeMismatchRefusal (#2296 part 1): Finish is running from a
	// different linked worktree than the one Begin recorded. Left
	// unclassified it would fall through to the opaque default
	// authority_failure and lose the exact --cwd the refusal names.
	case errors.Is(err, ErrRuntimeWorktreeMismatch):
		reason = CompactBlockWorktreeMismatch
	}
	return CompactAttemptResult{State: CompactStateBlocked, Reason: reason, Exit: detail, Detail: detail}
}

func compactBlocked(reason CompactBlockReason, token string) CompactAttemptResult {
	return CompactAttemptResult{State: CompactStateBlocked, Reason: reason, Token: token}
}

// compactForeignAcquireToken names the exact continuation for a losing
// ownership check (#2291): the caller presented a token, but it is not the
// ledger's live active attempt. It always carries the REAL active token —
// never the foreign one the caller supplied — through Token, Exit, and
// Detail alike, so a legible refusal (slice 1) also names how to proceed.
func compactForeignAcquireToken(activeToken string) CompactAttemptResult {
	detail := "a distinct attempt token " + activeToken + " is already active for this work unit; " +
		"rerun `gentle-ai sdd-attempt acquire --token " + activeToken + "` to continue that exact attempt, " +
		"or settle it with `gentle-ai sdd-attempt settle --token " + activeToken + "` before acquiring a new one"
	return CompactAttemptResult{State: CompactStateBlocked, Reason: CompactBlockActiveAttempt, Token: activeToken, Exit: detail, Detail: detail}
}
