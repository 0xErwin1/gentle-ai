package telemetry

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
)

const claudeSubagentHook = `{"session_id":"PRIVATE_SESSION","transcript_path":"PRIVATE_MAIN_PATH","cwd":"PRIVATE_CWD","permission_mode":"default","hook_event_name":"SubagentStop","stop_hook_active":false,"agent_id":"PRIVATE_AGENT_ID","agent_type":"sdd-apply","agent_transcript_path":"PRIVATE_AGENT_PATH","last_assistant_message":"FINAL_MESSAGE"}`

func TestClaudeRuntimeSubagentUsesLastAssistantUsage(t *testing.T) {
	hook, err := ParseClaudeHook(strings.NewReader(claudeSubagentHook))
	if err != nil {
		t.Fatal(err)
	}
	transcript := []byte("not-json\n" +
		`{"type":"assistant","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}` + "\n" +
		`{"type":"user","message":{"content":"PRIVATE_PROMPT"}}` + "\n" +
		`{"type":"assistant","message":{"model":"claude-opus-5","content":[{"type":"text","text":"FINAL_MESSAGE"}],"usage":{"input_tokens":11,"output_tokens":12,"cache_read_input_tokens":13,"cache_creation_input_tokens":14}}}` + "\n")
	usage, ok := ParseClaudeTranscriptTail(transcript, false, hook.LastAssistantDigest)
	if !ok {
		t.Fatal("usage not found")
	}
	o := NormalizeClaude(hook, usage, []byte("---\nname: sdd-apply\nmodel: claude-haiku-4-5\neffort: high\n---\nPRIVATE_PROMPT"))
	if o == nil {
		t.Fatal("observation missing")
	}
	if o.Row.AgentKind != "built_in" || o.Row.AgentClass != "sdd-apply" || o.Row.Model != (RuntimeModel{Provider: "anthropic", ID: "claude-opus-5"}) || o.Row.ModelEvidence != "response" || o.Row.SelectedEffort != "high" || o.Row.EffectiveEffort != "unavailable" {
		t.Fatalf("row: %+v", o.Row)
	}
	for got, want := range map[string]string{string(o.Row.Input): tokenReported("11"), string(o.Row.Output): tokenReported("12"), string(o.Row.CacheRead): tokenReported("13"), string(o.Row.CacheCreation): tokenReported("14"), string(o.Row.ReasoningTokens): `{"reported":0,"unavailable":0,"unsupported":1,"sum":0}`, string(o.Row.TotalTokens): tokenAbsent} {
		if got != want {
			t.Fatalf("token %s, want %s", got, want)
		}
	}
	if string(o.Row.Responses) != "1" || string(o.Row.Launches) != "null" || o.Row.Duration.Kind != "unavailable" || o.Row.ErrorCategory != "none" {
		t.Fatalf("coverage: %+v", o.Row)
	}
	out, _ := json.Marshal(o)
	if strings.Contains(string(out), "PRIVATE") {
		t.Fatalf("private source leaked: %s", out)
	}
}

func TestClaudeRuntimeRejectsStaleTranscriptUsage(t *testing.T) {
	stale := []byte(`{"type":"assistant","message":{"model":"claude-opus-5","content":[{"type":"text","text":"STALE_MESSAGE"}],"usage":{"input_tokens":99,"output_tokens":99}}}` + "\n")
	for _, input := range []string{
		claudeSubagentHook,
		strings.Replace(claudeSubagentHook, `"hook_event_name":"SubagentStop","stop_hook_active":false,"agent_id":"PRIVATE_AGENT_ID","agent_type":"sdd-apply","agent_transcript_path":"PRIVATE_AGENT_PATH"`, `"hook_event_name":"Stop","stop_hook_active":false`, 1),
	} {
		hook, err := ParseClaudeHook(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if usage, ok := ParseClaudeTranscriptTail(stale, false, hook.LastAssistantDigest); ok || usage.Evidence {
			t.Fatalf("%s stale usage accepted: %+v", hook.HookEventName, usage)
		}
		o := NormalizeClaude(hook, ClaudeUsage{}, nil)
		if string(o.Row.Launches) != "1" || string(o.Row.Responses) != "null" || string(o.Row.Input) != tokenAbsent {
			t.Fatalf("%s stale transcript did not fall back: %+v", hook.HookEventName, o.Row)
		}
	}
}

func TestClaudeRuntimeStopNeverAttributesMatchingTranscript(t *testing.T) {
	input := strings.Replace(claudeSubagentHook, `"hook_event_name":"SubagentStop","stop_hook_active":false,"agent_id":"PRIVATE_AGENT_ID","agent_type":"sdd-apply","agent_transcript_path":"PRIVATE_AGENT_PATH"`, `"hook_event_name":"Stop","stop_hook_active":false`, 1)
	hook, err := ParseClaudeHook(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	repeated := []byte(`{"type":"assistant","message":{"model":"claude-opus-5","content":"FINAL_MESSAGE","usage":{"input_tokens":77,"output_tokens":88}}}` + "\n")
	usage, ok := ParseClaudeTranscriptTail(repeated, false, hook.LastAssistantDigest)
	if !ok {
		t.Fatal("fixture did not produce matching usage")
	}
	o := NormalizeClaude(hook, usage, nil)
	if string(o.Row.Launches) != "1" || string(o.Row.Responses) != "null" || o.Row.ModelEvidence != "unknown" || o.Row.Model.ID != "unknown" || string(o.Row.Input) != tokenAbsent || string(o.Row.Output) != tokenAbsent {
		t.Fatalf("Stop attributed replayable transcript evidence: %+v", o.Row)
	}
}

func TestClaudeRuntimeSubagentCorrelatesMultipleTextBlocks(t *testing.T) {
	hook, err := ParseClaudeHook(strings.NewReader(claudeSubagentHook))
	if err != nil {
		t.Fatal(err)
	}
	transcript := []byte(`{"type":"assistant","message":{"model":"claude-opus-5","content":[{"type":"text","text":"FINAL_"},{"type":"text","text":"MESSAGE"}],"usage":{"input_tokens":55}}}` + "\n")
	usage, ok := ParseClaudeTranscriptTail(transcript, false, hook.LastAssistantDigest)
	if !ok || string(usage.Input) != "55" {
		t.Fatalf("multi-block correlation failed: %+v %v", usage, ok)
	}
}

func TestClaudeRuntimeSubagentRejectsNonFinalMatchingUsage(t *testing.T) {
	hook, err := ParseClaudeHook(strings.NewReader(claudeSubagentHook))
	if err != nil {
		t.Fatal(err)
	}
	matching := `{"type":"assistant","message":{"model":"claude-opus-5","content":"FINAL_MESSAGE","usage":{"input_tokens":66}}}`
	for name, transcript := range map[string]string{
		"later valid record":        matching + "\n" + `{"type":"user","message":{"content":"next"}}` + "\n",
		"partial current assistant": matching + "\n" + `{"type":"assistant","message":{"content":"FINAL_MESSAGE"`,
	} {
		t.Run(name, func(t *testing.T) {
			usage, ok := ParseClaudeTranscriptTail([]byte(transcript), false, hook.LastAssistantDigest)
			if ok || usage.Evidence {
				t.Fatalf("non-final usage accepted: %+v", usage)
			}
			o := NormalizeClaude(hook, ClaudeUsage{}, nil)
			if string(o.Row.Launches) != "1" || string(o.Row.Responses) != "null" || string(o.Row.Input) != tokenAbsent {
				t.Fatalf("non-final usage did not remain activity-only: %+v", o.Row)
			}
		})
	}
}

func TestClaudeRuntimeNoUsageBecomesLaunchStyle(t *testing.T) {
	for _, input := range []string{
		claudeSubagentHook,
		strings.Replace(claudeSubagentHook, `"hook_event_name":"SubagentStop","stop_hook_active":false,"agent_id":"PRIVATE_AGENT_ID","agent_type":"sdd-apply","agent_transcript_path":"PRIVATE_AGENT_PATH"`, `"hook_event_name":"Stop","stop_hook_active":false`, 1),
	} {
		hook, err := ParseClaudeHook(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		o := NormalizeClaude(hook, ClaudeUsage{}, nil)
		if o == nil || string(o.Row.Launches) != "1" || string(o.Row.Responses) != "null" {
			t.Fatalf("no-usage coverage: %+v", o)
		}
		if hook.HookEventName == "Stop" && (o.Row.AgentKind != "orchestrator" || o.Row.AgentClass != "orchestrator") {
			t.Fatalf("orchestrator mapping: %+v", o.Row)
		}
	}
}

func TestClaudeRuntimeAgentClassUsesCurrentAllowlist(t *testing.T) {
	for _, agentType := range strings.Split(runtimeAgentClasses, "|") {
		if agentType == "orchestrator" || agentType == "worker" || agentType == "explore" || agentType == "verify" || agentType == "unknown" {
			continue
		}
		input := strings.Replace(claudeSubagentHook, `"agent_type":"sdd-apply"`, `"agent_type":"`+agentType+`"`, 1)
		hook, err := ParseClaudeHook(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		o := NormalizeClaude(hook, ClaudeUsage{}, nil)
		if o.Row.AgentKind != "built_in" || o.Row.AgentClass != agentType {
			t.Fatalf("%s: %+v", agentType, o.Row)
		}
	}
	unknown, err := ParseClaudeHook(strings.NewReader(strings.Replace(claudeSubagentHook, `"agent_type":"sdd-apply"`, `"agent_type":"PRIVATE_CUSTOM"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	o := NormalizeClaude(unknown, ClaudeUsage{}, nil)
	if o.Row.AgentKind != "custom" || o.Row.AgentClass != "unknown" {
		t.Fatalf("unknown: %+v", o.Row)
	}
}

func TestClaudeRuntimeIgnoredAndBounds(t *testing.T) {
	hook, err := ParseClaudeHook(strings.NewReader(strings.Replace(claudeSubagentHook, "SubagentStop", "SubagentStart", 1)))
	if err != nil || NormalizeClaude(hook, ClaudeUsage{}, nil) != nil {
		t.Fatalf("ignored: %v", err)
	}
	for _, input := range []string{strings.Repeat("x", ClaudeMaxBytes+1), claudeSubagentHook + `{}`, strings.Replace(claudeSubagentHook, `"agent_id":"PRIVATE_AGENT_ID"`, `"agent_id":"x","agent_id":"y"`, 1)} {
		if _, err := ParseClaudeHook(strings.NewReader(input)); err == nil {
			t.Fatal("accepted invalid hook")
		}
	}
}

func TestClaudeTranscriptTailBoundsAndMalformedLines(t *testing.T) {
	if _, ok := ParseClaudeTranscriptTail(make([]byte, ClaudeTranscriptMaxBytes+1), false, sha256.Sum256([]byte("MATCH"))); ok {
		t.Fatal("accepted oversized tail")
	}
	line := `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":7,"output_tokens":8,"cache_read_input_tokens":9,"cache_creation_input_tokens":10}}}`
	usage, ok := ParseClaudeTranscriptTail([]byte("partial\n{bad\n"+strings.Replace(line, `"usage"`, `"content":"MATCH","usage"`, 1)+"\n"), true, sha256.Sum256([]byte("MATCH")))
	if !ok || string(usage.Input) != "7" {
		t.Fatalf("usage: %+v %v", usage, ok)
	}
}

func TestClaudeTranscriptExactBoundaryKeepsFirstRecord(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"model":"claude-opus-5","content":"MATCH","usage":{"input_tokens":31}}}` + "\n")
	usage, ok := ParseClaudeTranscriptTail(line, false, sha256.Sum256([]byte("MATCH")))
	if !ok || string(usage.Input) != "31" {
		t.Fatalf("boundary record lost: %+v %v", usage, ok)
	}
}

func TestClaudeFrontmatterFallbackAndInvalidValues(t *testing.T) {
	hook, _ := ParseClaudeHook(strings.NewReader(claudeSubagentHook))
	o := NormalizeClaude(hook, ClaudeUsage{}, []byte("---\nname: sdd-apply\nmodel: claude-haiku-4-5\neffort: xhigh\n---\nignored"))
	if o.Row.Model.ID != "claude-haiku-4-5" || o.Row.ModelEvidence != "selected" || o.Row.SelectedEffort != "xhigh" {
		t.Fatalf("fallback: %+v", o.Row)
	}
	o = NormalizeClaude(hook, ClaudeUsage{}, []byte("---\nmodel: PRIVATE_MODEL\neffort: PRIVATE_EFFORT\n---\n"))
	if o.Row.Model != (RuntimeModel{Provider: "custom", ID: "custom"}) || o.Row.SelectedEffort != "unavailable" {
		t.Fatalf("custom: %+v", o.Row)
	}
	o = NormalizeClaude(hook, ClaudeUsage{Evidence: true, Input: json.RawMessage("1")}, []byte("---\nmodel: claude-haiku-4-5\neffort: low\n---\n"))
	if o.Row.Model.ID != "claude-haiku-4-5" || o.Row.ModelEvidence != "selected" || string(o.Row.Responses) != "1" {
		t.Fatalf("model fallback with usage: %+v", o.Row)
	}
}
