package hostruntime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
)

const LaunchBindingSchemaV1 = "gentle-ai.host-launch-binding/v1"

var (
	ErrLaunchCapabilityRequired = errors.New("host runtime launch capability is required")
	ErrLaunchCapabilityConsumed = errors.New("host runtime launch capability is already consumed")
	ErrLaunchBindingMismatch    = errors.New("host runtime launch capability does not match the exact request")
)

// LaunchBinding contains only immutable coordination identities. It never
// contains argv, environment, output, or lifecycle authority.
type LaunchBinding struct {
	Schema          string `json:"schema"`
	WorkRunID       string `json:"workRunId"`
	ReservationRef  string `json:"reservationRef"`
	ActionTicketRef string `json:"actionTicketRef"`
	RequestDigest   string `json:"requestDigest"`
}

func (binding LaunchBinding) Validate() error {
	if binding.Schema != LaunchBindingSchemaV1 ||
		binding.WorkRunID == "" ||
		!validEvidenceDigest(binding.ReservationRef) ||
		!validEvidenceDigest(binding.ActionTicketRef) ||
		!validEvidenceDigest(binding.RequestDigest) {
		return errors.New("invalid host runtime launch binding")
	}
	return nil
}

// LaunchClaim durably claims the reservation immediately before HCR creates a
// process. Returning an error consumes the in-memory capability but launches
// nothing; ambiguous persistence therefore fails closed instead of retrying.
type LaunchClaim func(context.Context) error

// LaunchCapability is opaque outside HCR. There is deliberately no exported
// constructor. Provider composition obtains it only through a LaunchAuthority
// passed to WorkRun as its HCR-owned LaunchAuthorityPort.
type LaunchCapability struct {
	binding LaunchBinding
	claim   LaunchClaim
	seal    [sha256.Size]byte

	mu       sync.Mutex
	consumed bool
}

// ActivateLaunch exposes Executor as a provider-owned WorkRun port. Capability
// provenance is keyed by this exact Executor instance; there is no standalone
// public issuer type or capability constructor.
func (executor *Executor) ActivateLaunch(
	ctx context.Context,
	binding LaunchBinding,
	claim LaunchClaim,
) (*LaunchCapability, error) {
	if executor == nil || !executor.launchReady {
		return nil, errors.New("host runtime launch authority is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if claim == nil {
		return nil, errors.New("host runtime launch activation requires a durable claim")
	}
	capability := &LaunchCapability{binding: binding, claim: claim}
	capability.seal = launchCapabilitySeal(executor.launchKey, binding)
	return capability, nil
}

func (capability *LaunchCapability) ReservationRef() string {
	if capability == nil {
		return ""
	}
	return capability.binding.ReservationRef
}

func (capability *LaunchCapability) consume(
	ctx context.Context,
	requestDigest string,
	launchKey [sha256.Size]byte,
) error {
	if capability == nil {
		return ErrLaunchCapabilityRequired
	}
	capability.mu.Lock()
	defer capability.mu.Unlock()

	if capability.consumed {
		return ErrLaunchCapabilityConsumed
	}
	// A failed durable claim is still one attempted launch. Consuming first makes
	// publication uncertainty fail closed and prevents a second process.
	capability.consumed = true
	want := launchCapabilitySeal(launchKey, capability.binding)
	if subtle.ConstantTimeCompare(capability.seal[:], want[:]) != 1 {
		return errors.New("host runtime launch capability provenance is invalid")
	}
	if capability.binding.RequestDigest != requestDigest {
		return ErrLaunchBindingMismatch
	}
	if err := capability.claim(ctx); err != nil {
		return fmt.Errorf("claim host runtime launch: %w", err)
	}
	return nil
}

func launchCapabilitySeal(
	key [sha256.Size]byte,
	binding LaunchBinding,
) [sha256.Size]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(
		"gentle-ai.host-launch-capability/v1\x00" +
			binding.Schema + "\x00" +
			binding.WorkRunID + "\x00" +
			binding.ReservationRef + "\x00" +
			binding.ActionTicketRef + "\x00" +
			binding.RequestDigest))
	var result [sha256.Size]byte
	copy(result[:], mac.Sum(nil))
	return result
}
