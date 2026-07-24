package workprovider

import (
	"context"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
)

// unavailablePADDeliveryAdapter keeps the legacy/test factory structurally
// complete without inventing Git or hosting authority. Productive composition
// replaces every one of these ports with authenticated implementations.
func newUnavailablePADDeliveryAdapter(
	ctx context.Context,
	authority *PADRepositoryAuthority,
) (*PADDeliveryAdapter, error) {
	return NewPADDeliveryAdapter(
		ctx,
		authority,
		unavailablePADGitBindingAuthority{},
		unavailablePADGitObjectAuthority{},
		unavailableHostingAuthority{},
	)
}

type unavailablePADGitBindingAuthority struct{}

func (unavailablePADGitBindingAuthority) ResolvePADGitBinding(
	context.Context,
	deliveryadmission.CandidateBinding,
	deliveryadmission.DestinationBinding,
	deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	return PADGitBinding{}, ErrPADHostingUnavailable
}

type unavailablePADGitObjectAuthority struct{}

func (unavailablePADGitObjectAuthority) RequireCommit(
	context.Context,
	string,
	string,
) error {
	return ErrPADGitObjectUnavailable
}

func (unavailablePADGitObjectAuthority) ObserveAncestry(
	context.Context,
	string,
	string,
	string,
) (PADGitAncestryObservation, error) {
	return PADGitAncestryObservation{}, ErrPADGitObjectUnavailable
}

type unavailableHostingAuthority struct{}

func (unavailableHostingAuthority) ObserveDelivery(
	context.Context,
	HostingObservationRequest,
) (HostingDeliveryObservation, error) {
	return HostingDeliveryObservation{}, ErrPADHostingUnavailable
}

func (unavailableHostingAuthority) CompareAndSwapBranch(
	context.Context,
	HostingBranchCASRequest,
) (HostingBranchCASReceipt, error) {
	return HostingBranchCASReceipt{}, ErrPADHostingUnavailable
}

func (unavailableHostingAuthority) MergePullRequest(
	context.Context,
	HostingPullRequestMergeRequest,
) (HostingPullRequestMergeReceipt, error) {
	return HostingPullRequestMergeReceipt{}, ErrPADHostingUnavailable
}
