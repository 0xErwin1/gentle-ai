package workprovider

import (
	"context"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
)

// padPinnedCandidateBindingAuthority resolves one exact catalog record for the
// lifetime of a delivery decision. The mutable lookup index is consulted only
// as a fail-closed continuity guard; it never supplies the binding returned to
// a probe or effect.
type padPinnedCandidateBindingAuthority struct {
	catalog   *PADCandidateCatalog
	authority PADCandidateAuthority
}

func newPADPinnedCandidateBindingAuthority(
	ctx context.Context,
	catalog *PADCandidateCatalog,
	authority PADCandidateAuthority,
) (*padPinnedCandidateBindingAuthority, error) {
	if catalog == nil {
		return nil, ErrPADCandidateCatalogUnavailable
	}
	resolved, err := catalog.ResolveCandidateAuthority(
		ctx,
		authority.RecordRef,
		authority.Binding.Candidate,
		authority.Binding.Destination,
		authority.Binding.Mechanism,
		authority.CandidateTree,
	)
	if err != nil {
		return nil, err
	}
	if resolved != authority {
		return nil, fmt.Errorf(
			"%w: pinned candidate authority changed",
			ErrPADCandidateCatalogConflict,
		)
	}
	if err := catalog.RequireCurrentCandidateAuthority(ctx, resolved); err != nil {
		return nil, err
	}
	return &padPinnedCandidateBindingAuthority{
		catalog:   catalog,
		authority: resolved,
	}, nil
}

func (pinned *padPinnedCandidateBindingAuthority) ResolvePADGitBinding(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	if pinned == nil || pinned.catalog == nil {
		return PADGitBinding{}, ErrPADCandidateCatalogUnavailable
	}
	resolved, err := pinned.catalog.ResolveCandidateAuthority(
		ctx,
		pinned.authority.RecordRef,
		candidate,
		destination,
		mechanism,
		pinned.authority.CandidateTree,
	)
	if err != nil {
		return PADGitBinding{}, err
	}
	if resolved != pinned.authority {
		return PADGitBinding{}, fmt.Errorf(
			"%w: exact candidate authority changed",
			ErrPADCandidateCatalogConflict,
		)
	}
	if err := pinned.catalog.RequireCurrentCandidateAuthority(
		ctx,
		resolved,
	); err != nil {
		return PADGitBinding{}, err
	}
	return resolved.Binding, nil
}

var _ PADGitBindingAuthority = (*padPinnedCandidateBindingAuthority)(nil)
