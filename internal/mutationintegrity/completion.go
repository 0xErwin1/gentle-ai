// Package mutationintegrity owns provider-observed implementation completion
// evidence. It does not decide what should be implemented, select verification,
// or authorize delivery.
package mutationintegrity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	CompletionSchemaV1  = "gentle-ai.mutation-completion/v1"
	maximumReadbackSize = int64(1 << 30)
)

var (
	workRunIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	sha256RefPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type StructuralKind string

const (
	StructuralRegular StructuralKind = "regular"
	StructuralAbsent  StructuralKind = "absent"
)

// StructuralReadback is an MMI-owned observation of one intended path after
// implementation and source-mutating normalization have completed.
type StructuralReadback struct {
	Path          string         `json:"path"`
	Kind          StructuralKind `json:"kind"`
	Mode          uint32         `json:"mode,omitempty"`
	Size          int64          `json:"size,omitempty"`
	ContentSHA256 string         `json:"content_sha256,omitempty"`
}

// Completion is an immutable MMI observation. Snapshot and readbacks are
// derived from the live repository; consumers may provide intended paths but
// may not provide candidate identity, hashes, modes, or observed bytes.
type Completion struct {
	Schema        string                     `json:"schema"`
	CompletionRef string                     `json:"completion_ref"`
	RepositoryRef string                     `json:"repository_ref"`
	WorkRunID     string                     `json:"work_run_id"`
	Route         string                     `json:"route"`
	ScopeDigest   string                     `json:"scope_digest"`
	Snapshot      reviewtransaction.Snapshot `json:"snapshot"`
	Readbacks     []StructuralReadback       `json:"readbacks"`
}

type CompleteRequest struct {
	WorkRunID   string
	Route       string
	ScopeDigest string
	Target      reviewtransaction.Target
	// IntendedPaths is the exact owner-admitted implementation scope. MMI
	// rejects both missing intended mutations and additional changed paths.
	IntendedPaths []string
}

// Complete observes the exact post-normalization candidate twice around a
// structural readback. Any mutation during the observation fails closed.
func Complete(
	ctx context.Context,
	lease *reviewtransaction.RepositoryIdentityLease,
	request CompleteRequest,
) (Completion, error) {
	if err := ctx.Err(); err != nil {
		return Completion{}, err
	}
	if lease == nil {
		return Completion{}, errors.New("mutation completion requires a repository identity lease")
	}
	if !workRunIDPattern.MatchString(request.WorkRunID) {
		return Completion{}, errors.New("mutation completion work run identifier is invalid")
	}
	if err := validateToken("route", request.Route); err != nil {
		return Completion{}, err
	}
	if !sha256RefPattern.MatchString(request.ScopeDigest) {
		return Completion{}, errors.New("mutation completion scope digest must be sha256")
	}
	intended, err := canonicalPaths(request.IntendedPaths)
	if err != nil {
		return Completion{}, err
	}
	if len(intended) == 0 {
		return Completion{}, errors.New("mutation completion requires at least one intended path")
	}
	if err := lease.Validate(ctx); err != nil {
		return Completion{}, err
	}
	identity := lease.Identity()
	builder := reviewtransaction.SnapshotBuilder{Repo: identity.RepositoryRoot}
	before, err := builder.Build(ctx, request.Target)
	if err != nil {
		return Completion{}, fmt.Errorf("build mutation completion snapshot: %w", err)
	}
	if !equalStrings(before.Paths, intended) {
		return Completion{}, fmt.Errorf(
			"mutation completion changed paths differ from admitted scope: observed=%v intended=%v",
			before.Paths,
			intended,
		)
	}
	readbacks := make([]StructuralReadback, 0, len(intended))
	for _, logicalPath := range intended {
		readback, err := readbackPath(identity.RepositoryRoot, logicalPath)
		if err != nil {
			return Completion{}, err
		}
		readbacks = append(readbacks, readback)
	}
	if err := lease.Validate(ctx); err != nil {
		return Completion{}, err
	}
	after, err := builder.Build(ctx, request.Target)
	if err != nil {
		return Completion{}, fmt.Errorf("rebuild mutation completion snapshot: %w", err)
	}
	if !reviewtransaction.SnapshotsEqualExact(before, after) {
		return Completion{}, errors.New("candidate mutated during structural readback")
	}
	completion := Completion{
		Schema: CompletionSchemaV1, RepositoryRef: identity.RepositoryRef,
		WorkRunID: request.WorkRunID, Route: request.Route,
		ScopeDigest: request.ScopeDigest, Snapshot: after, Readbacks: readbacks,
	}
	completion.CompletionRef, err = completionDigest(completion)
	if err != nil {
		return Completion{}, err
	}
	if err := completion.Validate(); err != nil {
		return Completion{}, err
	}
	return completion, nil
}

func (completion Completion) Validate() error {
	if completion.Schema != CompletionSchemaV1 {
		return errors.New("unsupported mutation completion schema")
	}
	if !sha256RefPattern.MatchString(completion.CompletionRef) ||
		!sha256RefPattern.MatchString(completion.RepositoryRef) ||
		!workRunIDPattern.MatchString(completion.WorkRunID) ||
		!sha256RefPattern.MatchString(completion.ScopeDigest) {
		return errors.New("mutation completion immutable binding is invalid")
	}
	if err := validateToken("route", completion.Route); err != nil {
		return err
	}
	subject, err := reviewtransaction.VerificationSubjectFromSnapshot(completion.Snapshot)
	if err != nil {
		return fmt.Errorf("mutation completion snapshot: %w", err)
	}
	if subject.SnapshotIdentity != completion.Snapshot.Identity {
		return errors.New("mutation completion snapshot identity mismatch")
	}
	if len(completion.Readbacks) == 0 ||
		len(completion.Readbacks) != len(completion.Snapshot.Paths) {
		return errors.New("mutation completion readbacks do not cover the snapshot")
	}
	paths := make([]string, len(completion.Readbacks))
	for index, readback := range completion.Readbacks {
		if err := readback.Validate(); err != nil {
			return err
		}
		paths[index] = readback.Path
	}
	if !equalStrings(paths, completion.Snapshot.Paths) {
		return errors.New("mutation completion readbacks differ from snapshot paths")
	}
	want, err := completionDigest(completion)
	if err != nil {
		return err
	}
	if completion.CompletionRef != want {
		return errors.New("mutation completion reference does not match its content")
	}
	return nil
}

func (readback StructuralReadback) Validate() error {
	if _, err := canonicalPath(readback.Path); err != nil {
		return err
	}
	switch readback.Kind {
	case StructuralRegular:
		if readback.Mode > 0o777 || readback.Size < 0 ||
			!sha256RefPattern.MatchString(readback.ContentSHA256) {
			return errors.New("regular structural readback is invalid")
		}
	case StructuralAbsent:
		if readback.Mode != 0 || readback.Size != 0 || readback.ContentSHA256 != "" {
			return errors.New("absent structural readback carries file state")
		}
	default:
		return fmt.Errorf("unsupported structural readback kind %q", readback.Kind)
	}
	return nil
}

func readbackPath(root, logicalPath string) (StructuralReadback, error) {
	canonical, err := canonicalPath(logicalPath)
	if err != nil {
		return StructuralReadback{}, err
	}
	target := filepath.Join(root, filepath.FromSlash(canonical))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return StructuralReadback{}, errors.New("structural readback escaped repository root")
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return StructuralReadback{Path: canonical, Kind: StructuralAbsent}, nil
	}
	if err != nil {
		return StructuralReadback{}, fmt.Errorf("inspect structural path %q: %w", canonical, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return StructuralReadback{}, fmt.Errorf(
			"structural path %q must be a regular file or absent",
			canonical,
		)
	}
	if info.Size() > maximumReadbackSize {
		return StructuralReadback{}, fmt.Errorf("structural path %q exceeds readback limit", canonical)
	}
	file, err := os.Open(target)
	if err != nil {
		return StructuralReadback{}, fmt.Errorf("open structural path %q: %w", canonical, err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(file, maximumReadbackSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return StructuralReadback{}, fmt.Errorf("hash structural path %q: %w", canonical, copyErr)
	}
	if closeErr != nil {
		return StructuralReadback{}, fmt.Errorf("close structural path %q: %w", canonical, closeErr)
	}
	return StructuralReadback{
		Path: canonical, Kind: StructuralRegular,
		Mode: uint32(info.Mode().Perm()), Size: info.Size(),
		ContentSHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func completionDigest(completion Completion) (string, error) {
	preimage := completion
	preimage.CompletionRef = ""
	payload, err := json.Marshal(preimage)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("gentle-ai.mutation-completion/v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func canonicalPaths(paths []string) ([]string, error) {
	result := make([]string, len(paths))
	for index, value := range paths {
		canonical, err := canonicalPath(value)
		if err != nil {
			return nil, err
		}
		result[index] = canonical
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, fmt.Errorf("duplicate intended path %q", result[index])
		}
	}
	return result, nil
}

func canonicalPath(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) ||
		strings.ContainsAny(value, "\r\n\x00") ||
		filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return "", errors.New("structural path must be a canonical repository-relative path")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		clean != value {
		return "", errors.New("structural path must be a canonical repository-relative path")
	}
	return clean, nil
}

func validateToken(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 240 ||
		strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s must be a canonical token", name)
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
