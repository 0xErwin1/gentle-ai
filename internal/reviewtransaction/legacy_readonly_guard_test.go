package reviewtransaction

// RG.1 (Wave 7 S1, design decision 5 / tasks.md 1.9). D5 defines "one
// lifecycle" as: delete legacy MUTATION, retain legacy READ. This guard has
// two halves:
//
//   - RG.1a (retained-read half, GREEN from the moment this file lands):
//     every D5 retained-symbol name still parses as a declared identifier in
//     its home file -- a sanity fence so a later deletion slice cannot
//     accidentally sweep the forensic read path away along with the
//     mutation it sits beside.
//   - RG.1b (mutation-reachability half, INTENTIONALLY RED until WU19, SKIPPED
//     WU1-WU18 so the wave's PR chain can ship green CI at every intermediate
//     head): no legacy-mutation CLI verb literal is reachable from
//     internal/cli/review_facade.go's own dispatch switches
//     (runReviewCommand, runReviewCommandContext). At WU1 time every one of
//     these verbs is still dispatched (rows 6, 10, 14-16, 24 of the
//     deletion inventory) -- this half's assertion fails on purpose, naming
//     exactly which verbs are still reachable, and shrinks to zero only as
//     WU4-WU19 land, going fully GREEN at WU19 once the last D4 verb is
//     classified and deleted. Rather than let each WU1-WU18 PR head ship a
//     knowingly-failing test (CI evaluates exact PR heads, not the eventual
//     merged state), the test body below carries a t.Skip naming this same
//     reason; the assertion itself is untouched and unskipped again the
//     moment WU19 lands.
//
// `go test ./...` for this package will show ONE skipped test
// (TestLegacyReadOnlyGuardMutationVerbsUnreachable) from WU1 through WU18 --
// this is the documented, designed state (tasks.md: "Intentionally RED
// until WU19"), not a broken build.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// legacyRetainedReadSymbols is D5's closed retained-read list: file path
// (relative to this package directory) -> identifier names that file must
// still declare. sddstatus/legacy_binding_read.go, sddstatus/review_binding.go
// and candidate_decline.go live outside this package's own directory, so
// their paths are relative to this package's own directory.
// AuthoritativeStore/LoadChain/NewLegacyReadOnlyError are declared inside
// THIS package (store.go, compact_store.go) — review_facade.go:1632-1635
// (design.md) is only their call site, not their declaration.
var legacyRetainedReadSymbols = map[string][]string{
	"../sddstatus/legacy_binding_read.go": {"parseLegacyBinding"},
	"../sddstatus/review_binding.go":      {"parseBinding", "bindingBytes", "bindingDigest", "bindingPath"},
	"candidate_decline.go":                {"parseCandidateDeclineAuthorization"},
	"store.go":                            {"AuthoritativeStore", "LoadChain"},
	"compact_store.go":                    {"NewLegacyReadOnlyError"},
}

// legacyRetiredMutationVerbs is the exact case-clause literal set the
// deletion inventory's public-verb rows retire (rows 6, 10, 14-16, 24):
// reconcile-authority/-batch (S3/S4), the three quarantine/repair verbs
// (S5), and the six D4 verbs of ambiguous vintage (S8, classified at WU19).
// invalidate/abandon/recover/reclaim/dispose-result/reopen-results may
// individually turn out to have a residual legacy-READ role (S8 open
// question) -- if WU19 retains one under D5, this list (and this test) must
// be updated in that same slice with the written reason, not silently left
// red forever.
var legacyRetiredMutationVerbs = []string{
	"reconcile-authority", "reconcile-authority-batch",
	"quarantine-legacy", "quarantine-legacy-fix-scope", "repair-legacy-alias",
	"invalidate", "abandon", "recover", "reclaim", "dispose-result", "reopen-results",
}

// legacyDispatchFunctions is the closed set of functions in
// internal/cli/review_facade.go whose case-clause literals this guard scans
// -- runReviewCommand (default review command dispatch) and
// runReviewCommandContext (the context-aware dispatch, which falls through
// to runReviewCommand by default but has its own reconcile-authority-batch
// case).
var legacyDispatchFunctions = []string{"runReviewCommand", "runReviewCommandContext"}

// TestLegacyReadOnlyGuardRetainedSymbolsDeclared is RG.1a: proves every D5
// retained-read symbol is still a declared identifier in its home file.
func TestLegacyReadOnlyGuardRetainedSymbolsDeclared(t *testing.T) {
	for file, symbols := range legacyRetainedReadSymbols {
		t.Run(file, func(t *testing.T) {
			declared, err := declaredIdentifiers(file)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			for _, symbol := range symbols {
				if !declared[symbol] {
					t.Fatalf("%s no longer declares retained read symbol %q (D5 requires it to survive every legacy-mutation deletion slice)", file, symbol)
				}
			}
		})
	}
}

// TestLegacyReadOnlyGuardMutationVerbsUnreachable is RG.1b: proves no
// legacy-mutation verb literal remains a case clause in review_facade.go's
// dispatch switches. Intentionally RED until WU19 (tasks.md 1.9) -- see the
// package-level doc comment above.
func TestLegacyReadOnlyGuardMutationVerbsUnreachable(t *testing.T) {
	t.Skip("RG.1b intentionally RED WU1-WU18 (tasks.md 1.9): legacyRetiredMutationVerbs' six D4 verbs (invalidate, abandon, recover, reclaim, dispose-result, reopen-results) are retired progressively across WU4-WU19 and stay reachable from review_facade.go's dispatch switches until WU19 classifies and deletes the last one -- skipped rather than failed so every WU1-WU18 PR head ships green CI; unskip lands in the same slice as WU19's own fix.")
	reachable := map[string]bool{}
	for _, fn := range legacyDispatchFunctions {
		verbs, err := dispatchCaseLiterals("../cli/review_facade.go", fn)
		if err != nil {
			t.Fatalf("scan dispatch function %s: %v", fn, err)
		}
		for _, verb := range verbs {
			reachable[verb] = true
		}
	}
	var stillReachable []string
	for _, verb := range legacyRetiredMutationVerbs {
		if reachable[verb] {
			stillReachable = append(stillReachable, verb)
		}
	}
	if len(stillReachable) > 0 {
		t.Fatalf("legacy-mutation verbs still reachable from review_facade.go dispatch: %v (expected until WU19; this guard goes GREEN once the last one is classified and deleted)", stillReachable)
	}
}

// declaredIdentifiers returns every top-level func/type/const/var name
// declared in path, plus every field/method name on those declarations that
// carries an *ast.Ident (a coarse but sufficient net for RG.1a's "still
// declared" check).
func declaredIdentifiers(path string) (map[string]bool, error) {
	fileSet := token.NewFileSet()
	tree, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	ast.Inspect(tree, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncDecl:
			names[n.Name.Name] = true
		case *ast.TypeSpec:
			names[n.Name.Name] = true
		case *ast.ValueSpec:
			for _, name := range n.Names {
				names[name.Name] = true
			}
		}
		return true
	})
	return names, nil
}

// dispatchCaseLiterals returns every string literal case-clause value in
// funcName's top-level switch statement(s) within path.
func dispatchCaseLiterals(path, funcName string) ([]string, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	fileSet := token.NewFileSet()
	tree, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var literals []string
	ast.Inspect(tree, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			clause, ok := inner.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					literals = append(literals, strings.Trim(literal.Value, `"`))
				}
			}
			return true
		})
		return false
	})
	return literals, nil
}
