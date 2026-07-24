package workprovider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestProductionRecoveryFactoryResolvesAnchoredCandidateWithoutConnector(
	t *testing.T,
) {
	for _, test := range []struct {
		name        string
		mutateIndex func(*testing.T, *PADCandidateCatalog)
	}{
		{name: "index_intact"},
		{
			name: "index_deleted",
			mutateIndex: func(t *testing.T, catalog *PADCandidateCatalog) {
				t.Helper()
				path := ownerOnlyCandidateIndexPath(t, catalog)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "index_rewired",
			mutateIndex: func(t *testing.T, catalog *PADCandidateCatalog) {
				t.Helper()
				path := ownerOnlyCandidateIndexPath(t, catalog)
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var index padCandidateCatalogIndex
				if err := decodeStrictCoordinationJSON(
					payload,
					&index,
				); err != nil {
					t.Fatal(err)
				}
				index.RecordRef = ownerTestRef(
					"connector-free-recovery-rewired-index",
				)
				payload, err = canonicalCoordinationPayload(index)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, payload, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProductiveAdvanceTestFixture(
				t,
				"recovery-no-connector-"+test.name,
				productiveAdvanceTestCASSucceed,
				false,
			)
			advanced, err := fixture.runtime.AdvanceOutcome(
				context.Background(),
				fixture.workRunID,
				fixture.start.Revision,
			)
			if err != nil {
				t.Fatal(err)
			}
			if advanced.Status.PublicState != workrun.PublicStateReady ||
				fixture.casCalls() != 1 {
				t.Fatalf(
					"productive setup status/effects = %#v/%d",
					advanced.Status,
					fixture.casCalls(),
				)
			}

			factory, err := NewProductionOwnerCoordinatorFactory(
				context.Background(),
				fixture.repo,
				model.AgentCodex,
				StaticActivationResolver{Mode: ActivationDisabled},
			)
			if err != nil {
				t.Fatal(err)
			}
			if factory.candidateStore != nil ||
				factory.bindingSource != nil {
				t.Fatal(
					"connector-less factory retained mutable candidate authorities",
				)
			}
			coordinator, err := factory.openForProductiveRecovery(
				context.Background(),
				fixture.workRunID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if coordinator.padCandidates == nil ||
				coordinator.padBindingSource != nil {
				t.Fatal(
					"recovery did not compose an exact catalog-only authority",
				)
			}
			if test.mutateIndex != nil {
				test.mutateIndex(t, coordinator.padCandidates)
			}
			beforeCatalog := ownerTreeContentFingerprint(
				t,
				coordinator.padCandidates.root,
			)
			fixture.connector.hosting.mu.Lock()
			probesBefore := fixture.connector.hosting.observationCalls
			fixture.connector.hosting.mu.Unlock()

			recovered, found, err := coordinator.RecoverBoundDelivery(
				context.Background(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if !found ||
				recovered.Outcome !=
					deliveryadmission.ExecutionSucceeded ||
				recovered.CommandRef == "" {
				t.Fatalf(
					"connector-less recovery = %#v, found %t",
					recovered,
					found,
				)
			}
			if afterCatalog := ownerTreeContentFingerprint(
				t,
				coordinator.padCandidates.root,
			); afterCatalog != beforeCatalog {
				t.Fatal(
					"connector-less exact recovery mutated the candidate catalog",
				)
			}
			fixture.connector.hosting.mu.Lock()
			probesAfter := fixture.connector.hosting.observationCalls
			fixture.connector.hosting.mu.Unlock()
			if probesAfter != probesBefore {
				t.Fatalf(
					"connector-less recovery repeated live probe: %d -> %d",
					probesBefore,
					probesAfter,
				)
			}
			if fixture.casCalls() != 1 {
				t.Fatalf(
					"connector-less recovery repeated delivery effect: %d",
					fixture.casCalls(),
				)
			}
		})
	}
}

func ownerOnlyCandidateIndexPath(
	t *testing.T,
	catalog *PADCandidateCatalog,
) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(catalog.root, "bindings"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].IsDir() {
		t.Fatalf("candidate index entries = %#v", entries)
	}
	return filepath.Join(catalog.root, "bindings", entries[0].Name())
}
