package workprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/mutationintegrity"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// MutationCompletionWorkRunAuthority projects immutable MMI completion
// records into WorkRun's narrow owner port. It neither rebuilds snapshots nor
// accepts caller-authored hashes: Store.Read validates the durable record and
// exact repository binding before this projection is returned.
type MutationCompletionWorkRunAuthority struct {
	Store mutationintegrity.Store
	lease *reviewtransaction.RepositoryIdentityLease
	// existingOnly preserves the provider status invariant: resolving an
	// already-bound handoff may open its existing MMI store, but a read path
	// must never publish a missing owner repository.
	existingOnly bool
}

func (authority MutationCompletionWorkRunAuthority) ResolveMutationCompletion(
	ctx context.Context,
	ref string,
) (workrun.MutationCompletionAuthority, error) {
	store, err := authority.resolveStore(ctx)
	if err != nil {
		return workrun.MutationCompletionAuthority{}, err
	}
	completion, err := store.Read(ctx, ref)
	if err != nil {
		return workrun.MutationCompletionAuthority{}, err
	}
	if err := completion.Validate(); err != nil {
		return workrun.MutationCompletionAuthority{}, err
	}
	return workrun.MutationCompletionAuthority{
		CompletionRef: completion.CompletionRef,
		RepositoryRef: completion.RepositoryRef,
		WorkRunID:     completion.WorkRunID,
		Route:         completion.Route,
		ScopeDigest:   completion.ScopeDigest,
		Snapshot:      completion.Snapshot,
	}, nil
}

func (authority MutationCompletionWorkRunAuthority) resolveStore(
	ctx context.Context,
) (mutationintegrity.Store, error) {
	if authority.Store.Root() != "" {
		return authority.Store, nil
	}
	if authority.lease == nil {
		return mutationintegrity.Store{}, fmt.Errorf(
			"%w: mutation completion authority",
			workrun.ErrAuthorityPortUnavailable,
		)
	}
	if err := authority.lease.Validate(ctx); err != nil {
		return mutationintegrity.Store{}, err
	}
	if authority.existingOnly {
		root := filepath.Join(
			authority.lease.Identity().GitCommonDir,
			"gentle-ai",
			"mutation-integrity",
			"v1",
			"repositories",
			authority.lease.StorageKey(),
			"completions",
		)
		info, err := os.Lstat(root)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return mutationintegrity.Store{},
				mutationintegrity.ErrCompletionNotFound
		case err != nil:
			return mutationintegrity.Store{}, err
		case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
			return mutationintegrity.Store{}, errors.New(
				"mutation completion store root is unsafe",
			)
		}
	}
	return mutationintegrity.OpenStore(ctx, authority.lease)
}

// ForecastDispositionAuthority is the coordination-owner subset required
// before a terminal RAR result exists. Terminal result and receipt authority
// remain in reviewtransaction and are deliberately not copied into this port.
type ForecastDispositionAuthority interface {
	ResolveForecast(
		context.Context,
		string,
	) (workrun.VerificationForecastAuthority, error)
	ResolveDisposition(
		context.Context,
		string,
	) (workrun.VerificationDispositionAuthority, error)
}

// RARWorkRunAuthority composes coordination-owned forecast/consent facts with
// the native RAR repository. It is the production WorkRun verification port;
// CompactReceipt v2 is never converted to the historical Receipt v1 shape.
type RARWorkRunAuthority struct {
	Coordination ForecastDispositionAuthority
	RAR          *reviewtransaction.RARAuthorityRepository
}

func (authority RARWorkRunAuthority) ResolveForecast(
	ctx context.Context,
	ref string,
) (workrun.VerificationForecastAuthority, error) {
	if authority.Coordination == nil {
		return workrun.VerificationForecastAuthority{},
			fmt.Errorf("%w: forecast authority", workrun.ErrAuthorityPortUnavailable)
	}
	return authority.Coordination.ResolveForecast(ctx, ref)
}

func (authority RARWorkRunAuthority) ResolveDisposition(
	ctx context.Context,
	ref string,
) (workrun.VerificationDispositionAuthority, error) {
	if authority.Coordination == nil {
		return workrun.VerificationDispositionAuthority{},
			fmt.Errorf("%w: disposition authority", workrun.ErrAuthorityPortUnavailable)
	}
	return authority.Coordination.ResolveDisposition(ctx, ref)
}

func (authority RARWorkRunAuthority) ResolveResult(
	ctx context.Context,
	resultRef string,
) (workrun.VerificationResultAuthority, error) {
	if authority.RAR == nil {
		return workrun.VerificationResultAuthority{},
			fmt.Errorf("%w: RAR result authority", workrun.ErrAuthorityPortUnavailable)
	}
	resolved, err := authority.RAR.ResolveResult(ctx, resultRef)
	if err != nil {
		return workrun.VerificationResultAuthority{}, err
	}
	return projectRARResultAuthority(resolved, resultRef)
}

func (authority RARWorkRunAuthority) ResolveReviewReceipt(
	ctx context.Context,
	receiptRef string,
	resultRef string,
) (workrun.ReviewReceiptAuthority, error) {
	if authority.RAR == nil {
		return workrun.ReviewReceiptAuthority{},
			fmt.Errorf("%w: RAR receipt authority", workrun.ErrAuthorityPortUnavailable)
	}
	resolved, err := authority.RAR.ResolveReceiptResult(ctx, receiptRef, resultRef)
	if err != nil {
		return workrun.ReviewReceiptAuthority{}, err
	}
	if err := resolved.Validate(); err != nil {
		return workrun.ReviewReceiptAuthority{}, err
	}
	return workrun.ReviewReceiptAuthority{
		ReceiptRef:            resolved.Receipt.ReceiptRef,
		CandidateRef:          resolved.Result.Subject.SnapshotIdentity,
		VerificationResultRef: resolved.Result.ResultRef,
	}, nil
}

func projectRARResultAuthority(
	resolved reviewtransaction.RARVerificationAuthority,
	resultRef string,
) (workrun.VerificationResultAuthority, error) {
	if err := resolved.Validate(); err != nil {
		return workrun.VerificationResultAuthority{}, err
	}
	projected := workrun.VerificationResultAuthority{
		Applicability: resolved.Applicability,
		Registry:      resolved.Registry,
		Plan:          resolved.Plan,
		Result:        resolved.Result,
	}
	if err := projected.Validate(resultRef); err != nil {
		return workrun.VerificationResultAuthority{}, err
	}
	return projected, nil
}

// EvidenceWorkRunPort adapts only already-admitted EPD records. Raw process
// output and action-ticket argv never cross the WorkRun authority boundary.
type EvidenceWorkRunPort struct {
	Store evidence.Store
}

func (port EvidenceWorkRunPort) ReadActionTicket(
	ctx context.Context,
	ticketRef string,
) (workrun.AdmittedActionTicket, error) {
	if err := ctx.Err(); err != nil {
		return workrun.AdmittedActionTicket{}, err
	}
	ticket, err := port.Store.ReadTicket(ticketRef)
	if err != nil {
		return workrun.AdmittedActionTicket{}, err
	}
	slotBindingRef, err := ticket.SlotBindingRef()
	if err != nil {
		return workrun.AdmittedActionTicket{}, err
	}
	projected := workrun.AdmittedActionTicket{
		TicketRef:              ticket.TicketRef,
		SubjectRef:             ticket.SubjectRef,
		CandidateRef:           ticket.CandidateRef,
		VerificationContextRef: ticket.VerificationContextRef,
		ExpectedRevision:       ticket.ExpectedRevision,
		Slot:                   ticket.Slot,
		Capability:             ticket.Capability,
		SlotBindingRef:         slotBindingRef,
		HostRequestDigest:      ticket.RequestBinding.RequestDigest,
	}
	if err := projected.Validate(); err != nil {
		return workrun.AdmittedActionTicket{}, err
	}
	return projected, nil
}

func (port EvidenceWorkRunPort) ReadExecutionEvidence(
	ctx context.Context,
	evidenceRef string,
) (workrun.AdmittedExecutionEvidence, error) {
	if err := ctx.Err(); err != nil {
		return workrun.AdmittedExecutionEvidence{}, err
	}
	admitted, err := port.Store.ReadExecution(evidenceRef)
	if err != nil {
		return workrun.AdmittedExecutionEvidence{}, err
	}
	projected := workrun.AdmittedExecutionEvidence{
		EvidenceRef:            admitted.EvidenceRef,
		TicketRef:              admitted.TicketRef,
		SlotBindingRef:         admitted.SlotBindingRef,
		SubjectRef:             admitted.SubjectRef,
		CandidateRef:           admitted.CandidateRef,
		VerificationContextRef: admitted.VerificationContextRef,
		ExpectedRevision:       admitted.ExpectedRevision,
		Slot:                   admitted.Slot,
		Capability:             admitted.Capability,
		Complete:               admitted.Outcome == evidence.ExecutionComplete,
	}
	if err := projected.Validate(); err != nil {
		return workrun.AdmittedExecutionEvidence{}, err
	}
	return projected, nil
}

var _ workrun.VerificationAuthorityPort = RARWorkRunAuthority{}
var _ workrun.MutationCompletionAuthorityPort = MutationCompletionWorkRunAuthority{}
var _ workrun.EvidencePort = EvidenceWorkRunPort{}
