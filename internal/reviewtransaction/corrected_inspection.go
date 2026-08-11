package reviewtransaction

import (
	"context"
	"errors"
	"reflect"
)

// ResolveCorrectedCandidateInspection resolves the immutable correction snapshot
// for a captured targeted-validation request without rebuilding live workspace
// content. The returned snapshot can be passed to SnapshotBuilder.InspectCandidate.
func ResolveCorrectedCandidateInspection(ctx context.Context, repositoryContext string, request TargetedValidationRequest) (Snapshot, error) {
	if err := ValidateTargetedValidationRequest(request); err != nil {
		return Snapshot{}, errors.New("corrected candidate inspection request is invalid") // refusal:by-design operator-knowledge: only a fresh native transition can provide an exact provider-issued request
	}
	repo, binding, err := resolveOpaqueReviewRepositoryContext(ctx, repositoryContext)
	if err != nil {
		return Snapshot{}, err
	}
	if binding.LineageID != request.LineageID || binding.Revision != request.ExpectedRevision ||
		binding.TargetIdentity != request.CorrectionTargetIdentity {
		return Snapshot{}, errors.New("corrected candidate inspection context does not match request") // refusal:by-design operator-knowledge: the opaque locator commits the exact binding and cannot be corrected by caller input
	}
	store, err := CompactAuthoritativeStore(ctx, repo, request.LineageID)
	if err != nil {
		return Snapshot{}, err
	}
	record, err := store.Load()
	if err != nil {
		return Snapshot{}, err
	}
	state := record.State
	if record.Revision != request.ExpectedRevision || state.LineageID != request.LineageID ||
		state.State != StateCorrectionRequired || state.ProposedCorrectionLines == nil || state.CorrectionAttemptConsumed() {
		return Snapshot{}, errors.New("corrected candidate inspection requires current unconsumed correction authority") // refusal:by-design operator-knowledge: only current compact authority can identify an open correction transaction
	}
	projection, err := canonicalProjection(state.InitialSnapshot.Projection)
	if err != nil {
		return Snapshot{}, err
	}
	correction := Snapshot{
		Kind: TargetFixDiff, Projection: projection, UnbornHead: state.CurrentSnapshot.UnbornHead,
		BaseTree: state.CurrentSnapshot.CandidateTree, CandidateTree: request.CorrectionCandidateTree,
		PathsDigest: request.CorrectionPathsDigest, Paths: append([]string(nil), request.CorrectionPaths...),
		IntendedUntracked: append([]string(nil), state.InitialSnapshot.IntendedUntracked...),
		LedgerIDs:         append([]string(nil), state.FixFindingIDs...),
	}
	builder := SnapshotBuilder{Repo: repo}
	correction.IntendedUntrackedProof, err = builder.untrackedProof(ctx, correction.CandidateTree, correction.IntendedUntracked)
	if err != nil {
		return Snapshot{}, err
	}
	correction.Identity = snapshotIdentityForProjection(correction.Kind, correction.Projection, correction.BaseTree,
		correction.CandidateTree, correction.PathsDigest, correction.IntendedUntrackedProof, correction.IntendedUntracked, correction.LedgerIDs)
	if err := builder.ValidateEvidence(ctx, correction); err != nil || correction.Identity != request.CorrectionTargetIdentity {
		return Snapshot{}, errors.New("corrected candidate inspection tree evidence does not match request") // refusal:by-design world-action: missing or mismatched Git objects require a fresh correction capture
	}
	expected, err := targetedValidationRequestForCorrection(state, record.Revision, correction)
	if err != nil || !reflect.DeepEqual(request, expected) {
		return Snapshot{}, errors.New("corrected candidate inspection request does not match authority") // refusal:by-design operator-knowledge: only native authority can derive the exact targeted-validation request
	}
	captured, err := ReadCapturedVerificationEvidence(store.Dir, state.LineageID, record.Revision, correction)
	if err != nil || captured.Record.Outcome != VerificationOutcomePassed {
		return Snapshot{}, errors.New("corrected candidate inspection requires passed repository verification evidence") // refusal:by-design operator-knowledge: STATUS supplies the exact candidate-bound evidence capture transition
	}
	return correction, nil
}
