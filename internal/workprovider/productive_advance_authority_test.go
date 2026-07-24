package workprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestWorkAdvanceGitAuthorityUnavailableIsTypedAndStartsNoEffect(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"git-authority-unavailable",
		productiveAdvanceTestCASSucceed,
		false,
	)
	ctx := context.Background()
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		fixture.repo,
		fixture.runtime.agent,
		fixture.activation,
		fixture.connector,
	)
	if err != nil {
		t.Fatal(err)
	}
	factory.candidateStore.objects.executable = ""
	first, err := advanceProductiveWork(
		ctx,
		factory,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	message, _ := workrun.WorkAdvanceDiagnosticMessage(
		workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable,
	)
	if first.Status.PublicState != workrun.PublicStateNeedsYourDecision ||
		first.Diagnostic == nil ||
		first.Diagnostic.Code !=
			workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable ||
		first.Diagnostic.Message != message ||
		first.DeliveryResultRef != "" ||
		fixture.casCalls() != 0 ||
		fixture.remoteRevision(t) != fixture.base {
		t.Fatalf(
			"unavailable Git authority terminal/effect = %#v / %d",
			first,
			fixture.casCalls(),
		)
	}
	replayed, err := advanceProductiveWork(
		ctx,
		factory,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil || !reflect.DeepEqual(replayed, first) ||
		fixture.casCalls() != 0 {
		t.Fatalf(
			"unavailable Git authority replay = %#v, %v; CAS %d",
			replayed,
			err,
			fixture.casCalls(),
		)
	}

	store, err := existingProductiveAdvanceStore(
		factory.lease,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	diagnosticPath := filepath.Join(
		store.root,
		"diagnostic-"+
			productiveAdvanceRevisionKey(first.Diagnostic.Ref)+
			".json",
	)
	if err := os.Remove(diagnosticPath); err != nil {
		t.Fatal(err)
	}
	coordinator, err := factory.Open(ctx, fixture.workRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.RecordProductiveBlocker(
		ctx,
		fixture.start.Revision,
		first.Diagnostic.Ref,
	); err == nil {
		t.Fatal(
			"owner blocker replay accepted missing diagnostic authority",
		)
	}
	if _, err := coordinator.recordRecoveredProductiveBlocker(
		ctx,
		fixture.start.Revision,
		first.Diagnostic.Ref,
	); err == nil {
		t.Fatal(
			"owner recovered blocker replay accepted missing diagnostic authority",
		)
	}
}

func TestProductiveDeliveryExecutionBlockerDistinguishesPreclaimExpiry(
	t *testing.T,
) {
	if got := productiveDeliveryExecutionBlocker(
		deliveryadmission.ErrExpired,
	); got != workrun.WorkAdvanceDiagnosticDeliveryAuthorizationExpired {
		t.Fatalf("pre-claim expiry diagnostic = %q", got)
	}
	postClaim := fmt.Errorf(
		"%w: %w",
		deliveryadmission.ErrExecutionRejected,
		deliveryadmission.ErrExpired,
	)
	if got := productiveDeliveryExecutionBlocker(
		postClaim,
	); got != workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate {
		t.Fatalf("post-claim expiry diagnostic = %q", got)
	}
}

func TestOwnerDeliveryResultReplayRequiresLiveTerminalAuthority(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"delivery-result-owner-replay",
		productiveAdvanceTestCASSucceed,
		false,
	)
	ctx := context.Background()
	ready, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status.PublicState != workrun.PublicStateReady ||
		ready.DeliveryResultRef == "" ||
		fixture.casCalls() != 1 {
		t.Fatalf("ready delivery fixture = %#v", ready)
	}
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		fixture.repo,
		fixture.runtime.agent,
		fixture.activation,
		fixture.connector,
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := existingProductiveAdvanceStore(
		factory.lease,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(
		store.root,
		"delivery-result-"+
			productiveAdvanceRevisionKey(ready.DeliveryResultRef)+
			".json",
	)
	if err := os.Remove(resultPath); err != nil {
		t.Fatal(err)
	}
	coordinator, err := factory.Open(ctx, fixture.workRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.BindDeliveryResult(
		ctx,
		fixture.start.Revision,
		ready.DeliveryResultRef,
	); err == nil {
		t.Fatal(
			"owner delivery-result replay accepted missing authority",
		)
	}
	if _, err := coordinator.bindRecoveredDeliveryResult(
		ctx,
		fixture.start.Revision,
		ready.DeliveryResultRef,
	); err == nil {
		t.Fatal(
			"owner recovered delivery-result replay accepted missing authority",
		)
	}
}
