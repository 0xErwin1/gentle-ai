package sddstatus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	SDDWorkRunBindingSchema                    = "gentle-ai.sdd-work-run-binding/v1"
	CommonVerificationReservationBindingSchema = "gentle-ai.sdd-common-verification-reservation-binding/v1"
)

// SDDWorkRunBinding single-assigns one canonical SDD change/runRef to the
// WorkRun and accepted SDD-route decision that selected it.
type SDDWorkRunBinding struct {
	Schema             string `json:"schema"`
	BindingRef         string `json:"binding_ref"`
	RunRef             string `json:"run_ref"`
	WorkRunID          string `json:"work_run_id"`
	RouteAcceptanceRef string `json:"route_acceptance_ref"`
}

func (binding SDDWorkRunBinding) Validate() error {
	if binding.Schema != SDDWorkRunBindingSchema ||
		!runtimeRevisionPattern.MatchString(binding.BindingRef) ||
		!validReviewBindingChange(binding.RunRef) ||
		!runtimeRequestIDPattern.MatchString(binding.WorkRunID) ||
		!runtimeRevisionPattern.MatchString(binding.RouteAcceptanceRef) {
		return errors.New("invalid SDD WorkRun binding")
	}
	if binding.BindingRef != sddWorkRunBindingRef(binding) {
		return errors.New("SDD WorkRun binding ref does not match its canonical content")
	}
	return nil
}

// CommonVerificationReservationBinding binds a WorkRun reservation to the
// exact active SDD runtime attempt selected under the ledger lock. Callers
// cannot supply or choose the objective, generation, or ordinal.
type CommonVerificationReservationBinding struct {
	Schema              string `json:"schema"`
	BindingRef          string `json:"binding_ref"`
	RunRef              string `json:"run_ref"`
	WorkRunID           string `json:"work_run_id"`
	WorkRunRevision     string `json:"work_run_revision"`
	ReservationRef      string `json:"reservation_ref"`
	ActionTicketRef     string `json:"action_ticket_ref"`
	ObjectiveID         string `json:"objective_id"`
	ObjectiveGeneration int    `json:"objective_generation"`
	AttemptOrdinal      int    `json:"attempt_ordinal"`
}

func (binding CommonVerificationReservationBinding) Validate() error {
	if binding.Schema != CommonVerificationReservationBindingSchema ||
		!runtimeRevisionPattern.MatchString(binding.BindingRef) ||
		!validReviewBindingChange(binding.RunRef) ||
		!runtimeRequestIDPattern.MatchString(binding.WorkRunID) ||
		!runtimeRevisionPattern.MatchString(binding.WorkRunRevision) ||
		!runtimeRevisionPattern.MatchString(binding.ReservationRef) ||
		!runtimeRevisionPattern.MatchString(binding.ActionTicketRef) ||
		!runtimeRevisionPattern.MatchString(binding.ObjectiveID) ||
		binding.ObjectiveGeneration < 1 ||
		binding.AttemptOrdinal < 1 {
		return errors.New("invalid SDD common verification reservation binding")
	}
	if binding.BindingRef != commonVerificationReservationBindingRef(binding) {
		return errors.New("SDD reservation binding ref does not match its canonical content")
	}
	return nil
}

type bindWorkRunRequest struct {
	WorkRunID          string `json:"work_run_id"`
	RouteAcceptanceRef string `json:"route_acceptance_ref"`
}

type bindCommonVerificationReservationRequest struct {
	WorkRunID       string `json:"work_run_id"`
	WorkRunRevision string `json:"work_run_revision"`
	ReservationRef  string `json:"reservation_ref"`
	ActionTicketRef string `json:"action_ticket_ref"`
}

// BindWorkRun append-only binds this existing SDD change/runRef to one WorkRun.
// An exact retry is read-only. A different WorkRun or route acceptance can
// never replace the first binding.
func (store RuntimeStore) BindWorkRun(
	ctx context.Context,
	workRunID string,
	routeAcceptanceRef string,
) (SDDWorkRunBinding, error) {
	request := bindWorkRunRequest{
		WorkRunID:          workRunID,
		RouteAcceptanceRef: routeAcceptanceRef,
	}
	if err := validateBindWorkRunRequest(request); err != nil {
		return SDDWorkRunBinding{}, err
	}
	if err := store.validateWorkRunBindingStore(ctx); err != nil {
		return SDDWorkRunBinding{}, err
	}
	if err := ctx.Err(); err != nil {
		return SDDWorkRunBinding{}, err
	}

	preflight, err := store.load()
	if err != nil {
		return SDDWorkRunBinding{}, err
	}
	if err := store.requireExistingSDDAuthority(ctx, preflight); err != nil {
		return SDDWorkRunBinding{}, err
	}
	if err := store.ensureDirectories(); err != nil {
		return SDDWorkRunBinding{}, err
	}
	lock, err := reviewtransaction.AcquireAuthorityFileLock(filepath.Join(store.Dir, "LOCK"))
	if err != nil {
		return SDDWorkRunBinding{}, mapRuntimeLockError(err)
	}
	defer lock.Release()

	replay, err := store.load()
	if err != nil {
		return SDDWorkRunBinding{}, err
	}
	if err := store.requireExistingSDDAuthority(ctx, replay); err != nil {
		return SDDWorkRunBinding{}, err
	}
	candidate := newSDDWorkRunBinding(store.Change, request)
	if replay.Status.WorkRunBinding != nil {
		if *replay.Status.WorkRunBinding != candidate {
			return SDDWorkRunBinding{}, ErrSDDWorkRunBindingConflict
		}
		if err := store.syncReplay(); err != nil {
			return SDDWorkRunBinding{}, &RuntimePublicationError{
				Revision: replay.Status.Revision, Committed: true, Cause: err,
			}
		}
		return candidate, nil
	}

	requestID := "bind-workrun-" + strings.TrimPrefix(candidate.BindingRef, "sha256:")
	requestDigest := runtimeValueHash("gentle-ai.sdd-bind-work-run-request/v1", request)
	if receipt, exists := replay.Requests[requestID]; exists {
		if receipt.Digest != requestDigest {
			return SDDWorkRunBinding{}, ErrRuntimeRequestConflict
		}
		return SDDWorkRunBinding{}, errors.New("replayed WorkRun bind request has no binding projection")
	}
	record := runtimeRecord{
		Schema: runtimeRecordSchema, Change: store.Change,
		PreviousRevision: replay.Status.Revision,
		Operation:        runtimeOperationBindWorkRun,
		RequestID:        requestID,
		RequestDigest:    requestDigest,
		WorkRunBinding:   &candidate,
	}
	if err := validateRuntimeRecordShape(record); err != nil {
		return SDDWorkRunBinding{}, err
	}
	status, err := store.commitRecordLocked(record)
	if err != nil {
		return SDDWorkRunBinding{}, err
	}
	if status.WorkRunBinding == nil || *status.WorkRunBinding != candidate {
		return SDDWorkRunBinding{}, errors.New("committed SDD WorkRun binding is missing")
	}
	return candidate, nil
}

// ResolveWorkRunBinding resolves the immutable native ledger projection. It
// does not infer a WorkRun from SDD artifacts or accept caller binding fields.
func (store RuntimeStore) ResolveWorkRunBinding(
	ctx context.Context,
) (SDDWorkRunBinding, error) {
	if err := store.validateWorkRunBindingStore(ctx); err != nil {
		return SDDWorkRunBinding{}, err
	}
	if err := ctx.Err(); err != nil {
		return SDDWorkRunBinding{}, err
	}
	replay, err := store.load()
	if err != nil {
		return SDDWorkRunBinding{}, err
	}
	if replay.Status.WorkRunBinding == nil {
		return SDDWorkRunBinding{}, ErrSDDWorkRunBindingNotFound
	}
	binding := *replay.Status.WorkRunBinding
	if err := binding.Validate(); err != nil {
		return SDDWorkRunBinding{}, err
	}
	if binding.RunRef != store.Change {
		return SDDWorkRunBinding{}, errors.New("SDD WorkRun binding run does not match store")
	}
	return binding, nil
}

// BindCommonVerificationReservation appends one exact WorkRun reservation to
// the currently active SDD attempt. The ledger derives objective identity,
// generation, and ordinal while holding its existing authority lock.
func (store RuntimeStore) BindCommonVerificationReservation(
	ctx context.Context,
	workRunID string,
	workRunRevision string,
	reservationRef string,
	actionTicketRef string,
) (CommonVerificationReservationBinding, error) {
	request := bindCommonVerificationReservationRequest{
		WorkRunID: workRunID, WorkRunRevision: workRunRevision,
		ReservationRef: reservationRef, ActionTicketRef: actionTicketRef,
	}
	if err := validateBindCommonVerificationReservationRequest(request); err != nil {
		return CommonVerificationReservationBinding{}, err
	}
	if err := store.validateWorkRunBindingStore(ctx); err != nil {
		return CommonVerificationReservationBinding{}, err
	}
	if err := ctx.Err(); err != nil {
		return CommonVerificationReservationBinding{}, err
	}

	preflight, err := store.load()
	if err != nil {
		return CommonVerificationReservationBinding{}, err
	}
	_, exactReplay, conflict := matchExistingReservation(preflight.Status, request)
	if conflict != nil {
		return CommonVerificationReservationBinding{}, conflict
	}
	if !exactReplay {
		if err := validateReservationPreconditions(preflight.Status, request); err != nil {
			return CommonVerificationReservationBinding{}, err
		}
	}
	if err := store.ensureDirectories(); err != nil {
		return CommonVerificationReservationBinding{}, err
	}
	lock, err := reviewtransaction.AcquireAuthorityFileLock(filepath.Join(store.Dir, "LOCK"))
	if err != nil {
		return CommonVerificationReservationBinding{}, mapRuntimeLockError(err)
	}
	defer lock.Release()

	replay, err := store.load()
	if err != nil {
		return CommonVerificationReservationBinding{}, err
	}
	if exact, ok, conflict := matchExistingReservation(replay.Status, request); ok || conflict != nil {
		if conflict != nil {
			return CommonVerificationReservationBinding{}, conflict
		}
		if err := store.syncReplay(); err != nil {
			return CommonVerificationReservationBinding{}, &RuntimePublicationError{
				Revision: replay.Status.Revision, Committed: true, Cause: err,
			}
		}
		return exact, nil
	}
	if err := validateReservationPreconditions(replay.Status, request); err != nil {
		return CommonVerificationReservationBinding{}, err
	}
	active := replay.Status.ActiveAttempt
	candidate := newCommonVerificationReservationBinding(store.Change, request, *active)
	requestID := "bind-reservation-" + strings.TrimPrefix(request.ReservationRef, "sha256:")
	requestDigest := runtimeValueHash(
		"gentle-ai.sdd-bind-common-verification-reservation-request/v1",
		request,
	)
	if receipt, exists := replay.Requests[requestID]; exists {
		if receipt.Digest != requestDigest {
			return CommonVerificationReservationBinding{}, ErrRuntimeRequestConflict
		}
		return CommonVerificationReservationBinding{}, errors.New(
			"replayed SDD reservation request has no binding projection",
		)
	}
	record := runtimeRecord{
		Schema: runtimeRecordSchema, Change: store.Change,
		PreviousRevision:   replay.Status.Revision,
		Operation:          runtimeOperationBindReservation,
		RequestID:          requestID,
		RequestDigest:      requestDigest,
		ReservationBinding: &candidate,
	}
	if err := validateRuntimeRecordShape(record); err != nil {
		return CommonVerificationReservationBinding{}, err
	}
	status, err := store.commitRecordLocked(record)
	if err != nil {
		return CommonVerificationReservationBinding{}, err
	}
	exact, ok, conflict := matchExistingReservation(status, request)
	if conflict != nil {
		return CommonVerificationReservationBinding{}, conflict
	}
	if !ok || exact != candidate {
		return CommonVerificationReservationBinding{}, errors.New(
			"committed SDD reservation binding is missing",
		)
	}
	return candidate, nil
}

func validateBindWorkRunRequest(request bindWorkRunRequest) error {
	if !runtimeRequestIDPattern.MatchString(request.WorkRunID) ||
		!runtimeRevisionPattern.MatchString(request.RouteAcceptanceRef) {
		return errors.New("invalid SDD WorkRun binding request")
	}
	return nil
}

func validateBindCommonVerificationReservationRequest(
	request bindCommonVerificationReservationRequest,
) error {
	if !runtimeRequestIDPattern.MatchString(request.WorkRunID) ||
		!runtimeRevisionPattern.MatchString(request.WorkRunRevision) ||
		!runtimeRevisionPattern.MatchString(request.ReservationRef) ||
		!runtimeRevisionPattern.MatchString(request.ActionTicketRef) {
		return errors.New("invalid SDD common verification reservation request")
	}
	return nil
}

func newSDDWorkRunBinding(
	runRef string,
	request bindWorkRunRequest,
) SDDWorkRunBinding {
	binding := SDDWorkRunBinding{
		Schema: SDDWorkRunBindingSchema, RunRef: runRef,
		WorkRunID: request.WorkRunID, RouteAcceptanceRef: request.RouteAcceptanceRef,
	}
	binding.BindingRef = sddWorkRunBindingRef(binding)
	return binding
}

func newCommonVerificationReservationBinding(
	runRef string,
	request bindCommonVerificationReservationRequest,
	active RuntimeAttempt,
) CommonVerificationReservationBinding {
	binding := CommonVerificationReservationBinding{
		Schema: CommonVerificationReservationBindingSchema, RunRef: runRef,
		WorkRunID: request.WorkRunID, WorkRunRevision: request.WorkRunRevision,
		ReservationRef: request.ReservationRef, ActionTicketRef: request.ActionTicketRef,
		ObjectiveID: active.ObjectiveID, ObjectiveGeneration: active.ObjectiveGeneration,
		AttemptOrdinal: active.Ordinal,
	}
	binding.BindingRef = commonVerificationReservationBindingRef(binding)
	return binding
}

func sddWorkRunBindingRef(binding SDDWorkRunBinding) string {
	binding.BindingRef = ""
	return runtimeValueHash(SDDWorkRunBindingSchema, binding)
}

func commonVerificationReservationBindingRef(
	binding CommonVerificationReservationBinding,
) string {
	binding.BindingRef = ""
	return runtimeValueHash(CommonVerificationReservationBindingSchema, binding)
}

func validateReservationPreconditions(
	status RuntimeStatus,
	request bindCommonVerificationReservationRequest,
) error {
	if status.WorkRunBinding == nil {
		return ErrSDDWorkRunBindingNotFound
	}
	if status.WorkRunBinding.WorkRunID != request.WorkRunID {
		return ErrSDDWorkRunBindingConflict
	}
	if status.ActiveAttempt == nil || status.Objective == nil {
		return ErrRuntimeNoActiveAttempt
	}
	active := status.ActiveAttempt
	if active.ObjectiveID != status.Objective.ID ||
		active.ObjectiveGeneration != status.Objective.Generation ||
		active.Ordinal < 1 ||
		active.Outcome != AttemptRunning {
		return errors.New("active SDD attempt does not match its exact objective authority")
	}
	return nil
}

func matchExistingReservation(
	status RuntimeStatus,
	request bindCommonVerificationReservationRequest,
) (CommonVerificationReservationBinding, bool, error) {
	for _, existing := range status.VerificationReservations {
		if existing.ReservationRef == request.ReservationRef {
			if existing.WorkRunID == request.WorkRunID &&
				existing.WorkRunRevision == request.WorkRunRevision &&
				existing.ActionTicketRef == request.ActionTicketRef {
				return existing, true, nil
			}
			return CommonVerificationReservationBinding{}, false,
				ErrSDDReservationBindingConflict
		}
		if existing.ActionTicketRef == request.ActionTicketRef ||
			existing.WorkRunRevision == request.WorkRunRevision {
			return CommonVerificationReservationBinding{}, false,
				ErrSDDReservationBindingConflict
		}
	}
	return CommonVerificationReservationBinding{}, false, nil
}

func (store RuntimeStore) validateWorkRunBindingStore(ctx context.Context) error {
	if !validReviewBindingChange(store.Change) ||
		store.Repo == "" ||
		store.Workspace == "" ||
		store.commonDir == "" ||
		!filepath.IsAbs(store.commonDir) {
		return errors.New("SDD runtime store is not initialized")
	}
	canonical, err := OpenRuntimeStore(ctx, store.Workspace, store.Change)
	if err != nil {
		return fmt.Errorf("revalidate SDD runtime store: %w", err)
	}
	if filepath.Clean(store.Dir) != filepath.Clean(canonical.Dir) ||
		filepath.Clean(store.Repo) != filepath.Clean(canonical.Repo) ||
		filepath.Clean(store.Workspace) != filepath.Clean(canonical.Workspace) ||
		filepath.Clean(store.commonDir) != filepath.Clean(canonical.commonDir) {
		return errors.New("SDD runtime store does not match its canonical Git authority")
	}
	return nil
}

func (store RuntimeStore) requireExistingSDDAuthority(
	ctx context.Context,
	replay runtimeReplay,
) error {
	if replay.Status.Revision != "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	exists, err := store.hasLiveSDDChangeAuthority()
	if err != nil {
		return err
	}
	if !exists {
		return ErrSDDRunAuthorityNotFound
	}
	return ctx.Err()
}

func (store RuntimeStore) hasLiveSDDChangeAuthority() (bool, error) {
	changeRoot := filepath.Join(store.Repo, "openspec", "changes", store.Change)
	info, err := os.Lstat(changeRoot)
	if err == nil {
		return info.IsDir() && info.Mode()&os.ModeSymlink == 0, nil
	}
	if !os.IsNotExist(err) {
		return false, err
	}
	if !shouldTryEngram(store.Repo) {
		return false, nil
	}
	observations, err := engramExport(store.Repo)
	if err != nil {
		return false, err
	}
	project := inferEngramProject(store.Repo)
	return len(engramArtifactsForChange(observations, project, store.Change)) > 0, nil
}

func mapRuntimeLockError(err error) error {
	if errors.Is(err, reviewtransaction.ErrConcurrentUpdate) {
		return fmt.Errorf("%w: %v", ErrRuntimeConcurrentUpdate, err)
	}
	return err
}
