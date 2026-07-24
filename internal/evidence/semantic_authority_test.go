package evidence

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestSemanticAuthorityRestoresIdentityFromOwnerSeed(t *testing.T) {
	t.Parallel()

	seed := make([]byte, SemanticAuthoritySeedBytes)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	ownerRef := testRef("durable-owner")
	requirementRef := testRef("durable-requirement")
	first, err := ProvisionSemanticAuthority(ownerRef, requirementRef, seed)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ProvisionSemanticAuthority(ownerRef, requirementRef, seed)
	if err != nil {
		t.Fatal(err)
	}
	if first.Identity() != restored.Identity() {
		t.Fatalf("restored authority identity changed: %#v %#v",
			first.Identity(), restored.Identity())
	}

	request := actionTicketRequest(t, "output", "verified details")
	request.IssuerRef = ownerRef
	request.SemanticRequirementRef = requirementRef
	request.SemanticAuthority = first.Identity()
	ticket, err := NewActionTicket(request)
	if err != nil {
		t.Fatal(err)
	}
	process := executeTicket(t, context.Background(), ticket, false)
	verdict, err := restored.AttestPassed(ticket, process)
	if err != nil {
		t.Fatalf("restored owner signer could not attest: %v", err)
	}
	evidence, err := AdmitExecutionWithSemanticVerdict(
		ticket,
		process,
		verdict,
	)
	if err != nil || evidence.Outcome != ExecutionComplete {
		t.Fatalf("restored signer admission = %#v, %v", evidence, err)
	}

	payload, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(
		string(payload),
		base64.RawStdEncoding.EncodeToString(seed),
	) {
		t.Fatal("semantic evidence serialized the owner private seed")
	}
}

func TestSemanticAuthorityProvisioningRejectsMissingOrZeroOwnerSeed(t *testing.T) {
	t.Parallel()

	ownerRef := testRef("owner")
	requirementRef := testRef("requirement")
	for _, seed := range [][]byte{
		nil,
		make([]byte, SemanticAuthoritySeedBytes-1),
		make([]byte, SemanticAuthoritySeedBytes),
	} {
		if _, err := ProvisionSemanticAuthority(
			ownerRef,
			requirementRef,
			seed,
		); err == nil {
			t.Fatalf("ProvisionSemanticAuthority() accepted unsafe seed length/content %d", len(seed))
		}
	}
}
