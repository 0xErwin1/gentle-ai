package cli

import (
	"context"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type cliActivationResolver struct {
	mode  workprovider.ActivationMode
	err   error
	calls int
}

func (resolver *cliActivationResolver) ResolveActivation(
	context.Context,
	string,
) (workprovider.ActivationMode, error) {
	resolver.calls++
	return resolver.mode, resolver.err
}

type cliRepositoryOpener struct {
	repository workprovider.Repository
	calls      int
}

func (opener *cliRepositoryOpener) OpenRepository(
	context.Context,
	string,
	string,
) (workprovider.Repository, error) {
	opener.calls++
	return opener.repository, nil
}

type cliRepository struct {
	status        workrun.WorkStatusV1
	authorization workprovider.ResolvedAuthorization
	transition    workrun.WorkTransitionV1
	resolveErr    error

	resolveCalls int
	applyCalls   int
}

func (repository *cliRepository) Status(
	context.Context,
) (workrun.WorkStatusV1, error) {
	return repository.status, nil
}

func (repository *cliRepository) ResolveAuthorization(
	context.Context,
	string,
) (workprovider.ResolvedAuthorization, error) {
	repository.resolveCalls++
	return repository.authorization, repository.resolveErr
}

func (repository *cliRepository) ApplyAuthorized(
	context.Context,
	string,
	string,
) (workrun.WorkTransitionV1, error) {
	repository.applyCalls++
	return repository.transition, nil
}

func cliValidStatus() workrun.WorkStatusV1 {
	return workrun.WorkStatusV1{
		Schema: workrun.WorkStatusContractV1, Contract: workrun.WorkStatusContractV1,
		WorkRunID: "cli-work-run", Revision: cliRevision("a"),
		PublicState:         workrun.PublicStateWorking,
		RouteDecision:       workrun.RouteDecisionDirectInline,
		RoutePhase:          workrun.RoutePhaseImplementationSelected,
		ImplementationRoute: workrun.ImplementationRouteDirectInline,
		Verification: workrun.VerificationSummaryV1{
			Outcome: workrun.VerificationPending, ResultRefs: []string{},
		},
	}
}

func cliValidRepository() *cliRepository {
	return &cliRepository{
		status: cliValidStatus(),
		authorization: workprovider.ResolvedAuthorization{
			WorkRunID:                  "cli-work-run",
			AuthorizationRef:           "authorization:cli-test",
			ExpectedRevision:           cliRevision("a"),
			CandidateRef:               "candidate:cli-test",
			ActionTicketRef:            "ticket:cli-test",
			ApplicableAuthorizationRef: "authorization:applicability-cli-test",
			Class:                      workprovider.AuthorizationGeneral,
		},
		transition: workrun.WorkTransitionV1{
			Schema: workrun.WorkTransitionContractV1, Contract: workrun.WorkTransitionContractV1,
			WorkRunID:        "cli-work-run",
			PreviousRevision: cliRevision("a"), Revision: cliRevision("b"),
			AppliedAuthorizationRef: "authorization:cli-test",
			PublicState:             workrun.PublicStateChecking,
		},
	}
}

func cliRevision(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func cliController(
	repository *cliRepository,
	mode workprovider.ActivationMode,
) (workprovider.Controller, *cliActivationResolver, *cliRepositoryOpener) {
	activation := &cliActivationResolver{mode: mode}
	opener := &cliRepositoryOpener{repository: repository}
	return workprovider.NewController(opener, activation), activation, opener
}

var _ workprovider.Repository = (*cliRepository)(nil)
var _ workprovider.RepositoryOpener = (*cliRepositoryOpener)(nil)
var _ workprovider.ActivationResolver = (*cliActivationResolver)(nil)
