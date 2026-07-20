package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	reviewCapturePreflightSchema     = "gentle-ai.review-capture-preflight/v1"
	reviewCapturePreflightCapability = "review.native_capture_preflight"
	reviewIncidentArtifactSchema     = "gentle-ai.review-incident-artifact/v1"
	reviewIncidentArtifactCapability = "review.native_incident_artifact"
)

// reviewCapturePreflightResult confirms that one capture binding matches the
// reviewing authority reachable from the resolved repository root, before a
// bound reviewer consumes its exactly-once invocation.
type reviewCapturePreflightResult struct {
	Schema         string `json:"schema"`
	Capability     string `json:"capability"`
	RepositoryRoot string `json:"repository_root"`
	LineageID      string `json:"lineage_id"`
	TargetIdentity string `json:"target_identity"`
	Lens           string `json:"lens"`
	SelectedOrder  int    `json:"selected_order"`
}

// reviewIncidentArtifact references one durably preserved raw reviewer result.
// Its schema is distinct from the captured-result artifact schema on purpose:
// finalize rejects it, so a preserved incident can never masquerade as a
// verified lens capture.
type reviewIncidentArtifact struct {
	Schema         string                                `json:"schema"`
	Capability     string                                `json:"capability"`
	Path           string                                `json:"path"`
	SHA256         string                                `json:"sha256"`
	LineageID      string                                `json:"lineage_id"`
	TargetIdentity string                                `json:"target_identity"`
	Lens           string                                `json:"lens"`
	SelectedOrder  int                                   `json:"selected_order"`
	Class          reviewtransaction.ResultIncidentClass `json:"class,omitempty"`
}

// RunReviewPreserveResult durably preserves one raw reviewer result as an
// incident artifact beside the compact review authority root after a failed
// capture. It never validates the payload against the reviewing authority and
// never counts as a captured lens result; recovery re-runs
// `review capture-result` with the preserved payload from the reviewing
// repository, which performs full native verification.
func RunReviewPreserveResult(args []string, stdout io.Writer) error {
	flags := newReviewFlagSet("review preserve-result", stdout, "Durably preserve one raw reviewer result as an incident artifact after a failed capture; never a captured lens result.")
	cwd := flags.String("cwd", ".", "repository path")
	lineage := flags.String("lineage", "", "exact review lineage identifier from the capture binding")
	target := flags.String("target", "", "exact frozen target identity from the capture binding")
	lens := flags.String("lens", "", "exact selected lens from the capture binding")
	order := flags.Int("order", -1, "zero-based selected lens order from the capture binding")
	input := flags.String("input", "", "raw reviewer result file or - for stdin")
	class := flags.String("class", "", "extraction-failure classification: empty_result or nested_envelope")
	acquisition := flags.String("acquisition", "", "exact acquisition ID from a prior review acquire-result for this exact binding")
	if err := parseReviewFlags(flags, args); err != nil {
		return err
	}
	if reviewHelpRequested(args) {
		return nil
	}
	if flags.NArg() != 0 || strings.TrimSpace(*input) == "" {
		return reviewPreflightError(errors.New("review preserve-result requires exact --cwd, --lineage, --target, --lens, --order, --input, and --acquisition"))
	}
	switch *lens {
	case reviewtransaction.LensRisk, reviewtransaction.LensResilience, reviewtransaction.LensReadability, reviewtransaction.LensReliability:
	default:
		return reviewPreflightError(fmt.Errorf("review preserve-result requires one exact canonical lens; got %q", *lens))
	}
	if !validReviewCapabilitySHA256(*target) || *order < 0 || *order >= 4 {
		return reviewPreflightError(errors.New("review preserve-result requires the exact frozen target identity and selected lens order"))
	}
	if !reviewtransaction.ValidResultIncidentClass(reviewtransaction.ResultIncidentClass(*class)) {
		return reviewPreflightError(fmt.Errorf("review preserve-result requires --class to be empty or one exact canonical incident class; got %q", *class))
	}
	// Preserve-result durably saves evidence even from a repository that is not
	// the reviewing repository (for example a nested-worktree misconfiguration),
	// so it cannot require the acquisition to resolve against a live compact
	// store the way capture-result and dispose-result do. It requires the exact
	// acquire-result ID shape here as a fail-closed format guard; only the
	// store-bound terminal operations (capture-result, review dispose-result)
	// prove exact binding equality against the live acquisition record.
	if !validReviewAcquisitionID(*acquisition) {
		return reviewPreflightError(fmt.Errorf("review preserve-result requires --acquisition from a prior review acquire-result; got %q", *acquisition))
	}
	dir, err := reviewtransaction.CompactIncidentsDir(context.Background(), *cwd, *lineage)
	if err != nil {
		return reviewPreflightError(fmt.Errorf("resolve incident preservation directory: %w", err))
	}
	payload, err := readFacadeBytes(*input)
	if err != nil {
		return reviewPreflightError(fmt.Errorf("read raw reviewer result: %w", err))
	}
	if len(payload) == 0 || len(payload) > reviewResultArtifactLimit {
		return reviewPreflightError(errors.New("raw reviewer result must be non-empty and within the native result size limit"))
	}
	artifact, err := preserveIncidentArtifact(dir, *lineage, *target, *lens, *order, payload, reviewtransaction.ResultIncidentClass(*class))
	if err != nil {
		return reviewPreflightError(err)
	}
	return encodeReviewJSON(stdout, artifact)
}

func preserveIncidentArtifact(dir, lineage, target, lens string, order int, payload []byte, class reviewtransaction.ResultIncidentClass) (reviewIncidentArtifact, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return reviewIncidentArtifact{}, fmt.Errorf("create incident preservation directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !reviewArtifactModeSafe(info.Mode(), true) {
		return reviewIncidentArtifact{}, errors.New("incident preservation directory is not a private native directory")
	}
	hash := facadePayloadHash(payload)
	digest12 := strings.TrimPrefix(hash, "sha256:")[:12]
	name := fmt.Sprintf("%02d-%s-%s.raw", order, lens, digest12)
	if class != "" {
		name = fmt.Sprintf("%02d-%s-%s-%s.raw", order, lens, class, digest12)
	}
	path := filepath.Join(dir, name)
	artifact := reviewIncidentArtifact{
		Schema: reviewIncidentArtifactSchema, Capability: reviewIncidentArtifactCapability, Path: path,
		SHA256: hash, LineageID: lineage, TargetIdentity: target, Lens: lens, SelectedOrder: order, Class: class,
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if !bytes.Equal(existing, payload) {
			return reviewIncidentArtifact{}, errors.New("incident artifact already exists with different bytes")
		}
		return artifact, nil
	} else if !os.IsNotExist(readErr) {
		return reviewIncidentArtifact{}, readErr
	}
	temp, err := os.CreateTemp(dir, ".incident-*")
	if err != nil {
		return reviewIncidentArtifact{}, fmt.Errorf("create incident temporary file: %w", err)
	}
	owned, _ := temp.Stat()
	defer removeOwnedArtifact(temp.Name(), owned)
	if err := temp.Chmod(0o600); err != nil {
		return reviewIncidentArtifact{}, err
	}
	if _, err := temp.Write(payload); err != nil {
		return reviewIncidentArtifact{}, err
	}
	if err := temp.Sync(); err != nil {
		return reviewIncidentArtifact{}, err
	}
	if err := temp.Close(); err != nil {
		return reviewIncidentArtifact{}, err
	}
	if err := reviewtransaction.PublishFileNoReplace(temp.Name(), path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, payload) {
			return artifact, nil
		}
		return reviewIncidentArtifact{}, fmt.Errorf("publish incident artifact atomically: %w", err)
	}
	if err := syncReviewerArtifactDirectory(dir); err != nil {
		unsupported := errors.Is(err, syscall.EINVAL) || errors.Is(err, errors.ErrUnsupported) ||
			reviewArtifactRuntimeGOOS() == "windows" && errors.Is(err, os.ErrPermission)
		if !unsupported {
			removeOwnedArtifact(path, owned)
			return reviewIncidentArtifact{}, fmt.Errorf("sync incident preservation directory: %w", err)
		}
	}
	if preserved, err := os.ReadFile(path); err != nil || !bytes.Equal(preserved, payload) {
		removeOwnedArtifact(path, owned)
		return reviewIncidentArtifact{}, fmt.Errorf("read back incident artifact: %w", err)
	}
	return artifact, nil
}
