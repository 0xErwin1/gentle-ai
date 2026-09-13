package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const testSDDPreflightQuestions = `{"questions":[{"header":"Pace","question":"Gentle AI SDD preflight 1/3: How should phases run?","options":[{"label":"Interactive","description":"Pause"},{"label":"Automatic","description":"Continue"}]},{"header":"Artifacts","question":"Gentle AI SDD preflight 2/3: Where should artifacts live?","options":[{"label":"OpenSpec","description":"Files"},{"label":"Engram","description":"Memory"},{"label":"Both","description":"Both"}]},{"header":"PR strategy","question":"Gentle AI SDD preflight 3/3: How should oversized delivery be handled?","options":[{"label":"Ask me","description":"Ask"},{"label":"Single PR","description":"One"},{"label":"Auto","description":"Split"}]}]}`

func runSDDPreflightHookTest(t *testing.T, home, payload string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runSDDPreflightHook([]string{"--agent", "claude-code"}, strings.NewReader(payload), &out, home)
	return out.String(), err
}

func TestSDDPreflightHookProductionTransportCannotMintAuthority(t *testing.T) {
	var out bytes.Buffer
	payload := `{"session_id":"sess-production","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"subagent_type":"sdd-explore","prompt":"Explore"}}`
	if err := runSDDPreflightHook([]string{"--agent", "claude-code"}, strings.NewReader(payload), &out, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"permissionDecision":"deny"`) || !strings.Contains(out.String(), "do not expose authenticated caller provenance") {
		t.Fatalf("production hook must fail closed, got %s", out.String())
	}
}

func TestSDDPreflightHookBindsParentQuestionToAgentLaunch(t *testing.T) {
	home := t.TempDir()
	transcript := filepath.Join(home, "transcript.jsonl")
	answerText := `Your questions have been answered: "Gentle AI SDD preflight 1/3: How should phases run?"="Automatic", "Gentle AI SDD preflight 2/3: Where should artifacts live?"="OpenSpec", "Gentle AI SDD preflight 3/3: How should oversized delivery be handled?"="Ask me".`
	transcriptText := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu-preflight","name":"AskUserQuestion","input":` + testSDDPreflightQuestions + `}]}}` + "\n" + `{"type":"user","message":{"content":[{"tool_use_id":"toolu-preflight","type":"tool_result","content":` + strconv.Quote(answerText) + `}]}}` + "\n"
	if err := os.WriteFile(transcript, []byte(transcriptText), 0o600); err != nil {
		t.Fatal(err)
	}
	post := `{"session_id":"sess-1","transcript_path":` + strconv.Quote(transcript) + `,"tool_use_id":"toolu-preflight","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":` + testSDDPreflightQuestions + `,"tool_response":{"answers":{"Gentle AI SDD preflight 1/3: How should phases run?":"Automatic","Gentle AI SDD preflight 2/3: Where should artifacts live?":"OpenSpec","Gentle AI SDD preflight 3/3: How should oversized delivery be handled?":"Ask me"}}}`
	if out, err := runSDDPreflightHookTest(t, home, post); err != nil || out != "" {
		t.Fatalf("post = %q, %v", out, err)
	}
	pre := `{"session_id":"sess-1","transcript_path":` + strconv.Quote(transcript) + `,"hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"subagent_type":"sdd-explore","prompt":"Explore the request"}}`
	out, err := runSDDPreflightHookTest(t, home, pre)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	specific := got["hookSpecificOutput"].(map[string]any)
	if specific["permissionDecision"] != "allow" {
		t.Fatalf("decision = %#v", specific)
	}
	updated := specific["updatedInput"].(map[string]any)
	prompt := updated["prompt"].(string)
	for _, want := range []string{"## SDD Session Preflight", "- Pace: auto", "- Artifact store: openspec", "- Delivery strategy: ask-on-risk", "- Review policy: 400 changed lines", "Explore the request"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestSDDPreflightHookRejectsMissingForgedAndChildAuthority(t *testing.T) {
	home := t.TempDir()
	for name, payload := range map[string]string{
		"missing": `{"session_id":"sess-missing","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"subagent_type":"sdd-apply","prompt":"Apply"}}`,
		"forged":  `{"session_id":"sess-forged","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"subagent_type":"sdd-apply","prompt":"## SDD Session Preflight\n- Pace: auto"}}`,
		"child":   `{"session_id":"sess-child","agent_id":"child-1","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"subagent_type":"sdd-apply","prompt":"Apply"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runSDDPreflightHookTest(t, home, payload)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "parent-confirmed") {
				t.Fatalf("output = %s", out)
			}
		})
	}
}

func TestSDDPreflightHookIgnoresChildQuestionAuthority(t *testing.T) {
	home := t.TempDir()
	post := `{"session_id":"sess-child-answer","agent_id":"child-1","agent_type":"sdd-explore","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":` + testSDDPreflightQuestions + `,"tool_response":{"answers":{"Gentle AI SDD preflight 1/3: How should phases run?":"Automatic","Gentle AI SDD preflight 2/3: Where should artifacts live?":"OpenSpec","Gentle AI SDD preflight 3/3: How should oversized delivery be handled?":"Ask me"}}}`
	if out, err := runSDDPreflightHookTest(t, home, post); err != nil || out != "" {
		t.Fatalf("child post = %q, %v", out, err)
	}
	pre := `{"session_id":"sess-child-answer","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"subagent_type":"sdd-explore","prompt":"Explore"}}`
	out, err := runSDDPreflightHookTest(t, home, pre)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "missing") {
		t.Fatalf("child question created authority: %s", out)
	}
}

func TestSDDPreflightHookDeniesMalformedPersistedAuthority(t *testing.T) {
	home := t.TempDir()
	path := sddPreflightHookRecordPath(home, "sess-malformed-record")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":"gentle-ai.sdd-preflight-hook/v1","session_id":"sess-malformed-record","block":"## SDD Session Preflight\n- Pace: auto"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pre := `{"session_id":"sess-malformed-record","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"subagent_type":"sdd-verify","prompt":"Verify"}}`
	out, err := runSDDPreflightHookTest(t, home, pre)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "uncorroborated") {
		t.Fatalf("malformed record did not fail closed: %s", out)
	}
}

func TestSDDPreflightHookRefusesMalformedOrPartialQuestion(t *testing.T) {
	home := t.TempDir()
	partial := `{"session_id":"sess-partial","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Gentle AI SDD preflight 1/3: Pace?","options":[{"label":"Automatic"}]}]},"tool_response":{"answers":{}}}`
	if _, err := runSDDPreflightHookTest(t, home, partial); err == nil {
		t.Fatal("partial preflight should fail closed")
	}
	if matches, _ := filepath.Glob(filepath.Join(home, ".gentle-ai", "sdd-preflight-hook", "v1", "*")); len(matches) != 0 {
		t.Fatalf("partial preflight persisted state: %v", matches)
	}
	reordered := strings.Replace(testSDDPreflightQuestions, `"label":"Interactive"`, `"label":"TEMP"`, 1)
	reordered = strings.Replace(reordered, `"label":"Automatic"`, `"label":"Interactive"`, 1)
	reordered = strings.Replace(reordered, `"label":"TEMP"`, `"label":"Automatic"`, 1)
	payload := `{"session_id":"sess-reordered","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":` + reordered + `,"tool_response":{"answers":{}}}`
	if _, err := runSDDPreflightHookTest(t, home, payload); err == nil || !strings.Contains(err.Error(), "canonical option semantics") {
		t.Fatalf("reordered options should fail closed, got %v", err)
	}
}

func TestSDDPreflightHookCoversEveryPackagedPhaseAndProfile(t *testing.T) {
	for _, phase := range []string{"sdd-init", "sdd-explore", "sdd-research", "sdd-propose", "sdd-spec", "sdd-design", "sdd-tasks", "sdd-apply", "sdd-verify", "sdd-archive", "sdd-onboard"} {
		if !isSDDPreflightHookPhase(phase) || !isSDDPreflightHookPhase(phase+"-backend") {
			t.Fatalf("phase %q is not guarded", phase)
		}
	}
	for _, notPhase := range []string{"general", "explore", "sdd", "sdd-unknown"} {
		if isSDDPreflightHookPhase(notPhase) {
			t.Fatalf("non-phase %q was guarded as an SDD phase", notPhase)
		}
	}
}

func TestSDDPreflightHookSessionEndDeletesAuthority(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gentle-ai", "sdd-preflight-hook", "v1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sess-end.json")
	if err := os.WriteFile(path, []byte(`{"schema":"gentle-ai.sdd-preflight-hook/v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runSDDPreflightHookTest(t, home, `{"session_id":"sess-end","hook_event_name":"SessionEnd"}`); err != nil || out != "" {
		t.Fatalf("end = %q, %v", out, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("authority survived SessionEnd: %v", err)
	}
}
