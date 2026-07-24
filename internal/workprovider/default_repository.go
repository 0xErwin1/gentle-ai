package workprovider

import (
	"context"

	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// NewDefaultController exposes the existing WorkRun reader under a read-only
// default. The owner authorization repository/applier is intentionally absent
// until its integration work lands.
func NewDefaultController() Controller {
	return NewController(
		ReadOnlyWorkRunRepositoryOpener{},
		EnvironmentActivationResolver{},
	)
}

type ReadOnlyWorkRunRepositoryOpener struct{}

func (ReadOnlyWorkRunRepositoryOpener) OpenRepository(
	ctx context.Context,
	repo string,
	workRunID string,
) (Repository, error) {
	store, err := workrun.OpenWorkRunStore(ctx, repo, workRunID)
	if err != nil {
		return nil, err
	}
	return readOnlyWorkRunRepository{store: store}, nil
}

type readOnlyWorkRunRepository struct {
	store workrun.WorkRunStore
}

func (repository readOnlyWorkRunRepository) Status(
	ctx context.Context,
) (workrun.WorkStatusV1, error) {
	return repository.store.PublicStatus(ctx)
}

func (readOnlyWorkRunRepository) ResolveAuthorization(
	context.Context,
	string,
) (ResolvedAuthorization, error) {
	return ResolvedAuthorization{}, ErrAuthorizationNotFound
}

func (readOnlyWorkRunRepository) ApplyAuthorized(
	context.Context,
	string,
	string,
) (workrun.WorkTransitionV1, error) {
	return workrun.WorkTransitionV1{}, ErrCapabilityReadOnly
}

var _ RepositoryOpener = ReadOnlyWorkRunRepositoryOpener{}
var _ Repository = readOnlyWorkRunRepository{}
