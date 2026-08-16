package sddstatus

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// verifyReportFileName is the single canonical final-verification report an
// active OpenSpec change owns.
const verifyReportFileName = "verify-report.md"

// canonicalVerifyReportPaths proves that the change's final-verification
// report is the one canonical report its planning workspace owns, and returns
// the repository-relative slash path the settled candidate tree addresses it
// by.
//
// Every anchor is canonicalized here, through exactly the resolution
// resolveBindingChangeRoot already applied to the anchors it derived changeRoot
// from. Canonicalizing one operand and comparing it lexically against the raw
// other operand is the defect this exists to remove: a raw --cwd and a resolved
// change root are two spellings of one directory, and comparing spellings
// answers about the strings instead of about the directory.
//
// That is what broke the Windows suite. GetTempPath hands out the process
// temporary directory in its 8.3 short form (C:\Users\RUNNER~1\AppData\...),
// so filepath.Abs of --cwd kept the short spelling while
// filepath.EvalSymlinks expanded every component to its long, real-case name.
// filepath.Rel then walked out of the workspace with ".." and the change's own
// canonical report was refused as foreign, which projected as "current
// repository target no longer matches the reviewed scope". The same divergence
// exists wherever one directory has two spellings: a symlinked ancestor on
// POSIX, /var and /private/var on macOS.
func canonicalVerifyReportPaths(repo, workspace, changeRoot, change string) (repositoryRelative string, err error) {
	canonicalRepo, err := canonicalBindingPath(repo)
	if err != nil {
		return "", err
	}
	canonicalWorkspace, err := canonicalBindingPath(workspace)
	if err != nil {
		return "", err
	}
	canonicalChangeRoot, err := canonicalBindingPath(changeRoot)
	if err != nil {
		return "", err
	}
	return verifyReportPathUnderAnchors(
		filepath.ToSlash(canonicalRepo),
		filepath.ToSlash(canonicalWorkspace),
		filepath.ToSlash(canonicalChangeRoot),
		change,
	)
}

// verifyReportPathUnderAnchors is the pure decision behind that anchoring. It
// never touches the filesystem, so the whole path algebra is exercisable from
// any platform against any platform's spellings -- which is the only way this
// class of defect is catchable without a Windows runner.
//
// Every operand must already be an absolute path in slash form (the caller's
// filepath.ToSlash) and in one shared canonical spelling. This answers
// containment, never identity: it does not fold case, because on POSIX two
// differently-cased paths are genuinely two directories, and it does not
// resolve links, because the caller resolved every operand the same way before
// asking.
func verifyReportPathUnderAnchors(repo, workspace, changeRoot, change string) (repositoryRelative string, err error) {
	reportPath := path.Join(changeRoot, verifyReportFileName)
	canonical := path.Join("openspec", "changes", change, verifyReportFileName)
	workspaceRelative, contained := relativePathUnder(workspace, reportPath)
	if !contained || workspaceRelative != canonical {
		return "", fmt.Errorf("final verification report is not the canonical active-change path %q", canonical) // refusal:by-design world-action: a report outside the canonical active change cannot be safely attested
	}
	repositoryRelative, contained = relativePathUnder(repo, reportPath)
	if !contained {
		return "", errors.New("final verification report resolves outside the repository root") // refusal:by-design world-action: a report outside the repository has no path the settled candidate tree can be read by
	}
	return repositoryRelative, nil
}

// relativePathUnder reports target's slash-separated position beneath anchor.
//
// Both operands are cleaned the same way, so a trailing separator on either
// side is not a difference. The prefix is matched on whole components, so a
// sibling whose name merely starts with the anchor's name ("/repo-2" under
// "/repo") is not contained, and the result never escapes the anchor with
// "..": a target outside the anchor is refused rather than described.
func relativePathUnder(anchor, target string) (string, bool) {
	anchor = path.Clean(anchor)
	target = path.Clean(target)
	if anchor == target {
		return ".", true
	}
	if !strings.HasSuffix(anchor, "/") {
		anchor += "/"
	}
	relative, contained := strings.CutPrefix(target, anchor)
	if !contained || relative == "" {
		return "", false
	}
	return relative, true
}
