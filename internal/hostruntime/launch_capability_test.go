package hostruntime

import (
	"context"
	"errors"
	"testing"
)

func TestLaunchCapabilityIsExecutorBoundExactAndOneShot(t *testing.T) {
	step := helperStep(t, t.TempDir(), "exit", "0")
	request, err := BindExecStep(step)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor()
	claims := 0
	activate := func() *LaunchCapability {
		t.Helper()
		capability, activateErr := executor.ActivateLaunch(
			context.Background(),
			LaunchBinding{
				Schema: LaunchBindingSchemaV1, WorkRunID: "work-run",
				ReservationRef:  digestValues("reservation", t.Name()),
				ActionTicketRef: digestValues("ticket", t.Name()),
				RequestDigest:   request.RequestDigest,
			},
			func(context.Context) error {
				claims++
				return nil
			},
		)
		if activateErr != nil {
			t.Fatal(activateErr)
		}
		return capability
	}

	foreign := activate()
	if _, err := NewExecutor().ExecuteAuthorized(
		context.Background(),
		step,
		foreign,
	); err == nil {
		t.Fatal("a different Executor instance accepted the capability")
	}
	if claims != 0 {
		t.Fatalf("foreign Executor invoked durable claim %d times", claims)
	}
	if _, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		foreign,
	); !errors.Is(err, ErrLaunchCapabilityConsumed) {
		t.Fatalf("failed foreign provenance did not consume capability: %v", err)
	}

	mismatched := activate()
	changed := step
	changed.ExecutionID += "-changed"
	if _, err := executor.ExecuteAuthorized(
		context.Background(),
		changed,
		mismatched,
	); !errors.Is(err, ErrLaunchBindingMismatch) {
		t.Fatalf("mismatched request error = %v", err)
	}
	if claims != 0 {
		t.Fatalf("mismatched request invoked durable claim %d times", claims)
	}

	authorized := activate()
	if _, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		authorized,
	); err != nil {
		t.Fatalf("exact authorized execution failed: %v", err)
	}
	if claims != 1 {
		t.Fatalf("exact execution durable claim count = %d, want 1", claims)
	}
	if _, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		authorized,
	); !errors.Is(err, ErrLaunchCapabilityConsumed) {
		t.Fatalf("authorized capability replay error = %v", err)
	}
}
