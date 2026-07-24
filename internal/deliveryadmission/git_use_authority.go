package deliveryadmission

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maximumDeliveryGitControlBytes = 64 << 10

type deliveryGitIdentity struct {
	repositoryRoot string
	commonDir      string
	repositoryInfo os.FileInfo
	commonInfo     os.FileInfo
}

type deliveryGitControlSnapshot struct {
	controlInfo       os.FileInfo
	controlPayload    []byte
	gitDir            string
	gitDirInfo        os.FileInfo
	commonControlInfo os.FileInfo
	commonPayload     []byte
	commonDir         string
	commonInfo        os.FileInfo
}

var deliveryGitCommandContext = exec.CommandContext

func resolveDeliveryGitIdentity(
	ctx context.Context,
	authorityRoot string,
) (deliveryGitIdentity, error) {
	if !filepath.IsAbs(authorityRoot) || filepath.Clean(authorityRoot) != authorityRoot {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: repository authority must return an absolute clean root", ErrInvalid,
		)
	}
	canonicalRoot, err := filepath.EvalSymlinks(authorityRoot)
	if err != nil {
		return deliveryGitIdentity{}, fmt.Errorf("resolve delivery repository root: %w", err)
	}
	if filepath.Clean(canonicalRoot) != authorityRoot {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: repository authority root contains a symlink or reparse point", ErrUntrustedPreimage,
		)
	}
	rootBefore, err := os.Lstat(authorityRoot)
	if err != nil || !rootBefore.IsDir() || rootBefore.Mode()&os.ModeSymlink != 0 {
		if err != nil {
			return deliveryGitIdentity{}, fmt.Errorf("inspect delivery repository root: %w", err)
		}
		return deliveryGitIdentity{}, fmt.Errorf("%w: delivery repository root", ErrUntrustedPreimage)
	}

	first, err := queryDeliveryGitIdentity(ctx, authorityRoot)
	if err != nil {
		return deliveryGitIdentity{}, err
	}
	if first.repositoryRoot != authorityRoot {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: repository authority does not resolve the exact Git root", ErrBindingMismatch,
		)
	}
	commonCanonical, err := filepath.EvalSymlinks(first.commonDir)
	if err != nil {
		return deliveryGitIdentity{}, fmt.Errorf("resolve Git common directory: %w", err)
	}
	if filepath.Clean(commonCanonical) != first.commonDir {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: Git common directory contains a symlink or reparse point", ErrUntrustedPreimage,
		)
	}
	commonBefore, err := os.Lstat(first.commonDir)
	if err != nil || !commonBefore.IsDir() || commonBefore.Mode()&os.ModeSymlink != 0 {
		if err != nil {
			return deliveryGitIdentity{}, fmt.Errorf("inspect Git common directory: %w", err)
		}
		return deliveryGitIdentity{}, fmt.Errorf("%w: Git common directory", ErrUntrustedPreimage)
	}

	second, err := queryDeliveryGitIdentity(ctx, authorityRoot)
	if err != nil {
		return deliveryGitIdentity{}, err
	}
	rootAfter, rootErr := os.Lstat(authorityRoot)
	commonAfter, commonErr := os.Lstat(first.commonDir)
	if rootErr != nil || commonErr != nil {
		if rootErr != nil {
			return deliveryGitIdentity{}, fmt.Errorf("reinspect delivery repository root: %w", rootErr)
		}
		return deliveryGitIdentity{}, fmt.Errorf("reinspect Git common directory: %w", commonErr)
	}
	if first.repositoryRoot != second.repositoryRoot ||
		first.commonDir != second.commonDir ||
		!os.SameFile(rootBefore, rootAfter) ||
		!os.SameFile(commonBefore, commonAfter) {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: delivery repository Git identity changed during resolution", ErrBindingMismatch,
		)
	}
	if err := validateDeliveryGitControl(
		authorityRoot,
		first.commonDir,
		commonAfter,
	); err != nil {
		return deliveryGitIdentity{}, err
	}
	first.repositoryInfo = rootAfter
	first.commonInfo = commonAfter
	return first, nil
}

func queryDeliveryGitIdentity(
	ctx context.Context,
	repositoryRoot string,
) (deliveryGitIdentity, error) {
	rootOutput, err := runDeliveryGit(ctx, repositoryRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return deliveryGitIdentity{}, err
	}
	root, err := canonicalGitAuthorityPath(repositoryRoot, rootOutput)
	if err != nil {
		return deliveryGitIdentity{}, fmt.Errorf("parse Git repository root: %w", err)
	}
	commonOutput, err := runDeliveryGit(ctx, repositoryRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return deliveryGitIdentity{}, err
	}
	common, err := canonicalGitAuthorityPath(repositoryRoot, commonOutput)
	if err != nil {
		return deliveryGitIdentity{}, fmt.Errorf("parse Git common directory: %w", err)
	}
	return deliveryGitIdentity{repositoryRoot: root, commonDir: common}, nil
}

func runDeliveryGit(ctx context.Context, repositoryRoot string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"--no-replace-objects", "-C", repositoryRoot}, args...)
	command := deliveryGitCommandContext(ctx, "git", commandArgs...)
	command.Env = sanitizedDeliveryGitEnvironment(os.Environ())
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

func sanitizedDeliveryGitEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+4)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") ||
			strings.EqualFold(name, "LC_ALL") {
			continue
		}
		result = append(result, entry)
	}
	return append(
		result,
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
	)
}

func validateDeliveryGitControl(
	repositoryRoot string,
	expectedCommon string,
	expectedCommonInfo os.FileInfo,
) error {
	first, err := inspectDeliveryGitControl(repositoryRoot)
	if err != nil {
		return err
	}
	second, err := inspectDeliveryGitControl(repositoryRoot)
	if err != nil {
		return err
	}
	if !sameDeliveryGitControlSnapshot(first, second) {
		return fmt.Errorf(
			"%w: Git control relationship changed during resolution",
			ErrBindingMismatch,
		)
	}
	if expectedCommonInfo == nil || !os.SameFile(first.commonInfo, expectedCommonInfo) ||
		!sameCanonicalDeliveryGitPath(first.commonDir, expectedCommon) {
		return fmt.Errorf(
			"%w: Git common directory does not match the repository control entry",
			ErrBindingMismatch,
		)
	}
	return nil
}

func inspectDeliveryGitControl(repositoryRoot string) (deliveryGitControlSnapshot, error) {
	controlPath := filepath.Join(repositoryRoot, ".git")
	controlInfo, err := os.Lstat(controlPath)
	if err != nil || controlInfo.Mode()&os.ModeSymlink != 0 {
		if err != nil {
			return deliveryGitControlSnapshot{}, fmt.Errorf("inspect Git control entry: %w", err)
		}
		return deliveryGitControlSnapshot{}, fmt.Errorf(
			"%w: Git control entry is a symlink or reparse point",
			ErrUntrustedPreimage,
		)
	}
	snapshot := deliveryGitControlSnapshot{controlInfo: controlInfo}
	gitDir := controlPath
	switch {
	case controlInfo.IsDir():
	case controlInfo.Mode().IsRegular():
		snapshot.controlPayload, err = readBoundedDeliveryGitControl(controlPath)
		if err != nil {
			return deliveryGitControlSnapshot{}, err
		}
		gitDir, err = parseDeliveryGitDir(repositoryRoot, snapshot.controlPayload)
		if err != nil {
			return deliveryGitControlSnapshot{}, err
		}
	default:
		return deliveryGitControlSnapshot{}, fmt.Errorf(
			"%w: Git control entry has an unsupported type",
			ErrUntrustedPreimage,
		)
	}
	snapshot.gitDir, snapshot.gitDirInfo, err = canonicalDeliveryGitControlDirectory(gitDir)
	if err != nil {
		return deliveryGitControlSnapshot{}, fmt.Errorf("resolve Git directory control: %w", err)
	}

	commonControlPath := filepath.Join(snapshot.gitDir, "commondir")
	commonDir := snapshot.gitDir
	snapshot.commonControlInfo, err = os.Lstat(commonControlPath)
	switch {
	case err == nil:
		if snapshot.commonControlInfo.Mode()&os.ModeSymlink != 0 ||
			!snapshot.commonControlInfo.Mode().IsRegular() {
			return deliveryGitControlSnapshot{}, fmt.Errorf(
				"%w: Git commondir control has an unsupported type",
				ErrUntrustedPreimage,
			)
		}
		snapshot.commonPayload, err = readBoundedDeliveryGitControl(commonControlPath)
		if err != nil {
			return deliveryGitControlSnapshot{}, err
		}
		commonDir, err = parseDeliveryGitCommonDir(snapshot.gitDir, snapshot.commonPayload)
		if err != nil {
			return deliveryGitControlSnapshot{}, err
		}
	case os.IsNotExist(err):
		snapshot.commonControlInfo = nil
	default:
		return deliveryGitControlSnapshot{}, fmt.Errorf("inspect Git commondir control: %w", err)
	}
	snapshot.commonDir, snapshot.commonInfo, err = canonicalDeliveryGitControlDirectory(commonDir)
	if err != nil {
		return deliveryGitControlSnapshot{}, fmt.Errorf("resolve Git common-dir control: %w", err)
	}
	return snapshot, nil
}

func parseDeliveryGitDir(repositoryRoot string, payload []byte) (string, error) {
	line, err := singleDeliveryGitControlLine(payload)
	if err != nil || !strings.HasPrefix(line, "gitdir: ") {
		return "", fmt.Errorf("%w: malformed .git control file", ErrUntrustedPreimage)
	}
	value := strings.TrimPrefix(line, "gitdir: ")
	if value == "" || strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("%w: malformed .git directory", ErrUntrustedPreimage)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(repositoryRoot, value)
	}
	return filepath.Clean(value), nil
}

func parseDeliveryGitCommonDir(gitDir string, payload []byte) (string, error) {
	value, err := singleDeliveryGitControlLine(payload)
	if err != nil || value == "" || strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("%w: malformed Git commondir control", ErrUntrustedPreimage)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(gitDir, value)
	}
	return filepath.Clean(value), nil
}

func singleDeliveryGitControlLine(payload []byte) (string, error) {
	if len(payload) == 0 || bytes.IndexByte(payload, 0) >= 0 {
		return "", fmt.Errorf("%w: malformed Git control payload", ErrUntrustedPreimage)
	}
	payload = bytes.TrimSuffix(payload, []byte{'\n'})
	payload = bytes.TrimSuffix(payload, []byte{'\r'})
	if len(payload) == 0 || bytes.ContainsAny(payload, "\r\n") {
		return "", fmt.Errorf("%w: ambiguous Git control payload", ErrUntrustedPreimage)
	}
	return string(payload), nil
}

func readBoundedDeliveryGitControl(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Size() > maximumDeliveryGitControlBytes {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: Git control payload byte bound", ErrUntrustedPreimage)
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximumDeliveryGitControlBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 || len(payload) > maximumDeliveryGitControlBytes ||
		int64(len(payload)) != info.Size() {
		return nil, fmt.Errorf("%w: Git control payload changed during read", ErrUntrustedPreimage)
	}
	return payload, nil
}

func canonicalDeliveryGitControlDirectory(path string) (string, os.FileInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	absolute = filepath.Clean(absolute)
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", nil, err
	}
	if filepath.Clean(canonical) != absolute {
		return "", nil, fmt.Errorf(
			"%w: Git control directory contains a symlink or reparse point",
			ErrUntrustedPreimage,
		)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf(
			"%w: Git control path is not a directory",
			ErrUntrustedPreimage,
		)
	}
	return absolute, info, nil
}

func sameDeliveryGitControlSnapshot(
	left deliveryGitControlSnapshot,
	right deliveryGitControlSnapshot,
) bool {
	return sameDeliveryGitFileIdentity(left.controlInfo, right.controlInfo) &&
		bytes.Equal(left.controlPayload, right.controlPayload) &&
		sameDeliveryGitFileIdentity(left.gitDirInfo, right.gitDirInfo) &&
		sameOptionalDeliveryGitFileIdentity(
			left.commonControlInfo,
			right.commonControlInfo,
		) &&
		bytes.Equal(left.commonPayload, right.commonPayload) &&
		sameDeliveryGitFileIdentity(left.commonInfo, right.commonInfo) &&
		sameCanonicalDeliveryGitPath(left.gitDir, right.gitDir) &&
		sameCanonicalDeliveryGitPath(left.commonDir, right.commonDir)
}

func sameOptionalDeliveryGitFileIdentity(left, right os.FileInfo) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return os.SameFile(left, right)
}

func sameDeliveryGitFileIdentity(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right)
}

func sameCanonicalDeliveryGitPath(left, right string) bool {
	leftInfo, leftErr := os.Lstat(filepath.Clean(left))
	rightInfo, rightErr := os.Lstat(filepath.Clean(right))
	return leftErr == nil && rightErr == nil &&
		leftInfo.IsDir() && rightInfo.IsDir() &&
		os.SameFile(leftInfo, rightInfo)
}

func canonicalGitAuthorityPath(repositoryRoot string, output []byte) (string, error) {
	if len(output) == 0 || bytes.IndexByte(output, 0) >= 0 {
		return "", fmt.Errorf("%w: malformed Git authority path", ErrUntrustedPreimage)
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) != 1 {
		return "", fmt.Errorf("%w: ambiguous Git authority path", ErrUntrustedPreimage)
	}
	value := strings.TrimSuffix(lines[0], "\r")
	if value == "" || strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("%w: malformed Git authority path", ErrUntrustedPreimage)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(repositoryRoot, value)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}
