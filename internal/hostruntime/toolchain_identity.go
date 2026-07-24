package hostruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

const ToolchainIdentitySchemaV1 = "gentle-ai.host-toolchain-identity/v1"

type ToolchainIdentityState string

const (
	ToolchainIdentityObserved    ToolchainIdentityState = "observed"
	ToolchainIdentityUnavailable ToolchainIdentityState = "unavailable"
)

var ErrToolchainIdentityChanged = errors.New("host runtime toolchain identity changed")

// ToolchainIdentity is a path-free owner observation of the exact executable
// bytes selected for one request. Its fields are embedded into request and
// process evidence so legacy v1 JSON remains byte-for-byte representable when
// they are absent.
type ToolchainIdentity struct {
	ToolchainIdentitySchema string                 `json:"toolchainIdentitySchema,omitempty"`
	ToolchainIdentityRef    string                 `json:"toolchainIdentityRef,omitempty"`
	ToolchainState          ToolchainIdentityState `json:"toolchainState,omitempty"`
	ToolchainProgramDigest  string                 `json:"toolchainProgramDigest,omitempty"`
	ToolchainContentDigest  string                 `json:"toolchainContentDigest,omitempty"`
	ToolchainBytes          int64                  `json:"toolchainBytes,omitempty"`
}

func (identity ToolchainIdentity) IsZero() bool {
	return identity == (ToolchainIdentity{})
}

func (identity ToolchainIdentity) Validate() error {
	if identity.ToolchainIdentitySchema != ToolchainIdentitySchemaV1 ||
		!validEvidenceDigest(identity.ToolchainIdentityRef) ||
		!validEvidenceDigest(identity.ToolchainProgramDigest) {
		return errors.New("host toolchain identity is incomplete")
	}
	switch identity.ToolchainState {
	case ToolchainIdentityObserved:
		if !validEvidenceDigest(identity.ToolchainContentDigest) ||
			identity.ToolchainBytes <= 0 {
			return errors.New("observed host toolchain identity is incomplete")
		}
	case ToolchainIdentityUnavailable:
		if identity.ToolchainContentDigest != "" || identity.ToolchainBytes != 0 {
			return errors.New("unavailable host toolchain identity carries observed content")
		}
	default:
		return errors.New("host toolchain identity state is invalid")
	}
	if identity.ToolchainIdentityRef != toolchainIdentityRef(identity) {
		return errors.New("host toolchain identity reference does not match its content")
	}
	return nil
}

// ObserveToolchainIdentity hashes the exact executable bytes without exposing
// the executable path. An unavailable executable remains a typed identity so a
// missing-tool attempt can be recorded, but it can never support semantic PASS.
func ObserveToolchainIdentity(program string) (ToolchainIdentity, error) {
	if program == "" || !filepath.IsAbs(program) || filepath.Clean(program) != program {
		return ToolchainIdentity{}, errors.New("host toolchain program must be an absolute canonical path")
	}
	programDigest := digestValues("gentle-ai.host-program/v1", program)
	unavailable := ToolchainIdentity{
		ToolchainIdentitySchema: ToolchainIdentitySchemaV1,
		ToolchainState:          ToolchainIdentityUnavailable,
		ToolchainProgramDigest:  programDigest,
	}
	unavailable.ToolchainIdentityRef = toolchainIdentityRef(unavailable)

	file, err := os.Open(program)
	if err != nil {
		return unavailable, nil
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 {
		return unavailable, nil
	}
	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil {
		return ToolchainIdentity{}, errors.New("observe host toolchain content")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) ||
		before.Size() != after.Size() ||
		before.ModTime() != after.ModTime() ||
		written != after.Size() {
		return ToolchainIdentity{}, errors.New("host toolchain changed during observation")
	}
	identity := ToolchainIdentity{
		ToolchainIdentitySchema: ToolchainIdentitySchemaV1,
		ToolchainState:          ToolchainIdentityObserved,
		ToolchainProgramDigest:  programDigest,
		ToolchainContentDigest:  "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		ToolchainBytes:          written,
	}
	identity.ToolchainIdentityRef = toolchainIdentityRef(identity)
	return identity, nil
}

func toolchainIdentityRef(identity ToolchainIdentity) string {
	return digestValues(
		"gentle-ai.host-toolchain-identity/v1",
		identity.ToolchainIdentitySchema,
		string(identity.ToolchainState),
		identity.ToolchainProgramDigest,
		identity.ToolchainContentDigest,
		strconv.FormatInt(identity.ToolchainBytes, 10),
	)
}
