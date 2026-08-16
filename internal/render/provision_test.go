package render

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// Some components are performed rather than written: a download, a clone, an
// installed binary. They have no desired bytes, so they are reconciled by
// presence. Leaving them out of the manifest entirely is what made a document
// declaring them produce a plan that never mentioned them.
func TestProvisionedResourceIsPlannedByPresence(t *testing.T) {
	desired := Manifest{Resources: []Resource{
		{Path: string(model.ComponentEngram), Selector: ProvisionSelector, Component: model.ComponentEngram, Digest: ProvisionPresent},
	}}

	t.Run("absent provisioning is created", func(t *testing.T) {
		plan := Plan(Manifest{}, desired, map[ResourceKey]string{})

		if len(plan.Operations) != 1 {
			t.Fatalf("operations = %+v, want one", plan.Operations)
		}
		operation := plan.Operations[0]
		if operation.Kind != Create {
			t.Errorf("kind = %q, want %q", operation.Kind, Create)
		}
		if operation.Component != model.ComponentEngram {
			t.Errorf("component = %q, want %q", operation.Component, model.ComponentEngram)
		}
	})

	t.Run("present provisioning needs no operation", func(t *testing.T) {
		live := map[ResourceKey]string{
			{Selector: ProvisionSelector, Path: string(model.ComponentEngram)}: ProvisionPresent,
		}

		plan := Plan(desired, desired, live)

		for _, operation := range plan.Operations {
			if operation.Kind != Skip {
				t.Errorf("operation = %+v, want nothing to do for present provisioning", operation)
			}
		}
	})

	// Provisioning that vanished from the desired state is not deleted: removing
	// an installed binary is a destructive action the document never asked for.
	t.Run("provisioning dropped from the document is not removed", func(t *testing.T) {
		live := map[ResourceKey]string{
			{Selector: ProvisionSelector, Path: string(model.ComponentEngram)}: ProvisionPresent,
		}

		plan := Plan(desired, Manifest{}, live)

		for _, operation := range plan.Operations {
			if operation.Kind == Remove {
				t.Errorf("plan removes provisioning: %+v", operation)
			}
		}
	})
}
