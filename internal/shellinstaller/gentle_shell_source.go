package shellinstaller

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"reflect"
	"strings"
)

const gentleShellStablePackageName = "gentle-pi"
const gentleShellMainRepository = "Gentleman-Programming/gentle-shell"
const gentleShellMaxArchiveBytes int64 = 64 << 20

// GentleShellSourceSelection is channel metadata, not publisher identity,
// toolchain closure, authorization, installed bytes or Ready. It is mode-neutral.
type GentleShellSourceSelection struct {
	channel      Channel
	packageName  string
	version      string
	integritySRI string
	sha256Hex    string
	byteLength   int64
	repository   string
	commit       string
}

// NewGentleShellStableSource accepts only a single exact Stable package version
// and two independent EXPECTED digests supplied by a future protected host.
func NewGentleShellStableSource(packageName, version, sri, sha256Hex string,
	byteLength int64) (GentleShellSourceSelection, error) {
	if packageName != gentleShellStablePackageName || !gentleShellExactStableVersion(version) ||
		!gentleShellCanonicalSRI(sri) || !gentleShellLowerSHA256(sha256Hex) ||
		byteLength < 1 || byteLength > gentleShellMaxArchiveBytes {
		return GentleShellSourceSelection{}, errors.New("STOP: invalid Stable archive selector")
	}
	return GentleShellSourceSelection{channel: ChannelStable, packageName: packageName,
		version: version, integritySRI: sri, sha256Hex: sha256Hex,
		byteLength: byteLength}, nil
}

// NewGentleShellMainSource never resolves a branch, tag or default commit.
// A full SHA alone still lacks independently approved source/graph/toolchain.
func NewGentleShellMainSource(repository, commit string) (GentleShellSourceSelection, error) {
	if repository != gentleShellMainRepository || !gentleShellLowerCommit(commit) {
		return GentleShellSourceSelection{}, errors.New("STOP: Main requires canonical repository and full lowercase commit")
	}
	return GentleShellSourceSelection{channel: ChannelMain,
		repository: repository, commit: commit}, nil
}

// GentleShellSourceDescription is DATA, never an Apply or trust state.
type GentleShellSourceDescription struct {
	Kind        string
	Channel     Channel
	PackageName string
	Version     string
	Repository  string
	Commit      string
	Missing     [3]string
}

func (s GentleShellSourceSelection) Describe() (GentleShellSourceDescription, error) {
	result := GentleShellSourceDescription{Kind: "not-authorized", Channel: s.channel,
		Missing: [3]string{"publisher-and-graph-proof", "toolchain-and-loaded-bytes", "instance-consent-and-ready"}}
	switch s.channel {
	case ChannelStable:
		if s.packageName != gentleShellStablePackageName || !gentleShellExactStableVersion(s.version) ||
			!gentleShellCanonicalSRI(s.integritySRI) || !gentleShellLowerSHA256(s.sha256Hex) ||
			s.byteLength < 1 || s.byteLength > gentleShellMaxArchiveBytes ||
			s.repository != "" || s.commit != "" {
			return GentleShellSourceDescription{}, errors.New("STOP: invalid Stable selector")
		}
		result.PackageName, result.Version = s.packageName, s.version
	case ChannelMain:
		if s.repository != gentleShellMainRepository || !gentleShellLowerCommit(s.commit) ||
			s.packageName != "" || s.version != "" || s.integritySRI != "" ||
			s.sha256Hex != "" || s.byteLength != 0 {
			return GentleShellSourceDescription{}, errors.New("STOP: invalid Main selector")
		}
		result.Repository, result.Commit = s.repository, s.commit
		result.Missing[0] = "approved-full-commit-and-graph-proof"
	default:
		return GentleShellSourceDescription{}, errors.New("STOP: missing explicit source channel")
	}
	return result, nil
}

// GentleShellArchiveComparison contains measured DATA only. Matched is NOT a
// signature, publisher authentication, consent or permission to install.
type GentleShellArchiveComparison struct {
	Kind          string
	ByteLength    int64
	MeasuredSHA256 string
	MeasuredSRI   string
	Matched       bool
}

// CompareGentleShellStableArchive reads at most expected bytes + ONE probe
// byte, with a fixed 32KiB buffer. The caller must supply a protected reader;
// this function does not open, extract, parse or execute an archive.
func CompareGentleShellStableArchive(s GentleShellSourceSelection, reader io.Reader) (GentleShellArchiveComparison, error) {
	result := GentleShellArchiveComparison{Kind: "not-authorized"}
	if reader == nil {
		return result, errors.New("STOP: no archive reader")
	}
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		if value.IsNil() {
			return result, errors.New("STOP: typed-nil archive reader")
		}
	}
	if _, err := s.Describe(); err != nil || s.channel != ChannelStable {
		return result, errors.New("STOP: expected exact Stable selector")
	}
	expectedSHA512, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(s.integritySRI, "sha512-"))
	if err != nil || len(expectedSHA512) != sha512.Size {
		return result, errors.New("STOP: invalid SHA-512 SRI")
	}
	expectedSHA256, err := hex.DecodeString(s.sha256Hex)
	if err != nil || len(expectedSHA256) != sha256.Size {
		return result, errors.New("STOP: invalid SHA-256 expectation")
	}
	h512, h256 := sha512.New(), sha256.New()
	var buffer [32 * 1024]byte
	remaining := s.byteLength
	for remaining > 0 {
		limit := int64(len(buffer))
		if remaining < limit {
			limit = remaining
		}
		n, readErr := reader.Read(buffer[:int(limit)])
		if n < 0 || n > int(limit) {
			return result, errors.New("STOP: reader violated bounded Read contract")
		}
		if n > 0 {
			if err := gentleShellFeedDigests(h512, h256, buffer[:n]); err != nil {
				return result, err
			}
			remaining -= int64(n)
			result.ByteLength += int64(n)
		}
		if readErr != nil && (readErr != io.EOF || remaining != 0) {
			return result, fmt.Errorf("STOP: short/error archive read: %w", readErr)
		}
		if n == 0 {
			return result, errors.New("STOP: zero-progress archive reader")
		}
	}
	var extra [1]byte
	n, readErr := reader.Read(extra[:])
	if n != 0 || readErr != io.EOF {
		return result, errors.New("STOP: extra bytes or uncertain archive EOF")
	}
	result.MeasuredSHA256 = hex.EncodeToString(h256.Sum(nil))
	result.MeasuredSRI = "sha512-" + base64.StdEncoding.EncodeToString(h512.Sum(nil))
	actual512 := h512.Sum(nil)
	actual256 := h256.Sum(nil)
	result.Matched = subtle.ConstantTimeCompare(actual512, expectedSHA512) == 1 &&
		subtle.ConstantTimeCompare(actual256, expectedSHA256) == 1 &&
		result.ByteLength == s.byteLength
	if !result.Matched {
		return result, errors.New("STOP: archive bytes differ from expected length or digests")
	}
	return result, nil // matched source DATA, not publisher or runtime proof
}

func gentleShellFeedDigests(h512, h256 hash.Hash, data []byte) error {
	if _, err := h512.Write(data); err != nil {
		return fmt.Errorf("STOP: SHA-512 write: %w", err)
	}
	if _, err := h256.Write(data); err != nil {
		return fmt.Errorf("STOP: SHA-256 write: %w", err)
	}
	return nil
}

func gentleShellExactStableVersion(value string) bool {
	if len(value) < 5 || len(value) > 64 {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return false
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
	}
	return true
}

func gentleShellCanonicalSRI(value string) bool {
	if len(value) != len("sha512-")+base64.StdEncoding.EncodedLen(sha512.Size) ||
		!strings.HasPrefix(value, "sha512-") {
		return false
	}
	encoded := strings.TrimPrefix(value, "sha512-")
	bytes, err := base64.StdEncoding.Strict().DecodeString(encoded)
	return err == nil && len(bytes) == sha512.Size && base64.StdEncoding.EncodeToString(bytes) == encoded
}

func gentleShellLowerSHA256(value string) bool {
	return gentleShellLowerHex(value, 64)
}

func gentleShellLowerCommit(value string) bool {
	return gentleShellLowerHex(value, 40)
}

func gentleShellLowerHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < '0' || c > '9' && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
