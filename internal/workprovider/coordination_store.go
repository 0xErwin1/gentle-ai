package workprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	coordinationIndexSchema  = "gentle-ai.coordination-authority-index/v1"
	coordinationRootVersion  = "v1"
	coordinationMaximumBytes = 1 << 20

	coordinationKindForecast    coordinationRecordKind = "forecast"
	coordinationKindDisposition coordinationRecordKind = "disposition"
	coordinationKindExplicitSDD coordinationRecordKind = "explicit_sdd_request"
	coordinationKindRoute       coordinationRecordKind = "route_selection"
)

type coordinationRecordKind string

type CoordinationAuthorityConflictError struct {
	Kind    string
	SlotRef string
}

func (err *CoordinationAuthorityConflictError) Error() string {
	if err == nil {
		return ErrCoordinationAuthorityConflict.Error()
	}
	return fmt.Sprintf(
		"%v: %s slot %s",
		ErrCoordinationAuthorityConflict,
		err.Kind,
		err.SlotRef,
	)
}

func (err *CoordinationAuthorityConflictError) Unwrap() error {
	return ErrCoordinationAuthorityConflict
}

type coordinationRepositoryIdentity struct {
	repositoryRoot     string
	gitCommonDir       string
	gitDir             string
	repositoryIdentity string
	repositoryInfo     fs.FileInfo
	commonInfo         fs.FileInfo
	gitDirInfo         fs.FileInfo
	lease              *reviewtransaction.RepositoryIdentityLease
}

// CoordinationAuthorityStore is bound to one exact Git identity and WorkRun.
// Its root is always derived from Git's common directory; no caller path is
// accepted. Embedding this store in the later terminal RAR adapter supplies the
// forecast/disposition half of workrun.VerificationAuthorityPort.
type CoordinationAuthorityStore struct {
	identity  coordinationRepositoryIdentity
	root      string
	workRunID string
	plans     RARPlanAuthorityPort
}

// OpenCoordinationAuthorityStore is a composition-only Go seam. It performs no
// publication and never creates storage while opening or resolving status.
func OpenCoordinationAuthorityStore(
	ctx context.Context,
	repo string,
	workRunID string,
	plans RARPlanAuthorityPort,
) (*CoordinationAuthorityStore, error) {
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve coordination repository identity: %w",
			err,
		)
	}
	return openCoordinationAuthorityStoreWithLease(
		ctx,
		lease,
		workRunID,
		plans,
	)
}

func openCoordinationAuthorityStoreWithLease(
	ctx context.Context,
	lease *reviewtransaction.RepositoryIdentityLease,
	workRunID string,
	plans RARPlanAuthorityPort,
) (*CoordinationAuthorityStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateCoordinationWorkRunID(workRunID); err != nil {
		return nil, err
	}
	identity, err := coordinationRepositoryIdentityFromLease(ctx, lease)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve coordination repository identity: %w",
			err,
		)
	}
	root := filepath.Join(
		identity.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"coordination-authority",
		coordinationRootVersion,
	)
	return &CoordinationAuthorityStore{
		identity:  identity,
		root:      root,
		workRunID: workRunID,
		plans:     plans,
	}, nil
}

func (store *CoordinationAuthorityStore) PublishForecast(
	ctx context.Context,
	request ForecastAuthorityPublication,
) (ForecastAuthorityRecord, error) {
	if err := store.validateRequestContext(ctx, request.WorkRunID); err != nil {
		return ForecastAuthorityRecord{}, err
	}
	plan, err := store.resolvePlan(ctx, request.PlanRevisionRef)
	if err != nil {
		return ForecastAuthorityRecord{}, err
	}
	record, err := newForecastAuthorityRecord(
		store.identity.repositoryIdentity,
		request,
		plan,
	)
	if err != nil {
		return ForecastAuthorityRecord{}, err
	}
	slotRef, err := forecastAuthoritySlotRef(record)
	if err != nil {
		return ForecastAuthorityRecord{}, err
	}
	payload, err := canonicalCoordinationPayload(record)
	if err != nil {
		return ForecastAuthorityRecord{}, err
	}
	if err := store.publish(
		ctx,
		coordinationKindForecast,
		slotRef,
		record.AuthorityRef,
		payload,
	); err != nil {
		return ForecastAuthorityRecord{}, err
	}
	// Issuance succeeds only if the exact RAR plan remains live after durable
	// publication. A stale orphan is immutable and has no WorkRun authority.
	live, err := store.resolvePlan(ctx, record.PlanRevisionRef)
	if err != nil {
		return ForecastAuthorityRecord{}, err
	}
	if err := validateForecastPlanBinding(record, live); err != nil {
		return ForecastAuthorityRecord{}, err
	}
	return cloneForecastAuthorityRecord(record), nil
}

func (store *CoordinationAuthorityStore) PublishDisposition(
	ctx context.Context,
	request DispositionAuthorityPublication,
) (DispositionAuthorityRecord, error) {
	if err := store.validateRequestContext(ctx, request.WorkRunID); err != nil {
		return DispositionAuthorityRecord{}, err
	}
	forecastRecord, forecastAuthority, err := store.resolveForecastRecord(
		ctx,
		request.Forecast.AvailabilityRef,
	)
	if err != nil {
		return DispositionAuthorityRecord{}, err
	}
	record, err := newDispositionAuthorityRecord(
		store.identity.repositoryIdentity,
		request,
		forecastRecord,
		forecastAuthority,
	)
	if err != nil {
		return DispositionAuthorityRecord{}, err
	}
	slotRef, err := dispositionAuthoritySlotRef(record)
	if err != nil {
		return DispositionAuthorityRecord{}, err
	}
	payload, err := canonicalCoordinationPayload(record)
	if err != nil {
		return DispositionAuthorityRecord{}, err
	}
	if err := store.publish(
		ctx,
		coordinationKindDisposition,
		slotRef,
		record.AuthorityRef,
		payload,
	); err != nil {
		return DispositionAuthorityRecord{}, err
	}
	if _, err := store.ResolveDisposition(ctx, record.AuthorityRef); err != nil {
		return DispositionAuthorityRecord{}, err
	}
	return record, nil
}

func (store *CoordinationAuthorityStore) PublishRouteSelection(
	ctx context.Context,
	request RouteSelectionPublication,
) (RouteSelectionRecord, error) {
	if err := store.validateRequestContext(ctx, request.WorkRunID); err != nil {
		return RouteSelectionRecord{}, err
	}
	record, err := newRouteSelectionRecord(
		store.identity.repositoryIdentity,
		request,
	)
	if err != nil {
		return RouteSelectionRecord{}, err
	}
	slotRef, err := routeSelectionSlotRef(record)
	if err != nil {
		return RouteSelectionRecord{}, err
	}
	payload, err := canonicalCoordinationPayload(record)
	if err != nil {
		return RouteSelectionRecord{}, err
	}
	if err := store.publish(
		ctx,
		coordinationKindRoute,
		slotRef,
		record.AuthorityRef,
		payload,
	); err != nil {
		return RouteSelectionRecord{}, err
	}
	return record, nil
}

func (store *CoordinationAuthorityStore) PublishExplicitSDDRequest(
	ctx context.Context,
	request ExplicitSDDRequestPublication,
) (ExplicitSDDRequestRecord, error) {
	if err := store.validateRequestContext(ctx, request.WorkRunID); err != nil {
		return ExplicitSDDRequestRecord{}, err
	}
	record, err := newExplicitSDDRequestRecord(
		store.identity.repositoryIdentity,
		request,
	)
	if err != nil {
		return ExplicitSDDRequestRecord{}, err
	}
	slotRef, err := explicitSDDRequestSlotRef(record)
	if err != nil {
		return ExplicitSDDRequestRecord{}, err
	}
	payload, err := canonicalCoordinationPayload(record)
	if err != nil {
		return ExplicitSDDRequestRecord{}, err
	}
	if err := store.publish(
		ctx,
		coordinationKindExplicitSDD,
		slotRef,
		record.AuthorityRef,
		payload,
	); err != nil {
		return ExplicitSDDRequestRecord{}, err
	}
	return record, nil
}

func (store *CoordinationAuthorityStore) ResolveForecast(
	ctx context.Context,
	authorityRef string,
) (workrun.VerificationForecastAuthority, error) {
	_, authority, err := store.resolveForecastRecord(ctx, authorityRef)
	return authority, err
}

func (store *CoordinationAuthorityStore) ResolveExplicitSDDRequest(
	ctx context.Context,
	authorityRef string,
) (workrun.ExplicitSDDRequestAuthority, error) {
	if err := store.validateRequestContext(ctx, store.workRunID); err != nil {
		return workrun.ExplicitSDDRequestAuthority{}, err
	}
	payload, err := store.readObject(
		coordinationKindExplicitSDD,
		authorityRef,
	)
	if err != nil {
		return workrun.ExplicitSDDRequestAuthority{}, err
	}
	var record ExplicitSDDRequestRecord
	if err := decodeStrictCoordinationJSON(payload, &record); err != nil {
		return workrun.ExplicitSDDRequestAuthority{}, fmt.Errorf(
			"%w: decode explicit SDD request: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	canonical, err := canonicalCoordinationPayload(record)
	if err != nil || !bytes.Equal(payload, canonical) {
		return workrun.ExplicitSDDRequestAuthority{}, fmt.Errorf(
			"%w: explicit SDD request is not canonical",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	if err := record.Validate(); err != nil {
		return workrun.ExplicitSDDRequestAuthority{}, fmt.Errorf(
			"%w: validate explicit SDD request: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	if record.AuthorityRef != authorityRef {
		return workrun.ExplicitSDDRequestAuthority{}, fmt.Errorf(
			"%w: explicit SDD request lookup binding mismatch",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	if err := store.validateRecordBinding(
		record.RepositoryIdentity,
		record.WorkRunID,
	); err != nil {
		return workrun.ExplicitSDDRequestAuthority{}, err
	}
	slotRef, err := explicitSDDRequestSlotRef(record)
	if err != nil {
		return workrun.ExplicitSDDRequestAuthority{}, err
	}
	if err := store.validatePublishedSlot(
		coordinationKindExplicitSDD,
		slotRef,
		record.AuthorityRef,
	); err != nil {
		return workrun.ExplicitSDDRequestAuthority{}, err
	}
	return record.explicitSDDRequestAuthority(), nil
}

func (store *CoordinationAuthorityStore) ResolveDisposition(
	ctx context.Context,
	authorityRef string,
) (workrun.VerificationDispositionAuthority, error) {
	if err := store.validateRequestContext(ctx, store.workRunID); err != nil {
		return workrun.VerificationDispositionAuthority{}, err
	}
	payload, err := store.readObject(
		coordinationKindDisposition,
		authorityRef,
	)
	if err != nil {
		return workrun.VerificationDispositionAuthority{}, err
	}
	var record DispositionAuthorityRecord
	if err := decodeStrictCoordinationJSON(payload, &record); err != nil {
		return workrun.VerificationDispositionAuthority{}, fmt.Errorf(
			"%w: decode disposition record: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	canonical, err := canonicalCoordinationPayload(record)
	if err != nil || !bytes.Equal(payload, canonical) {
		return workrun.VerificationDispositionAuthority{}, fmt.Errorf(
			"%w: disposition record is not canonical",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	if err := record.Validate(); err != nil {
		return workrun.VerificationDispositionAuthority{}, fmt.Errorf(
			"%w: validate disposition record: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	if record.AuthorityRef != authorityRef {
		return workrun.VerificationDispositionAuthority{}, fmt.Errorf(
			"%w: disposition lookup binding mismatch",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	if err := store.validateRecordBinding(
		record.RepositoryIdentity,
		record.WorkRunID,
	); err != nil {
		return workrun.VerificationDispositionAuthority{}, err
	}
	slotRef, err := dispositionAuthoritySlotRef(record)
	if err != nil {
		return workrun.VerificationDispositionAuthority{}, err
	}
	if err := store.validatePublishedSlot(
		coordinationKindDisposition,
		slotRef,
		record.AuthorityRef,
	); err != nil {
		return workrun.VerificationDispositionAuthority{}, err
	}
	forecastRecord, _, err := store.resolveForecastRecord(
		ctx,
		record.ForecastAuthorityRef,
	)
	if err != nil {
		return workrun.VerificationDispositionAuthority{}, err
	}
	if forecastRecord.HandoffDigest != record.HandoffDigest ||
		forecastRecord.CandidateRef != record.CandidateRef ||
		forecastRecord.PlanRevisionRef != record.PlanRevisionRef {
		return workrun.VerificationDispositionAuthority{}, fmt.Errorf(
			"%w: disposition forecast bindings differ",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	return record.dispositionAuthority(), nil
}

func (store *CoordinationAuthorityStore) ResolveRouteSelection(
	ctx context.Context,
	authorityRef string,
) (workrun.RouteSelectionAuthority, error) {
	if err := store.validateRequestContext(ctx, store.workRunID); err != nil {
		return workrun.RouteSelectionAuthority{}, err
	}
	payload, err := store.readObject(coordinationKindRoute, authorityRef)
	if err != nil {
		return workrun.RouteSelectionAuthority{}, err
	}
	var record RouteSelectionRecord
	if err := decodeStrictCoordinationJSON(payload, &record); err != nil {
		return workrun.RouteSelectionAuthority{}, fmt.Errorf(
			"%w: decode route selection: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	canonical, err := canonicalCoordinationPayload(record)
	if err != nil || !bytes.Equal(payload, canonical) {
		return workrun.RouteSelectionAuthority{}, fmt.Errorf(
			"%w: route selection is not canonical",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	if err := record.Validate(); err != nil {
		return workrun.RouteSelectionAuthority{}, fmt.Errorf(
			"%w: validate route selection: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	if record.AuthorityRef != authorityRef {
		return workrun.RouteSelectionAuthority{}, fmt.Errorf(
			"%w: route selection lookup binding mismatch",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	if err := store.validateRecordBinding(
		record.RepositoryIdentity,
		record.WorkRunID,
	); err != nil {
		return workrun.RouteSelectionAuthority{}, err
	}
	slotRef, err := routeSelectionSlotRef(record)
	if err != nil {
		return workrun.RouteSelectionAuthority{}, err
	}
	if err := store.validatePublishedSlot(
		coordinationKindRoute,
		slotRef,
		record.AuthorityRef,
	); err != nil {
		return workrun.RouteSelectionAuthority{}, err
	}
	return record.routeAuthority(), nil
}

func (store *CoordinationAuthorityStore) resolveForecastRecord(
	ctx context.Context,
	authorityRef string,
) (
	ForecastAuthorityRecord,
	workrun.VerificationForecastAuthority,
	error,
) {
	if err := store.validateRequestContext(ctx, store.workRunID); err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			err
	}
	payload, err := store.readObject(coordinationKindForecast, authorityRef)
	if err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			err
	}
	var record ForecastAuthorityRecord
	if err := decodeStrictCoordinationJSON(payload, &record); err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			fmt.Errorf(
				"%w: decode forecast record: %v",
				ErrCoordinationAuthorityCorrupt,
				err,
			)
	}
	canonical, err := canonicalCoordinationPayload(record)
	if err != nil || !bytes.Equal(payload, canonical) {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			fmt.Errorf(
				"%w: forecast record is not canonical",
				ErrCoordinationAuthorityCorrupt,
			)
	}
	if err := record.Validate(); err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			fmt.Errorf(
				"%w: validate forecast record: %v",
				ErrCoordinationAuthorityCorrupt,
				err,
			)
	}
	if record.AuthorityRef != authorityRef {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			fmt.Errorf(
				"%w: forecast lookup binding mismatch",
				ErrCoordinationAuthorityCorrupt,
			)
	}
	if err := store.validateRecordBinding(
		record.RepositoryIdentity,
		record.WorkRunID,
	); err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			err
	}
	slotRef, err := forecastAuthoritySlotRef(record)
	if err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			err
	}
	if err := store.validatePublishedSlot(
		coordinationKindForecast,
		slotRef,
		record.AuthorityRef,
	); err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			err
	}
	plan, err := store.resolvePlan(ctx, record.PlanRevisionRef)
	if err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			err
	}
	if err := validateForecastPlanBinding(record, plan); err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			err
	}
	authority := workrun.VerificationForecastAuthority{
		AvailabilityRef: record.AuthorityRef,
		Applicability:   plan.Applicability,
		Registry:        plan.Registry,
		Plan:            plan.Plan,
		PlanRevisionRef: plan.AuthorityRef,
		Availability:    record.Availability,
		DiagnosticRefs:  cloneCoordinationStrings(record.DiagnosticRefs),
	}
	if err := authority.Validate(); err != nil {
		return ForecastAuthorityRecord{},
			workrun.VerificationForecastAuthority{},
			fmt.Errorf(
				"%w: project forecast authority: %v",
				ErrCoordinationAuthorityCorrupt,
				err,
			)
	}
	return cloneForecastAuthorityRecord(record), authority, nil
}

func (store *CoordinationAuthorityStore) resolvePlan(
	ctx context.Context,
	planRevisionRef string,
) (reviewtransaction.RARPlanAuthority, error) {
	if store == nil || store.plans == nil {
		return reviewtransaction.RARPlanAuthority{},
			ErrPlanAuthorityUnavailable
	}
	plan, err := store.plans.ResolvePlan(ctx, planRevisionRef)
	if err != nil {
		return reviewtransaction.RARPlanAuthority{}, fmt.Errorf(
			"%w: resolve live RAR plan: %v",
			ErrCoordinationAuthorityStale,
			err,
		)
	}
	if err := validateResolvedPlanAuthority(
		plan,
		store.identity.repositoryIdentity,
		planRevisionRef,
	); err != nil {
		return reviewtransaction.RARPlanAuthority{}, fmt.Errorf(
			"%w: %v",
			ErrCoordinationAuthorityStale,
			err,
		)
	}
	return plan, nil
}

func validateForecastPlanBinding(
	record ForecastAuthorityRecord,
	plan reviewtransaction.RARPlanAuthority,
) error {
	if record.RepositoryIdentity != plan.RepositoryIdentity ||
		record.PlanRevisionRef != plan.AuthorityRef ||
		record.CandidateRef != plan.Subject.SnapshotIdentity ||
		record.ApplicabilityDigest != plan.Applicability.Digest ||
		record.PlanRegistryDigest != plan.Registry.Digest ||
		record.PlanDigest != plan.Plan.Digest {
		return fmt.Errorf(
			"%w: forecast differs from live RAR plan authority",
			ErrCoordinationAuthorityStale,
		)
	}
	if err := validateForecastAvailability(
		record.Availability,
		record.DiagnosticRefs,
		plan.Plan,
	); err != nil {
		return fmt.Errorf(
			"%w: forecast availability no longer matches plan: %v",
			ErrCoordinationAuthorityStale,
			err,
		)
	}
	return nil
}

func (store *CoordinationAuthorityStore) validateRequestContext(
	ctx context.Context,
	workRunID string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil {
		return errors.New("coordination authority store is not initialized")
	}
	if workRunID != store.workRunID {
		return ErrCoordinationAuthorityCrossRun
	}
	if err := store.validateIdentity(ctx); err != nil {
		return err
	}
	return nil
}

func (store *CoordinationAuthorityStore) validateRecordBinding(
	repositoryIdentity string,
	workRunID string,
) error {
	if workRunID != store.workRunID {
		return ErrCoordinationAuthorityCrossRun
	}
	if repositoryIdentity != store.identity.repositoryIdentity {
		return fmt.Errorf(
			"%w: repository identity differs",
			ErrCoordinationAuthorityStale,
		)
	}
	return nil
}

func (store *CoordinationAuthorityStore) validateIdentity(
	ctx context.Context,
) error {
	if store == nil ||
		store.identity.repositoryRoot == "" ||
		store.identity.gitCommonDir == "" ||
		store.identity.gitDir == "" ||
		store.identity.lease == nil {
		return errors.New("coordination authority store is not initialized")
	}
	if err := store.identity.lease.Validate(ctx); err != nil {
		return errors.Join(
			ErrCoordinationAuthorityStale,
			fmt.Errorf("Git repository identity changed: %w", err),
		)
	}
	live := store.identity.lease.Identity()
	if live.RepositoryRef != store.identity.repositoryIdentity ||
		live.RepositoryRoot != store.identity.repositoryRoot ||
		live.GitCommonDir != store.identity.gitCommonDir ||
		live.GitDir != store.identity.gitDir {
		return fmt.Errorf(
			"%w: Git repository identity changed",
			ErrCoordinationAuthorityStale,
		)
	}
	wantRoot := filepath.Join(
		live.GitCommonDir,
		"gentle-ai",
		"work-provider",
		"coordination-authority",
		coordinationRootVersion,
	)
	if filepath.Clean(store.root) != filepath.Clean(wantRoot) {
		return errors.New(
			"coordination authority root is not derived from Git common-dir",
		)
	}
	return nil
}

type coordinationIndex struct {
	Schema             string                 `json:"schema"`
	Kind               coordinationRecordKind `json:"kind"`
	SlotRef            string                 `json:"slot_ref"`
	AuthorityRef       string                 `json:"authority_ref"`
	RepositoryIdentity string                 `json:"repository_identity"`
	WorkRunID          string                 `json:"work_run_id"`
}

func (index coordinationIndex) Validate() error {
	if index.Schema != coordinationIndexSchema ||
		!validCoordinationKind(index.Kind) ||
		!validCoordinationRef(index.SlotRef) ||
		!validCoordinationRef(index.AuthorityRef) ||
		!validCoordinationRef(index.RepositoryIdentity) {
		return errors.New("coordination authority index is invalid")
	}
	return validateCoordinationWorkRunID(index.WorkRunID)
}

func forecastAuthoritySlotRef(
	record ForecastAuthorityRecord,
) (string, error) {
	return coordinationDigest(
		"gentle-ai.forecast-authority-slot/v1",
		struct {
			RepositoryIdentity string `json:"repository_identity"`
			WorkRunID          string `json:"work_run_id"`
			HandoffDigest      string `json:"handoff_digest"`
			CandidateRef       string `json:"candidate_ref"`
			PlanRevisionRef    string `json:"plan_revision_ref"`
		}{
			RepositoryIdentity: record.RepositoryIdentity,
			WorkRunID:          record.WorkRunID,
			HandoffDigest:      record.HandoffDigest,
			CandidateRef:       record.CandidateRef,
			PlanRevisionRef:    record.PlanRevisionRef,
		},
	)
}

func dispositionAuthoritySlotRef(
	record DispositionAuthorityRecord,
) (string, error) {
	return coordinationDigest(
		"gentle-ai.disposition-authority-slot/v1",
		struct {
			RepositoryIdentity string `json:"repository_identity"`
			WorkRunID          string `json:"work_run_id"`
			ForecastDigest     string `json:"forecast_digest"`
		}{
			RepositoryIdentity: record.RepositoryIdentity,
			WorkRunID:          record.WorkRunID,
			ForecastDigest:     record.ForecastDigest,
		},
	)
}

func explicitSDDRequestSlotRef(
	record ExplicitSDDRequestRecord,
) (string, error) {
	return coordinationDigest(
		"gentle-ai.explicit-sdd-request-slot/v1",
		struct {
			RepositoryIdentity string `json:"repository_identity"`
			WorkRunID          string `json:"work_run_id"`
		}{
			RepositoryIdentity: record.RepositoryIdentity,
			WorkRunID:          record.WorkRunID,
		},
	)
}

func routeSelectionSlotRef(
	record RouteSelectionRecord,
) (string, error) {
	return coordinationDigest(
		"gentle-ai.route-selection-slot/v1",
		struct {
			RepositoryIdentity    string `json:"repository_identity"`
			WorkRunID             string `json:"work_run_id"`
			PendingDecisionDigest string `json:"pending_decision_digest"`
		}{
			RepositoryIdentity:    record.RepositoryIdentity,
			WorkRunID:             record.WorkRunID,
			PendingDecisionDigest: record.PendingDecisionDigest,
		},
	)
}

// validatePublishedSlot distinguishes an authorized publication from a
// harmless content-addressed orphan left by a crash before index publication.
func (store *CoordinationAuthorityStore) validatePublishedSlot(
	kind coordinationRecordKind,
	slotRef string,
	authorityRef string,
) error {
	if err := ensurePrivateCoordinationDirectoryTree(
		store.root,
		store.slotsRoot(kind),
		false,
	); err != nil {
		return fmt.Errorf(
			"%w: publication slot is unavailable: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	payload, err := readPrivateCoordinationFile(
		store.slotPath(kind, slotRef),
	)
	if err != nil {
		return fmt.Errorf(
			"%w: read publication slot: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	var index coordinationIndex
	if err := decodeStrictCoordinationJSON(payload, &index); err != nil {
		return fmt.Errorf(
			"%w: decode publication slot: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	canonical, err := canonicalCoordinationPayload(index)
	if err != nil ||
		!bytes.Equal(payload, canonical) ||
		index.Validate() != nil ||
		index.Kind != kind ||
		index.SlotRef != slotRef ||
		index.AuthorityRef != authorityRef ||
		index.RepositoryIdentity != store.identity.repositoryIdentity ||
		index.WorkRunID != store.workRunID {
		return fmt.Errorf(
			"%w: publication slot binding is invalid",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	return nil
}

func (store *CoordinationAuthorityStore) publish(
	ctx context.Context,
	kind coordinationRecordKind,
	slotRef string,
	authorityRef string,
	payload []byte,
) error {
	if err := store.validateRequestContext(ctx, store.workRunID); err != nil {
		return err
	}
	if !validCoordinationKind(kind) ||
		!validCoordinationRef(slotRef) ||
		!validCoordinationRef(authorityRef) {
		return errors.New("invalid coordination authority publication")
	}
	if err := ensureCoordinationRepositoryRoot(
		store.identity.gitCommonDir,
		store.root,
		true,
	); err != nil {
		return err
	}
	for _, dir := range []string{
		store.objectsRoot(kind),
		store.slotsRoot(kind),
	} {
		if err := ensurePrivateCoordinationDirectoryTree(
			store.root,
			dir,
			true,
		); err != nil {
			return err
		}
	}
	index := coordinationIndex{
		Schema:             coordinationIndexSchema,
		Kind:               kind,
		SlotRef:            slotRef,
		AuthorityRef:       authorityRef,
		RepositoryIdentity: store.identity.repositoryIdentity,
		WorkRunID:          store.workRunID,
	}
	indexPayload, err := canonicalCoordinationPayload(index)
	if err != nil {
		return err
	}
	slotPath := store.slotPath(kind, slotRef)
	existingIndex, err := readPrivateCoordinationFile(slotPath)
	if err == nil {
		if !bytes.Equal(existingIndex, indexPayload) {
			return &CoordinationAuthorityConflictError{
				Kind:    string(kind),
				SlotRef: slotRef,
			}
		}
		existingObject, readErr := readPrivateCoordinationFile(
			store.objectPath(kind, authorityRef),
		)
		if readErr != nil || !bytes.Equal(existingObject, payload) {
			return fmt.Errorf(
				"%w: exact replay object is absent or differs",
				ErrCoordinationAuthorityCorrupt,
			)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := publishPrivateCoordinationImmutable(
		store.objectPath(kind, authorityRef),
		payload,
	); err != nil {
		return err
	}
	if err := publishPrivateCoordinationImmutable(slotPath, indexPayload); err != nil {
		if errors.Is(err, ErrCoordinationAuthorityConflict) {
			existing, readErr := readPrivateCoordinationFile(slotPath)
			if readErr == nil && bytes.Equal(existing, indexPayload) {
				return nil
			}
			return &CoordinationAuthorityConflictError{
				Kind:    string(kind),
				SlotRef: slotRef,
			}
		}
		return err
	}
	return nil
}

func (store *CoordinationAuthorityStore) readObject(
	kind coordinationRecordKind,
	authorityRef string,
) ([]byte, error) {
	if !validCoordinationKind(kind) || !validCoordinationRef(authorityRef) {
		return nil, errors.New("invalid coordination authority lookup")
	}
	if err := ensureCoordinationRepositoryRoot(
		store.identity.gitCommonDir,
		store.root,
		false,
	); err != nil {
		return nil, err
	}
	if err := ensurePrivateCoordinationDirectoryTree(
		store.root,
		store.objectsRoot(kind),
		false,
	); err != nil {
		return nil, err
	}
	payload, err := readPrivateCoordinationFile(
		store.objectPath(kind, authorityRef),
	)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (store *CoordinationAuthorityStore) objectsRoot(
	kind coordinationRecordKind,
) string {
	return filepath.Join(store.root, "objects", string(kind))
}

func (store *CoordinationAuthorityStore) slotsRoot(
	kind coordinationRecordKind,
) string {
	return filepath.Join(store.root, "slots", string(kind))
}

func (store *CoordinationAuthorityStore) objectPath(
	kind coordinationRecordKind,
	authorityRef string,
) string {
	return filepath.Join(
		store.objectsRoot(kind),
		strings.TrimPrefix(authorityRef, "sha256:")+".json",
	)
}

func (store *CoordinationAuthorityStore) slotPath(
	kind coordinationRecordKind,
	slotRef string,
) string {
	return filepath.Join(
		store.slotsRoot(kind),
		strings.TrimPrefix(slotRef, "sha256:")+".json",
	)
}

func validCoordinationKind(kind coordinationRecordKind) bool {
	switch kind {
	case coordinationKindForecast, coordinationKindDisposition,
		coordinationKindExplicitSDD, coordinationKindRoute:
		return true
	default:
		return false
	}
}

func resolveCoordinationRepositoryIdentity(
	ctx context.Context,
	repo string,
) (coordinationRepositoryIdentity, error) {
	if err := ctx.Err(); err != nil {
		return coordinationRepositoryIdentity{}, err
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return coordinationRepositoryIdentity{}, err
	}
	return coordinationRepositoryIdentityFromLease(ctx, lease)
}

func coordinationRepositoryIdentityFromLease(
	ctx context.Context,
	lease *reviewtransaction.RepositoryIdentityLease,
) (coordinationRepositoryIdentity, error) {
	if lease == nil {
		return coordinationRepositoryIdentity{}, errors.New(
			"coordination repository identity lease is required",
		)
	}
	if err := lease.Validate(ctx); err != nil {
		return coordinationRepositoryIdentity{}, err
	}
	value := lease.Identity()
	paths := [...]string{
		value.RepositoryRoot,
		value.GitCommonDir,
		value.GitDir,
	}
	infos := make([]fs.FileInfo, len(paths))
	for index, path := range paths {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.IsDir() ||
			coordinationPathUnsafe(path, info) ||
			!coordinationSharedDirectorySafe(path, info) {
			if statErr != nil {
				return coordinationRepositoryIdentity{}, statErr
			}
			return coordinationRepositoryIdentity{}, errors.New(
				"coordination Git identity contains an unsafe directory",
			)
		}
		infos[index] = info
	}
	if err := lease.Validate(ctx); err != nil {
		return coordinationRepositoryIdentity{}, err
	}
	identity := coordinationRepositoryIdentity{
		repositoryRoot: paths[0],
		gitCommonDir:   paths[1],
		gitDir:         paths[2],
		repositoryInfo: infos[0],
		commonInfo:     infos[1],
		gitDirInfo:     infos[2],
		lease:          lease,
	}
	identity.repositoryIdentity = coordinationRepositoryIdentityDigest(identity)
	if identity.repositoryIdentity != value.RepositoryRef {
		return coordinationRepositoryIdentity{}, errors.New(
			"coordination repository identity differs from shared lease",
		)
	}
	return identity, nil
}

func coordinationRepositoryIdentityDigest(
	identity coordinationRepositoryIdentity,
) string {
	payload, _ := json.Marshal(struct {
		RepositoryRoot string `json:"repository_root"`
		GitCommonDir   string `json:"git_common_dir"`
		GitDir         string `json:"git_dir"`
	}{
		RepositoryRoot: filepath.Clean(identity.repositoryRoot),
		GitCommonDir:   filepath.Clean(identity.gitCommonDir),
		GitDir:         filepath.Clean(identity.gitDir),
	})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sameCoordinationDirectory(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil &&
		rightErr == nil &&
		leftInfo.IsDir() &&
		rightInfo.IsDir() &&
		os.SameFile(leftInfo, rightInfo)
}

func cloneForecastAuthorityRecord(
	record ForecastAuthorityRecord,
) ForecastAuthorityRecord {
	record.DiagnosticRefs = cloneCoordinationStrings(record.DiagnosticRefs)
	return record
}
