package reviewtransaction

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Correction scope admission (issue #3375).
//
// A bounded correction used to be confined to the frozen genesis manifest:
// every path it touched had to already be one the lenses reviewed. That rule
// is not arbitrary. It is what keeps three separate invariants true at once:
//
//   - Reviewers only ever inspected the genesis paths, so a path outside them
//     entered the approved candidate having been judged by nobody. The same
//     principle already demotes an out-of-genesis severe finding to
//     CausalUnknown (findingLocationInGenesis, compact.go).
//   - The receipt's PathsDigest is derived from that manifest, and every
//     delivery gate re-proves the delivered paths against it. A path that
//     entered behind the manifest's back would either be refused at delivery
//     or, worse, ride along inside a receipt that never named it.
//   - The pre-PR publication range check ("nothing unreviewed rides along in
//     intermediate commits", prepr.go) is stated in the same terms.
//
// So the guard cannot simply be deleted. But it also made the single bounded
// correction structurally unable to satisfy the single most common blocking
// finding a reviewer produces: "this behaviour has no test able to execute
// it". The only remedy for that finding is a new test file, a new path, and
// therefore a scope_changed recovery plus a second full review — a cascade
// caused by the rule rather than by the code.
//
// This file narrows the guard instead of removing it. A correction may add a
// path only when that path is BOTH:
//
//  1. recognized as a test path under the closed convention table below, and
//  2. a companion of an already-reviewed genesis path — living in a reviewed
//     directory, or in a test directory immediately inside or immediately
//     beside one.
//
// Everything else keeps today's exact behaviour and still requires recovery.
// The line budget is untouched: an added file's lines are correction lines
// like any other, so the blast radius stays bounded by the same number. The
// single-correction rule is untouched. And an added path is never silent: it
// is persisted on the authority as CorrectionAddedPaths and disclosed on the
// terminal receipt, so an auditor can always see exactly which delivered
// paths no lens inspected.
//
// The honest limit of rule 1 is that "is a test file" is a naming convention
// in every ecosystem except Go, where the compiler enforces it. The table is
// deliberately closed and conservative: an unrecognized name is refused, not
// guessed at, so the failure mode is the status quo (recovery) rather than an
// unreviewed file slipping into an approved candidate.

// correctionTestDirectoryNames is the closed set of directory names a
// repository conventionally reserves for tests.
var correctionTestDirectoryNames = map[string]struct{}{
	"test": {}, "tests": {}, "__tests__": {}, "spec": {}, "specs": {},
}

func correctionTestDirectoryName(segment string) bool {
	_, ok := correctionTestDirectoryNames[strings.ToLower(segment)]
	return ok
}

// correctionTestFileName reports whether one base name is a test file under
// the closed convention table.
func correctionTestFileName(base string) bool {
	stem := strings.TrimSuffix(base, path.Ext(base))
	if stem == "" {
		return false
	}
	lower := strings.ToLower(stem)
	// pytest and unittest: test_calc.py.
	if strings.HasPrefix(lower, "test_") {
		return true
	}
	// Go, Rust, Ruby, Elixir, and the JS/TS family: widget_test.go,
	// calc_spec.rb, service.test.ts, service.spec.ts, service-test.js.
	for _, marker := range []string{"_test", "_spec", ".test", ".spec", "-test", "-spec"} {
		if strings.HasSuffix(lower, marker) {
			return true
		}
	}
	// .NET, Java, and Kotlin: CalcTests.cs, WidgetTest.java. The capital T is
	// load-bearing rather than incidental: it is the CamelCase word boundary
	// that separates a real suffix from an ordinary word merely ending in the
	// same letters, so "latest.go" and "protest.go" stay production files.
	for _, marker := range []string{"Test", "Tests"} {
		if strings.HasSuffix(stem, marker) && len(stem) > len(marker) {
			return true
		}
	}
	return false
}

// correctionTestPath reports whether a repository-relative path is a test
// path, either by its own name or by living under a recognized test
// directory.
func correctionTestPath(candidate string) bool {
	if directory := path.Dir(candidate); directory != "." {
		for _, segment := range strings.Split(directory, "/") {
			if correctionTestDirectoryName(segment) {
				return true
			}
		}
	}
	return correctionTestFileName(path.Base(candidate))
}

// correctionCompanionDirectory reports whether a test living in addedDir is
// plausibly evidence about a reviewed file in genesisDir. The three accepted
// shapes are the ones every mainstream layout uses: the same directory
// (Go, JS/TS co-located), a test directory immediately inside the reviewed
// one, and a test directory immediately beside it.
func correctionCompanionDirectory(addedDir, genesisDir string) bool {
	if addedDir == genesisDir {
		return true
	}
	if !correctionTestDirectoryName(path.Base(addedDir)) {
		return false
	}
	parent := path.Dir(addedDir)
	return parent == genesisDir || parent == path.Dir(genesisDir)
}

// correctionAddedPathAdmissible reports whether one path a correction adds
// beyond the frozen genesis scope may enter the reviewed candidate.
func correctionAddedPathAdmissible(added string, genesis []string) bool {
	if !correctionTestPath(added) {
		return false
	}
	directory := path.Dir(added)
	for _, reviewed := range genesis {
		if correctionCompanionDirectory(directory, path.Dir(reviewed)) {
			return true
		}
	}
	return false
}

// admitCorrectionScope returns the canonical paths a correction adds beyond
// the frozen genesis scope. It answers nil for a correction that stays inside
// genesis — byte-for-byte the pathsAreSubset outcome that preceded it — and
// refuses, naming the exact path, as soon as one addition is inadmissible.
func admitCorrectionScope(paths, genesis []string) ([]string, error) {
	canonicalCandidate, err := canonicalPaths(paths)
	if err != nil || !equalStrings(canonicalCandidate, paths) {
		// refusal:by-design world-action: a non-canonical snapshot path set is derived by native Git, so its repair belongs to code or storage, not to an operator command
		return nil, errors.New("snapshot paths must be canonical")
	}
	canonicalGenesis, err := canonicalPaths(genesis)
	if err != nil || !equalStrings(canonicalGenesis, genesis) {
		// refusal:by-design world-action: the frozen genesis manifest is persisted authority, so a non-canonical one requires code or storage repair
		return nil, errors.New("genesis snapshot paths must be canonical")
	}
	reviewed := make(map[string]struct{}, len(genesis))
	for _, item := range genesis {
		reviewed[item] = struct{}{}
	}
	var added []string
	for _, item := range paths {
		if _, ok := reviewed[item]; ok {
			continue
		}
		if !correctionAddedPathAdmissible(item, genesis) {
			// refusal:by-design operator-knowledge: only the human writing the correction can decide whether to keep it inside the reviewed manifest or take the recovery route, and no command can make that choice for them
			return nil, fmt.Errorf("correction path %q is outside immutable genesis scope", item)
		}
		added = append(added, item)
	}
	return added, nil
}

// correctionScopeRefused is the boolean spelling of admitCorrectionScope, for
// the composite conditions that only need to know whether the paths were
// refused.
func correctionScopeRefused(paths, genesis []string) bool {
	_, err := admitCorrectionScope(paths, genesis)
	return err != nil
}

// CorrectionScopePaths is the path set an approved candidate may actually
// deliver: the frozen genesis manifest plus any companion test paths one
// admitted bounded correction added. It is identical to GenesisPaths for
// every authority that never added one, which is every authority written
// before issue #3375 and every correction that stays inside the manifest.
//
// Genesis itself is never widened. GenesisPaths remains exactly what the
// lenses inspected, because that is what finding causality is judged against.
func (state CompactState) CorrectionScopePaths() []string {
	if len(state.CorrectionAddedPaths) == 0 {
		return state.GenesisPaths
	}
	scope, err := canonicalPaths(append(append([]string(nil), state.GenesisPaths...), state.CorrectionAddedPaths...))
	if err != nil {
		// Fail closed: an authority whose persisted scope cannot be
		// canonicalized delivers under the narrower reviewed manifest.
		return state.GenesisPaths
	}
	return scope
}

// validateCompactCorrectionAddedPaths keeps the widened scope from becoming a
// forgery surface: the field is meaningful only alongside a real completed
// correction, may never restate a genesis path, and every entry must still
// satisfy the same admission the correction had to pass.
func validateCompactCorrectionAddedPaths(state CompactState) error {
	if len(state.CorrectionAddedPaths) == 0 {
		return nil
	}
	canonical, err := canonicalPaths(state.CorrectionAddedPaths)
	if err != nil || !equalStrings(canonical, state.CorrectionAddedPaths) {
		// refusal:by-design world-action: persisted authority that cannot be canonicalized requires code or storage repair
		return errors.New("compact correction added paths must be canonical")
	}
	if len(state.CorrectionAttempts) == 0 && state.ActualCorrectionLines == nil {
		// refusal:by-design world-action: a widened scope with no correction behind it is contradictory persisted authority and cannot be repaired by an operator command
		return errors.New("compact correction added paths require a completed correction")
	}
	for _, added := range state.CorrectionAddedPaths {
		if stringIndex(state.GenesisPaths, added) >= 0 {
			// refusal:by-design world-action: an added path restating the frozen manifest is contradictory persisted authority and requires code or storage repair
			return fmt.Errorf("compact correction added path %q is already inside the frozen genesis scope", added)
		}
		if !correctionAddedPathAdmissible(added, state.GenesisPaths) {
			// refusal:by-design world-action: persisted authority claiming a scope this contract never admits cannot be repaired by an operator command
			return fmt.Errorf("compact correction added path %q is not an admissible companion test path", added)
		}
	}
	return nil
}
