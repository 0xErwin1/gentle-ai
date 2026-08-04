package assets

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// reviewPluginHarness is a Node entry point that loads the embedded OpenCode
// review plugin exactly as OpenCode does and reports the message of whichever
// error the selected hook throws. It exists so the plugin's recovery paths are
// proven by execution, not by reading the source for substrings.
const reviewPluginHarness = `import plugin from "./plugin.mts"

const scenario = process.argv[2]
const cwd = process.argv[3]
const hooks = await plugin({ directory: cwd, worktree: cwd })

const opaque = {
  lens: "review-risk", lineage: "trust-check", order: 0,
  repository_context: "rctx1_" + "a".repeat(64),
  revision: "sha256:" + "b".repeat(64),
  subject_hash: "sha256:" + "c".repeat(64),
  target: "sha256:" + "d".repeat(64),
}
const legacy = { lens: "review-risk", lineage: "trust-check", order: 0, target: "sha256:" + "d".repeat(64) }
const binding = scenario.endsWith("legacy") ? legacy : opaque
let prompt = ` + "`" + `GENTLE_AI_REVIEW_BINDING ${JSON.stringify(binding)}\nreview the frozen candidate\n` + "`" + `
if (scenario === "before-substitute") prompt += ` + "`" + `base_tree=${"9".repeat(40)} candidate_tree=${"8".repeat(40)} changed_path_manifest=[{"path":"caller.txt"}]\n` + "`" + `
if (scenario === "before-missing") prompt = "review the frozen candidate\n"
if (scenario === "before-equals") prompt = ` + "`" + `GENTLE_AI_REVIEW_BINDING=${JSON.stringify(binding)}\nreview the frozen candidate\n` + "`" + `
if (scenario === "before-malformed") prompt = "GENTLE_AI_REVIEW_BINDING {not-json}\nreview the frozen candidate\n"

const capture = async (activeHooks: typeof hooks, sessionID: string, marker: string, boundPrompt: string = prompt) => {
  const input = { tool: "task", sessionID, callID: "call-" + marker, args: { subagent_type: "review-risk", prompt: boundPrompt } }
  const incomplete = '{"subject_hash":"sha256:' + "c".repeat(64) + '","inspection":{"status":"incomplete","paths":[]},"findings":[],"evidence":["` + reviewPluginPayloadMarker + `"]}'
  const output = { title: "", output: marker === "incomplete" ? incomplete : '{"subject_hash":"sha256:' + "c".repeat(64) + '","findings":[],"evidence":["' + marker + '"]}', metadata: {} }
  try {
    await activeHooks["tool.execute.after"](input, output)
    return output.output
  } catch (cause: unknown) {
    return cause instanceof Error ? cause.message : String(cause)
  }
}

try {
  if (scenario.startsWith("before")) {
    const output = { args: { subagent_type: "review-risk", prompt } }
    await hooks["tool.execute.before"]({ tool: "task", sessionID: "session-a", callID: "call-before" }, output)
    console.log(scenario === "before-valid" || scenario === "before-substitute" ? output.args.prompt : "NO_ERROR")
  } else if (scenario === "after-state") {
    const outcomes = [
      await capture(hooks, "session-a", "first"), await capture(hooks, "session-b", "other-session"),
      await capture(hooks, "session-a", "corrected"), await capture(hooks, "session-a", "after-terminal"),
    ]
    console.log(outcomes.join("\n---\n"))
  } else if (scenario === "after-success") {
    console.log([
      await capture(hooks, "session-a", "first"), await capture(hooks, "session-a", "capture-success"),
      await capture(hooks, "session-a", "after-success"),
    ].join("\n---\n"))
  } else if (scenario === "after-lifecycle") {
    const outcomes = [await capture(hooks, "session-delete", "before-delete")]
    await hooks.event?.({ event: { type: "session.deleted", properties: { info: { id: "session-delete" } } } })
    outcomes.push(await capture(hooks, "session-delete", "after-delete"), await capture(hooks, "session-dispose", "before-dispose"))
    await hooks.dispose?.()
    outcomes.push(await capture(hooks, "session-dispose", "after-dispose"))
    console.log(outcomes.join("\n---\n"))
  } else if (scenario === "after-no-lifecycle") {
    let perSessionAllowed = 0
    let lastSession = ""
    for (let index = 0; index <= 8; index++) {
      const variedPrompt = ` + "`" + `GENTLE_AI_REVIEW_BINDING ${JSON.stringify({ ...opaque, order: index })}\nreview\n` + "`" + `
      lastSession = await capture(hooks, "session-full", "no-lifecycle", variedPrompt)
      if (lastSession.includes("exactly once")) perSessionAllowed++
    }
    const globalHooks = await plugin({ directory: cwd, worktree: cwd })
    let globalAllowed = 0
    let lastGlobal = ""
    for (let index = 0; index <= 64; index++) {
      lastGlobal = await capture(globalHooks, ` + "`" + `session-${index}` + "`" + `, "no-lifecycle")
      if (lastGlobal.includes("exactly once")) globalAllowed++
    }
    console.log(` + "`" + `${perSessionAllowed},${globalAllowed}\n---\n${lastSession}\n---\n${lastGlobal}` + "`" + `)
  } else {
    console.log(await capture(hooks, "session-a", scenario === "after-incomplete" ? "incomplete" : "` + reviewPluginPayloadMarker + `"))
  }
} catch (cause: unknown) {
  console.log(cause instanceof Error ? cause.message : String(cause))
}
`

// reviewPluginPayloadMarker is a token that appears only inside the simulated
// reviewer payload, so a message that contains it can only have embedded that
// payload.
const reviewPluginPayloadMarker = "MARKER-PAYLOAD-9f3a"

// reviewPluginNativeTrustFailure is the failure surface the native CLI now
// emits when Git refuses the bound repository for ownership reasons. It is
// exactly `reviewGitTrustRefusalCode: ...; reviewGitTrustRefusalAction` from
// internal/cli/review_incident.go.
const reviewPluginNativeTrustFailure = "git_repository_untrusted: provider-issued review repository context operation failed; " +
	"Git declined to open the bound repository in this process because it is owned by a different account; " +
	"gentle-ai never provisions a safe.directory exception and never bypasses that protection. " +
	"Restart the host process under a Git context that already trusts that repository, then retry the same exact binding"

// runReviewPluginScenario executes one plugin hook against a stub `gentle-ai`
// that always fails with nativeStderr, and returns the thrown error message.
func runReviewPluginScenario(t *testing.T, scenario, nativeStderr string) string {
	return runReviewPluginScenarioWithNative(t, scenario, "", nativeStderr)
}

func runReviewPluginScenarioWithNative(t *testing.T, scenario, nativeStdout, nativeStderr string) string {
	return runReviewPluginScenarioWithNativeAndPreservation(t, scenario, nativeStdout, nativeStderr, "")
}

func runReviewPluginScenarioWithNativeAndPreservation(t *testing.T, scenario, nativeStdout, nativeStderr, preserveStdout string) string {
	return runReviewPluginScenarioStub(t, scenario, reviewPluginStub{
		stdout: nativeStdout, stderr: nativeStderr, preserveStdout: preserveStdout,
	})
}

// reviewPluginStub configures the fake `gentle-ai` binary the harness runs
// against. preflight/nameStatus/numstat/patches feed the provider-injected
// evidence-materialization path (`review inspect-candidate`); stdout/stderr
// remain the generic fallback used by the capture/preserve-result scenarios
// that predate evidence materialization.
type reviewPluginStub struct {
	stdout         string
	stderr         string
	preserveStdout string
	preflight      string
	nameStatus     string
	numstat        string
	patches        []string
	inspectStderr  string
	patchHugeBytes int
}

func runReviewPluginScenarioStub(t *testing.T, scenario string, stub reviewPluginStub) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub native binary requires a POSIX shell")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	source, err := Read("opencode/plugins/review-result-artifacts.ts")
	if err != nil {
		t.Fatalf("Read(review-result-artifacts.ts) error = %v", err)
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	workDir := filepath.Join(root, "work")
	for _, dir := range []string{binDir, workDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The stub parses --operation/--path-index itself so `review
	// inspect-candidate` calls (name-status, numstat, one patch per manifest
	// index) return distinct, index-addressable evidence, exactly like the
	// real native command does for the frozen trees. A huge patch is
	// generated on the fly from /dev/zero so budget-overflow tests never push
	// megabytes of payload through argv/env (ARG_MAX).
	stubScript := "#!/bin/sh\npayload=$(cat)\n" +
		"op=\"\"; path_index=\"\"; prev=\"\"\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$prev\" in\n" +
		"    --operation) op=\"$arg\" ;;\n" +
		"    --path-index) path_index=\"$arg\" ;;\n" +
		"  esac\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"if [ \"$2\" = \"inspect-candidate\" ]; then\n" +
		"  case \"$op\" in\n" +
		"    name-status) printf '%s\\n' \"$GENTLE_AI_STUB_INSPECT_NAME_STATUS\"; exit 0 ;;\n" +
		"    numstat) printf '%s\\n' \"$GENTLE_AI_STUB_INSPECT_NUMSTAT\"; exit 0 ;;\n" +
		"    patch)\n" +
		"      eval \"value=\\$GENTLE_AI_STUB_INSPECT_PATCH_$path_index\"\n" +
		"      if [ -n \"$value\" ]; then printf '%s\\n' \"$value\"; exit 0; fi\n" +
		"      if [ -n \"$GENTLE_AI_STUB_INSPECT_PATCH_HUGE_BYTES\" ]; then\n" +
		"        head -c \"$GENTLE_AI_STUB_INSPECT_PATCH_HUGE_BYTES\" /dev/zero | tr '\\0' 'A'\n" +
		"        exit 0\n" +
		"      fi\n" +
		"      ;;\n" +
		"  esac\n" +
		"  if [ -n \"$GENTLE_AI_STUB_INSPECT_STDERR\" ]; then printf '%s\\n' \"$GENTLE_AI_STUB_INSPECT_STDERR\" >&2; exit 1; fi\n" +
		"fi\n" +
		"if [ \"$2\" = \"capture-result\" ]; then\n" +
		"  case \"$payload\" in *capture-success*) printf '%s\\n' 'CAPTURED'; exit 0;; esac\n" +
		"  if [ -n \"$GENTLE_AI_STUB_PREFLIGHT\" ]; then\n" +
		"    case \"$*\" in *--preflight*) printf '%s\\n' \"$GENTLE_AI_STUB_PREFLIGHT\"; exit 0 ;; esac\n" +
		"  fi\n" +
		"fi\n" +
		"if [ \"$2\" = \"preserve-result\" ] && [ -n \"$GENTLE_AI_STUB_PRESERVE_STDOUT\" ]; then printf '%s\\n' \"$GENTLE_AI_STUB_PRESERVE_STDOUT\"; exit 0; fi\n" +
		"if [ -n \"$GENTLE_AI_STUB_STDOUT\" ]; then printf '%s\\n' \"$GENTLE_AI_STUB_STDOUT\"; exit 0; fi\n" +
		"printf '%s\\n' \"$GENTLE_AI_STUB_STDERR\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "gentle-ai"), []byte(stubScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugin.mts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "harness.mts"), []byte(reviewPluginHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "harness.mts", scenario, workDir)
	command.Dir = root
	env := append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GENTLE_AI_STUB_STDOUT="+stub.stdout,
		"GENTLE_AI_STUB_STDERR="+stub.stderr,
		"GENTLE_AI_STUB_PRESERVE_STDOUT="+stub.preserveStdout,
		"GENTLE_AI_STUB_PREFLIGHT="+stub.preflight,
		"GENTLE_AI_STUB_INSPECT_NAME_STATUS="+stub.nameStatus,
		"GENTLE_AI_STUB_INSPECT_NUMSTAT="+stub.numstat,
		"GENTLE_AI_STUB_INSPECT_STDERR="+stub.inspectStderr,
		"GENTLE_AI_REVIEW_CWD=",
	)
	if stub.patchHugeBytes > 0 {
		env = append(env, fmt.Sprintf("GENTLE_AI_STUB_INSPECT_PATCH_HUGE_BYTES=%d", stub.patchHugeBytes))
	}
	for index, patch := range stub.patches {
		env = append(env, fmt.Sprintf("GENTLE_AI_STUB_INSPECT_PATCH_%d=%s", index, patch))
	}
	command.Env = env
	output, err := command.CombinedOutput()
	if err != nil {
		t.Skipf("node could not run the TypeScript plugin harness (%v): %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestReviewPluginRejectsInvalidBindingBeforeReviewerLaunch(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
	}{
		{name: "missing", wantErr: "review task is missing GENTLE_AI_REVIEW_BINDING"},
		{name: "equals", wantErr: "review task is missing GENTLE_AI_REVIEW_BINDING"},
		{name: "malformed", wantErr: "review task binding is malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := runReviewPluginScenarioWithNative(t, "before-"+tt.name, `{"unexpected":"native call"}`, "")
			if message != tt.wantErr {
				t.Fatalf("invalid binding result = %q, want %q", message, tt.wantErr)
			}
		})
	}
}

// reviewPluginPreflight builds a valid single-path preflight response for
// lineage "trust-check" / target sha256:d...
func reviewPluginPreflight(baseTree, candidateTree string) string {
	return reviewPluginPreflightWithPaths(baseTree, candidateTree, []string{"internal/example.go"})
}

// reviewPluginPreflightWithPaths builds a preflight response whose manifest
// holds one entry per path, in the given (already-canonical) order.
func reviewPluginPreflightWithPaths(baseTree, candidateTree string, paths []string) string {
	entries := make([]string, len(paths))
	for index, path := range paths {
		entries[index] = `{"path":"` + path + `","status":"M","old_mode":"100644","new_mode":"100644","deleted":false,"type_changed":false,"mode_only":false,"intended_untracked":false}`
	}
	return `{"schema":"gentle-ai.review-capture-preflight/v1","capability":"review.native_capture_preflight",` +
		`"lineage_id":"trust-check","target_identity":"sha256:` + strings.Repeat("d", 64) + `","lens":"review-risk","selected_order":0,` +
		`"artifact_subject":{"schema":"gentle-ai.review-artifact-subject/v2","subject_hash":"sha256:` + strings.Repeat("c", 64) + `",` +
		`"lineage_id":"trust-check","authority_revision":"sha256:` + strings.Repeat("b", 64) + `","target_identity":"sha256:` + strings.Repeat("d", 64) + `",` +
		`"base_tree":"` + baseTree + `","candidate_tree":"` + candidateTree + `","changed_path_manifest_sha256":"sha256:` + strings.Repeat("e", 64) + `",` +
		`"lens":"review-risk","selected_order":0},"base_tree":"` + baseTree + `","candidate_tree":"` + candidateTree + `",` +
		`"changed_path_manifest":[` + strings.Join(entries, ",") + `]}`
}

// TestReviewPluginBindsProviderOwnedCandidateContext pins the restored
// shell-less transport: the plugin itself materializes name-status, numstat,
// and one verbatim patch per manifest path through the native channel, and
// injects all of it into the reviewer's prompt before the reviewer ever
// launches. No shell and no read tool exist on the reviewer side, so this
// injected block is provably its only byte source.
func TestReviewPluginBindsProviderOwnedCandidateContext(t *testing.T) {
	baseTree := strings.Repeat("1", 40)
	candidateTree := strings.Repeat("2", 40)
	preflight := reviewPluginPreflight(baseTree, candidateTree)
	prompt := runReviewPluginScenarioStub(t, "before-valid", reviewPluginStub{
		preflight: preflight, nameStatus: "M\tinternal/example.go", numstat: "3\t1\tinternal/example.go",
		patches: []string{"--- a/internal/example.go\n+++ b/internal/example.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"},
	})
	if !strings.HasPrefix(prompt, "GENTLE_AI_REVIEW_BINDING {") {
		t.Fatalf("injected prompt does not begin with the exact binding prefix: %q", prompt)
	}
	if !strings.Contains(prompt, `"subject_hash":"sha256:`+strings.Repeat("c", 64)+`"`) {
		t.Fatalf("bound prompt is missing the preflight subject hash: %q", prompt)
	}
	for _, want := range []string{
		"GENTLE_AI_REVIEW_CONTEXT ", baseTree, candidateTree, "internal/example.go",
		"GENTLE_AI_REVIEW_NAME_STATUS\nM\tinternal/example.go\nGENTLE_AI_REVIEW_NAME_STATUS_END",
		"GENTLE_AI_REVIEW_NUMSTAT\n3\t1\tinternal/example.go\nGENTLE_AI_REVIEW_NUMSTAT_END",
		"GENTLE_AI_REVIEW_PATCH 0 internal/example.go\n--- a/internal/example.go",
		"+new\nGENTLE_AI_REVIEW_PATCH_END",
		"GENTLE_AI_REVIEW_CONTEXT_END",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("plugin omitted provider context %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "candidate_diff") {
		t.Fatalf("injected prompt contains obsolete candidate diff payload: %q", prompt)
	}
}

// TestReviewPluginOrdersEvidenceByManifestIndex proves multi-path evidence is
// materialized once per canonical manifest index, in exact order, each
// carrying its own index and literal path.
func TestReviewPluginOrdersEvidenceByManifestIndex(t *testing.T) {
	baseTree := strings.Repeat("1", 40)
	candidateTree := strings.Repeat("2", 40)
	paths := []string{"internal/a.go", "internal/b.go"}
	preflight := reviewPluginPreflightWithPaths(baseTree, candidateTree, paths)
	prompt := runReviewPluginScenarioStub(t, "before-valid", reviewPluginStub{
		preflight: preflight, nameStatus: "M\tinternal/a.go\nM\tinternal/b.go", numstat: "1\t1\tinternal/a.go\n1\t1\tinternal/b.go",
		patches: []string{"PATCH-CONTENT-A", "PATCH-CONTENT-B"},
	})
	first := strings.Index(prompt, "GENTLE_AI_REVIEW_PATCH 0 internal/a.go")
	second := strings.Index(prompt, "GENTLE_AI_REVIEW_PATCH 1 internal/b.go")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("path evidence is not in exact manifest order: %q", prompt)
	}
	if !strings.Contains(prompt, "PATCH-CONTENT-A") || !strings.Contains(prompt, "PATCH-CONTENT-B") {
		t.Fatalf("plugin omitted per-path patch content: %q", prompt)
	}
}

// TestReviewPluginReplacesCallerAuthoredCandidateContext proves the
// provider-injected prompt wholesale replaces caller-authored text: nothing
// the caller wrote (a stale tree, a fabricated manifest, or free prose)
// survives into the launched reviewer's prompt.
func TestReviewPluginReplacesCallerAuthoredCandidateContext(t *testing.T) {
	baseTree := strings.Repeat("1", 40)
	candidateTree := strings.Repeat("2", 40)
	prompt := runReviewPluginScenarioStub(t, "before-substitute", reviewPluginStub{
		preflight:  reviewPluginPreflight(baseTree, candidateTree),
		nameStatus: "M\tinternal/example.go", numstat: "1\t1\tinternal/example.go",
		patches: []string{"REAL-PATCH-CONTENT"},
	})
	for _, callerValue := range []string{strings.Repeat("9", 40), strings.Repeat("8", 40), "caller.txt", "review the frozen candidate"} {
		if strings.Contains(prompt, callerValue) {
			t.Fatalf("provider injection retained caller-authored context %q: %q", callerValue, prompt)
		}
	}
	for _, providerValue := range []string{baseTree, candidateTree, "internal/example.go", "REAL-PATCH-CONTENT"} {
		if !strings.Contains(prompt, providerValue) {
			t.Fatalf("provider injection omitted preflight context %q: %q", providerValue, prompt)
		}
	}
}

// TestReviewPluginFailsClosedWhenEvidenceMaterializationFails is mutation
// proof (c): if evidence materialization fails partway (here, the numstat
// call), the reviewer must never launch with the caller-authored prompt as a
// fallback. A mutant that swallowed the failure and fell through to
// `output.args.prompt` unchanged would leave the caller's own text in the
// result; this test fails red against that mutant.
func TestReviewPluginFailsClosedWhenEvidenceMaterializationFails(t *testing.T) {
	baseTree := strings.Repeat("1", 40)
	candidateTree := strings.Repeat("2", 40)
	message := runReviewPluginScenarioStub(t, "before-valid", reviewPluginStub{
		preflight:  reviewPluginPreflight(baseTree, candidateTree),
		nameStatus: "M\tinternal/example.go",
		// numstat left unset so the stub falls through to inspectStderr.
		inspectStderr: "NATIVE-INSPECT-FAILED",
	})
	if !strings.Contains(message, "review context evidence materialization failed") {
		t.Fatalf("evidence materialization failure did not stop the reviewer launch closed: %s", message)
	}
	if !strings.Contains(message, "The reviewer was not launched") {
		t.Fatalf("evidence materialization failure lost its exactly-once guarantee: %s", message)
	}
	if strings.Contains(message, "review the frozen candidate") {
		t.Fatalf("evidence materialization failure fell back to the caller-authored prompt: %s", message)
	}
}

// TestReviewPluginFailsClosedOnEvidenceBudgetOverflow is mutation proof (d):
// evidence that would exceed REVIEW_CONTEXT_BYTE_BUDGET must refuse the
// reviewer launch outright, never truncate. A mutant that silently truncated
// the oversized patch instead of refusing would produce a "completed"-shaped
// result from partial evidence; this test fails red against that mutant,
// because it demands the refusal message and demands the huge payload never
// reached the result at all.
func TestReviewPluginFailsClosedOnEvidenceBudgetOverflow(t *testing.T) {
	baseTree := strings.Repeat("1", 40)
	candidateTree := strings.Repeat("2", 40)
	message := runReviewPluginScenarioStub(t, "before-valid", reviewPluginStub{
		preflight:      reviewPluginPreflight(baseTree, candidateTree),
		nameStatus:     "M\tinternal/example.go",
		numstat:        "1\t1\tinternal/example.go",
		patchHugeBytes: 2_000_000, // exceeds the 1,048,576-byte budget
	})
	if !strings.Contains(message, "provider injection budget") {
		t.Fatalf("budget overflow did not refuse the reviewer launch: %s", message)
	}
	if !strings.Contains(message, "never truncated") {
		t.Fatalf("budget overflow message does not rule out truncation: %s", message)
	}
	if !strings.Contains(message, "The reviewer was not launched") {
		t.Fatalf("budget overflow lost its exactly-once guarantee: %s", message)
	}
	if len(message) > 4096 || strings.Count(message, "A") > 512 {
		t.Fatalf("budget overflow leaked the oversized payload into the transcript: %d bytes", len(message))
	}
}

// TestReviewPluginRequiresRepositoryContextForEvidenceMaterialization proves
// the evidence-materialization guard: a binding without repository_context
// and revision (the legacy shape) can never reach `inspect-candidate` at
// all, even if its preflight response were somehow well-formed, because
// inspect-candidate has no --cwd fallback.
func TestReviewPluginRequiresRepositoryContextForEvidenceMaterialization(t *testing.T) {
	baseTree := strings.Repeat("1", 40)
	candidateTree := strings.Repeat("2", 40)
	message := runReviewPluginScenarioStub(t, "before-legacy", reviewPluginStub{
		preflight:     reviewPluginPreflight(baseTree, candidateTree),
		inspectStderr: "NATIVE-INSPECT-MUST-NOT-RUN",
	})
	if !strings.Contains(message, "requires a repository-context binding") {
		t.Fatalf("legacy binding did not refuse evidence materialization closed: %s", message)
	}
	if strings.Contains(message, "NATIVE-INSPECT-MUST-NOT-RUN") {
		t.Fatalf("legacy binding attempted native candidate inspection: %s", message)
	}
}

func TestReviewPluginRejectsNonCanonicalProviderManifest(t *testing.T) {
	entry := `{"path":"internal/example.go","status":"M","old_mode":"100644","new_mode":"100644","deleted":false,"type_changed":false,"mode_only":false,"intended_untracked":false}`
	unsorted := `{"path":"z.go","status":"M","old_mode":"100644","new_mode":"100644","deleted":false,"type_changed":false,"mode_only":false,"intended_untracked":false},` + entry
	preflight := strings.Replace(reviewPluginPreflight(strings.Repeat("1", 40), strings.Repeat("2", 40)), entry, unsorted, 1)
	message := runReviewPluginScenarioStub(t, "before-valid", reviewPluginStub{preflight: preflight})
	if !strings.Contains(message, "review capture preflight failed") || !strings.Contains(message, "The reviewer was not launched") {
		t.Fatalf("non-canonical provider manifest was accepted: %s", message)
	}
	if strings.Contains(message, "incomplete artifact subject") {
		t.Fatalf("opaque preflight exposed native validation detail: %s", message)
	}
}

func TestReviewPluginRejectsLegacyBinaryWithoutPreflightBeforeReviewerLaunch(t *testing.T) {
	message := runReviewPluginScenario(t, "before-legacy", "flag provided but not defined: -preflight")
	if !strings.Contains(message, "review capture preflight failed") || !strings.Contains(message, "The reviewer was not launched") {
		t.Fatalf("unsupported preflight did not fail closed before reviewer launch: %s", message)
	}
}

// TestReviewPluginOpaqueDoubleFailurePreservesPayload pins the symmetry the
// external report identified: when capture AND durable preservation both fail,
// an opaque repository_context binding must retain the same bounded copy of the
// reviewer payload the legacy --cwd binding already retains. Both bindings
// resolve the same repository, so one environmental refusal can fail both, and
// on the opaque path the transcript was the only remaining copy.
func TestReviewPluginOpaqueDoubleFailurePreservesPayload(t *testing.T) {
	for _, scenario := range []string{"after-opaque", "after-legacy"} {
		t.Run(scenario, func(t *testing.T) {
			message := runReviewPluginScenario(t, scenario, "resolve failed")
			if message == "NO_ERROR" {
				t.Fatal("plugin did not fail despite an always-failing native binary")
			}
			if !strings.Contains(message, "raw reviewer result follows for manual recovery") {
				t.Fatalf("double failure dropped its last-resort payload fallback: %s", message)
			}
			if !strings.Contains(message, reviewPluginPayloadMarker) {
				t.Fatalf("double failure did not preserve the reviewer payload: %s", message)
			}
		})
	}
}

// TestReviewPluginPostLaunchTrustRefusalStaysActionable pins that the typed
// trust refusal keeps its carry-outable instruction on the post-launch capture
// path too, where the reviewer has already been spent and the payload is the
// only thing left to protect.
func TestReviewPluginPostLaunchTrustRefusalStaysActionable(t *testing.T) {
	message := runReviewPluginScenario(t, "after-opaque", reviewPluginNativeTrustFailure)
	if !strings.Contains(message, "git_repository_untrusted") {
		t.Fatalf("post-launch failure suppressed the native Git trust refusal: %s", message)
	}
	if !strings.Contains(message, "Restart the host process") {
		t.Fatalf("post-launch trust refusal carries no instruction the caller can carry out: %s", message)
	}
	if !strings.Contains(message, reviewPluginPayloadMarker) {
		t.Fatalf("post-launch trust refusal lost the reviewer payload: %s", message)
	}
}

// TestReviewPluginSurfacesNativeGitTrustRefusal pins the other half of
// finding 1: the plugin must stop collapsing a native Git trust refusal into
// "refresh the exact native next_transition", which cannot change the Git
// trust context of an already-running host process.
func TestReviewPluginSurfacesNativeGitTrustRefusal(t *testing.T) {
	message := runReviewPluginScenario(t, "before-opaque", reviewPluginNativeTrustFailure)
	if message == "NO_ERROR" {
		t.Fatal("preflight did not fail despite an always-failing native binary")
	}
	if !strings.Contains(message, "git_repository_untrusted") {
		t.Fatalf("plugin suppressed the native Git trust refusal: %s", message)
	}
	if strings.Contains(message, "next_transition") {
		t.Fatalf("plugin still advises refreshing the transition for a Git trust refusal: %s", message)
	}
	if !strings.Contains(message, "Restart the host process") {
		t.Fatalf("plugin carries no instruction the caller can carry out: %s", message)
	}
	if !strings.Contains(message, "The reviewer was not launched") {
		t.Fatalf("plugin lost its pre-launch exactly-once guarantee: %s", message)
	}
}

// TestReviewPluginSurfacesAdmissionRejectionClass pins the diagnosability fix
// from the first live 4R run: a reviewer result the native admission refused
// (for example a severe finding anchored to an unchanged line) collapsed into
// "retry the same opaque binding" — advice that deterministically fails,
// because recapturing identical bytes can never satisfy admission. The opaque
// message must carry the typed decision class and direct the caller to
// relaunch the reviewer, while the native diagnostic prose (which can embed
// payload text) stays out of the transcript.
func TestReviewPluginTerminatesUnstructuredAdmissionRejection(t *testing.T) {
	native := "Error: reviewer artifact admission out_of_scope: candidate-causal findings are not proven by repository-derived changed-line evidence"
	message := runReviewPluginScenario(t, "after-opaque", native)
	if message == "NO_ERROR" {
		t.Fatal("plugin did not fail despite an always-failing native binary")
	}
	if !strings.Contains(message, "rejected the reviewer result as out_of_scope") {
		t.Fatalf("admission rejection lost its typed decision class: %s", message)
	}
	if !strings.Contains(message, "reviewer_admission_recovery_unavailable") || !strings.Contains(message, "stop relaunching") {
		t.Fatalf("unstructured admission rejection was not terminal: %s", message)
	}
	if strings.Contains(message, "relaunch this lens reviewer") || strings.Contains(message, "retry the same opaque binding") {
		t.Fatalf("unstructured admission rejection authorized a blind retry: %s", message)
	}
	if strings.Contains(message, "severe findings must anchor") {
		t.Fatalf("admission rejection inferred a severe-finding cause without structured evidence: %s", message)
	}
	if strings.Contains(message, "changed-line evidence") {
		t.Fatalf("plugin forwarded native admission diagnostic prose through an opaque binding: %s", message)
	}
	if !strings.Contains(message, reviewPluginPayloadMarker) {
		t.Fatalf("admission rejection did not preserve the reviewer payload: %s", message)
	}
}

func TestReviewPluginTerminatesIncompleteAdmissionWithoutDiagnostic(t *testing.T) {
	nativeDiagnosticMarker := "NATIVE-DIAGNOSTIC-4e7c"
	native := "Error: reviewer artifact admission incomplete: " + nativeDiagnosticMarker +
		` rejected payload {"evidence":["` + reviewPluginPayloadMarker + `"]}`
	message := runReviewPluginScenarioWithNativeAndPreservation(
		t,
		"after-incomplete",
		"",
		native,
		`{"reference":"incident/reviewer-result.json"}`,
	)
	if message == "NO_ERROR" {
		t.Fatal("plugin did not fail despite native incomplete admission")
	}
	if !strings.Contains(message, "rejected the reviewer result as incomplete") {
		t.Fatalf("incomplete admission lost its typed decision: %s", message)
	}
	if !strings.Contains(message, "reviewer_admission_recovery_unavailable") || !strings.Contains(message, "without a safe actionable diagnostic") {
		t.Fatalf("incomplete admission was not terminal and actionable: %s", message)
	}
	if strings.Contains(message, "relaunch this lens reviewer") {
		t.Fatalf("incomplete admission authorized a blind relaunch: %s", message)
	}
	if strings.Contains(message, "severe findings must anchor") {
		t.Fatalf("incomplete admission inferred a severe-finding cause: %s", message)
	}
	if strings.Contains(message, nativeDiagnosticMarker) {
		t.Fatalf("incomplete admission leaked native diagnostic prose: %s", message)
	}
	if strings.Contains(message, reviewPluginPayloadMarker) {
		t.Fatalf("incomplete admission leaked the rejected reviewer payload: %s", message)
	}
}

func TestReviewPluginSurfacesStructuredLocationRecoveryDiagnostic(t *testing.T) {
	native := `Error: reviewer artifact admission out_of_scope: reviewer finding location is invalid; ` +
		`admission_diagnostic={"code":"invalid_finding_location","finding_id":"R3-001",` +
		`"location":"internal/a.go:207-221","reason":"line_suffix_not_integer"}`
	message := runReviewPluginScenarioWithNativeAndPreservation(
		t, "after-opaque", "", native, `{"reference":"rinc1_safe"}`,
	)
	for _, want := range []string{
		"rejected the reviewer result as out_of_scope",
		`finding R3-001 at "internal/a.go:207-221": line_suffix_not_integer`,
		"relaunch this lens reviewer",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("structured recovery message %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "reviewer finding location is invalid") {
		t.Fatalf("structured recovery leaked native diagnostic prose: %s", message)
	}
	for _, location := range []string{
		`C:\Users\private\repo\a.go:207-221`,
		"internal:../private/a.go:207-221",
		"internal/a.go:7:/home/private/repo.go",
		"internal/a.go:7:https://private.example/repo",
	} {
		unsafe := strings.Replace(native, "internal/a.go:207-221", location, 1)
		message = runReviewPluginScenarioWithNativeAndPreservation(t, "after-opaque", "", unsafe, `{"reference":"rinc1_safe"}`)
		if strings.Contains(message, location) || strings.Contains(message, "finding R3-001") {
			t.Fatalf("unsafe structured diagnostic escaped opaque filtering: %s", message)
		}
	}
	for _, incompatible := range []string{
		strings.Replace(native, `"code":"invalid_finding_location"`, `"code":"candidate_causality_unproven"`, 1),
		strings.Replace(native, `"reason":"line_suffix_not_integer"`, `"reason":"line_not_changed_by_candidate"`, 1),
	} {
		message = runReviewPluginScenarioWithNativeAndPreservation(t, "after-opaque", "", incompatible, `{"reference":"rinc1_safe"}`)
		if strings.Contains(message, "finding R3-001") || strings.Contains(message, "internal/a.go:207-221") {
			t.Fatalf("incompatible structured diagnostic escaped filtering: %s", message)
		}
	}
}

func TestReviewPluginScopesAndReclaimsAdmissionRecovery(t *testing.T) {
	native := `Error: reviewer artifact admission out_of_scope: native detail must stay opaque; admission_diagnostic={"code":"candidate_causality_unproven","finding_id":"R3-001","location":"internal/a.go:7","reason":"line_not_changed_by_candidate"}`
	message := runReviewPluginScenarioWithNativeAndPreservation(t, "after-state", "", native, `{"reference":"rinc1_safe"}`)
	parts := strings.Split(message, "\n---\n")
	if len(parts) != 4 {
		t.Fatalf("bounded recovery outcomes = %q", message)
	}
	for _, index := range []int{0, 1, 3} {
		if !strings.Contains(parts[index], "exactly once") || strings.Contains(parts[index], "recovery_exhausted") {
			t.Fatalf("outcome %d did not hold an independent first attempt: %s", index, parts[index])
		}
	}
	if !strings.Contains(parts[2], "reviewer_admission_recovery_exhausted") || !strings.Contains(parts[2], "stop relaunching") {
		t.Fatalf("same session and STATUS binding did not become terminal: %s", parts[2])
	}
	if !strings.Contains(parts[0], "line_not_changed_by_candidate") {
		t.Fatalf("first refusal lost its actionable diagnostic: %s", parts[0])
	}
}

func TestReviewPluginReclaimsSuccessfulAdmissionRecovery(t *testing.T) {
	native := `reviewer artifact admission out_of_scope: opaque; admission_diagnostic={"code":"candidate_causality_unproven","finding_id":"R3-001","location":"internal/a.go:7","reason":"line_not_changed_by_candidate"}`
	parts := strings.Split(runReviewPluginScenarioWithNativeAndPreservation(t, "after-success", "", native, `{"reference":"rinc1_safe"}`), "\n---\n")
	if len(parts) != 3 || !strings.Contains(parts[0], "exactly once") || parts[1] != "CAPTURED" || !strings.Contains(parts[2], "exactly once") {
		t.Fatalf("successful capture did not reclaim its recovery state: %q", parts)
	}
}

func TestReviewPluginClearsRecoveryOnLifecycleHooks(t *testing.T) {
	native := `reviewer artifact admission out_of_scope: opaque; admission_diagnostic={"code":"invalid_finding_location","finding_id":"R3-001","location":"internal/a.go:7-9","reason":"line_suffix_not_integer"}`
	parts := strings.Split(runReviewPluginScenarioWithNativeAndPreservation(t, "after-lifecycle", "", native, `{"reference":"rinc1_safe"}`), "\n---\n")
	if len(parts) != 4 {
		t.Fatalf("lifecycle outcomes = %q", parts)
	}
	for index, part := range parts {
		if !strings.Contains(part, "exactly once") || strings.Contains(part, "recovery_exhausted") {
			t.Fatalf("lifecycle outcome %d retained stale recovery state: %s", index, part)
		}
	}
}

func TestReviewPluginBoundsRecoveryWithoutLifecycleEvents(t *testing.T) {
	native := `reviewer artifact admission out_of_scope: opaque; admission_diagnostic={"code":"invalid_finding_location","finding_id":"R3-001","location":"internal/a.go:7-9","reason":"line_suffix_not_integer"}`
	parts := strings.Split(runReviewPluginScenarioWithNativeAndPreservation(t, "after-no-lifecycle", "", native, `{"reference":"rinc1_safe"}`), "\n---\n")
	if len(parts) != 3 || parts[0] != "8,64" ||
		!strings.Contains(parts[1], "reviewer_admission_recovery_unavailable") ||
		!strings.Contains(parts[2], "reviewer_admission_recovery_unavailable") {
		t.Fatalf("no-lifecycle fallback was not bounded: %q", parts)
	}
}

// TestReviewPluginKeepsGenericOpaqueFailureOpaque proves the trust
// pass-through is not a hole in the opaque path's path-safety rule: any
// other native preflight failure still collapses into the generic
// provider-owned message.
func TestReviewPluginKeepsGenericOpaqueFailureOpaque(t *testing.T) {
	leak := "repository_context_unavailable: provider-issued review repository context operation failed; " +
		"failed under /home/someone/private/repo"
	message := runReviewPluginScenario(t, "before-opaque", leak)
	if strings.Contains(message, "/home/someone/private/repo") {
		t.Fatalf("plugin forwarded a native path through an opaque binding: %s", message)
	}
	if !strings.Contains(message, "repository_context_preflight_failed") {
		t.Fatalf("generic opaque failure lost its provider-owned code: %s", message)
	}
}
