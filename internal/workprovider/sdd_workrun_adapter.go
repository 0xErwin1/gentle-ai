package workprovider

import (
	"context"
	"errors"

	"github.com/gentleman-programming/gentle-ai/internal/sddstatus"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// SDDWorkRunAuthority is the optional SDD adapter. It opens only the explicitly
// selected runRef; direct and delegated WorkRuns never invoke it and therefore
// never create SDD artifacts or consume SDD attempt authority.
type SDDWorkRunAuthority struct {
	Repo string
}

func (authority SDDWorkRunAuthority) ResolveRun(
	ctx context.Context,
	runRef string,
) (workrun.SDDRunAuthority, error) {
	if authority.Repo == "" {
		return workrun.SDDRunAuthority{}, errors.New(
			"SDD WorkRun authority repository is required",
		)
	}
	store, err := sddstatus.OpenRuntimeStore(ctx, authority.Repo, runRef)
	if err != nil {
		return workrun.SDDRunAuthority{}, err
	}
	binding, err := store.ResolveWorkRunBinding(ctx)
	if err != nil {
		return workrun.SDDRunAuthority{}, err
	}
	projected := workrun.SDDRunAuthority{
		RunRef:             binding.RunRef,
		WorkRunID:          binding.WorkRunID,
		RouteAcceptanceRef: binding.RouteAcceptanceRef,
	}
	if err := projected.Validate(
		runRef,
		binding.WorkRunID,
		binding.RouteAcceptanceRef,
	); err != nil {
		return workrun.SDDRunAuthority{}, err
	}
	return projected, nil
}

func (authority SDDWorkRunAuthority) BindVerificationReservation(
	ctx context.Context,
	binding workrun.SDDReservationBinding,
) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if authority.Repo == "" {
		return errors.New("SDD WorkRun authority repository is required")
	}
	store, err := sddstatus.OpenRuntimeStore(
		ctx,
		authority.Repo,
		binding.SDDRunRef,
	)
	if err != nil {
		return err
	}
	run, err := store.ResolveWorkRunBinding(ctx)
	if err != nil {
		return err
	}
	if run.RunRef != binding.SDDRunRef ||
		run.WorkRunID != binding.WorkRunID {
		return workrun.ErrAuthorityBindingMismatch
	}
	_, err = store.BindCommonVerificationReservation(
		ctx,
		binding.WorkRunID,
		binding.WorkRunRevision,
		binding.ReservationRef,
		binding.ActionTicketRef,
	)
	return err
}

var _ workrun.SDDAuthorityPort = SDDWorkRunAuthority{}
