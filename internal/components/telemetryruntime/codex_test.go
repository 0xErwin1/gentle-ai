package telemetryruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
	"github.com/gentleman-programming/gentle-ai/v2/internal/telemetry"
)

func codexBridgeHome(t *testing.T, enabled bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("GENTLE_AI_TELEMETRY", "")
	if err := telemetry.Save(home, telemetry.State{InstallID: "PRIVATE_INSTALL", Enabled: enabled, NoticeShown: true}); err != nil {
		t.Fatal(err)
	}
	return home
}

func codexBridgeHook(path string) string {
	encoded, _ := json.Marshal(map[string]any{
		"session_id": "PRIVATE_SESSION", "transcript_path": "PRIVATE_PARENT", "cwd": "PRIVATE_CWD",
		"hook_event_name": "SubagentStop", "model": "gpt-5.6-sol", "permission_mode": "default", "turn_id": "PRIVATE_TURN",
		"agent_id": "PRIVATE_AGENT", "agent_type": "sdd-apply", "agent_transcript_path": path,
		"stop_hook_active": false, "last_assistant_message": "PRIVATE_MESSAGE",
	})
	return string(encoded)
}

func codexStopBridgeHook(path string) string {
	encoded, _ := json.Marshal(map[string]any{
		"session_id": "PRIVATE_SESSION", "transcript_path": path, "cwd": "PRIVATE_CWD",
		"hook_event_name": "Stop", "model": "gpt-5.6-sol", "permission_mode": "default", "turn_id": "PRIVATE_TURN",
		"stop_hook_active": false, "last_assistant_message": "PRIVATE_MESSAGE",
	})
	return string(encoded)
}

type codexRoundTrip func(*http.Request) (*http.Response, error)

func (f codexRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSendCodexPolicyNormalizeAndSendOnce(t *testing.T) {
	home := codexBridgeHome(t, true)
	if err := state.Write(home, state.InstallState{
		CodexModelAssignments:      map[string]string{"sdd-apply": "medium"},
		CodexPhaseModelAssignments: map[string]string{"sdd-apply": "gpt-5.6-terra"},
	}); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := `{"type":"turn_context","payload":{"model":"gpt-5.6-sol","effort":"high","summary":"PRIVATE_SUMMARY"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}}}}
`
	if err := os.WriteFile(transcript, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	client := &http.Client{Transport: codexRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		body, _ := io.ReadAll(r.Body)
		event, err := telemetry.ParseRuntimeEvent(body)
		if err != nil || event.Host != "codex" || len(event.Rows) != 1 {
			t.Fatalf("event=%+v err=%v", event, err)
		}
		row := event.Rows[0]
		if row.AgentClass != "sdd-apply" || row.SelectedEffort != "medium" || row.EffectiveEffort != "high" || row.Model.ID != "gpt-5.6-sol" || row.ModelEvidence != "response" {
			t.Fatalf("row=%+v", row)
		}
		for _, private := range []string{"PRIVATE", transcript, "transcript_path", "agent_id", "last_assistant_message"} {
			if bytes.Contains(body, []byte(private)) {
				t.Fatalf("private data leaked: %s", body)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"schema":"gentle-ai.telemetry-runtime-delivery/v1","decision":"stored"}`))}, nil
	})}
	t.Setenv(telemetry.EndpointEnvVar, "https://telemetry.example.invalid")
	before := codexBridgeDisk(t, home)
	if got := SendCodex(context.Background(), home, os.Getenv, strings.NewReader(codexBridgeHook(transcript)), client); got != "stored" || requests != 1 {
		t.Fatalf("decision=%q requests=%d", got, requests)
	}
	if !reflect.DeepEqual(before, codexBridgeDisk(t, home)) {
		t.Fatal("Codex bridge mutated disk")
	}
}

type codexNoRead struct{ t *testing.T }

func (r codexNoRead) Read([]byte) (int, error) {
	r.t.Fatal("read before telemetry policy")
	return 0, io.EOF
}

func TestSendCodexPolicyBeforeRead(t *testing.T) {
	home := codexBridgeHome(t, false)
	if got := SendCodex(context.Background(), home, os.Getenv, codexNoRead{t}, nil); got != "disabled" {
		t.Fatalf("decision=%q", got)
	}
}

func TestSendCodexSelectedModelFallbackAndUnknownAgent(t *testing.T) {
	home := codexBridgeHome(t, true)
	if err := state.Write(home, state.InstallState{
		CodexModelAssignments:      map[string]string{"sdd-apply": "xhigh"},
		CodexPhaseModelAssignments: map[string]string{"sdd-apply": "gpt-5.4"},
	}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: codexRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		event, err := telemetry.ParseRuntimeEvent(body)
		if err != nil {
			t.Fatal(err)
		}
		row := event.Rows[0]
		if row.Model.ID != "gpt-5.4" || row.ModelEvidence != "selected" || row.SelectedEffort != "xhigh" {
			t.Fatalf("row=%+v", row)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"schema":"gentle-ai.telemetry-runtime-delivery/v1","decision":"stored"}`))}, nil
	})}
	t.Setenv(telemetry.EndpointEnvVar, "https://telemetry.example.invalid")
	if got := SendCodex(context.Background(), home, os.Getenv, strings.NewReader(codexBridgeHook(filepath.Join(t.TempDir(), "missing.jsonl"))), client); got != "stored" {
		t.Fatal(got)
	}
}

func TestSendCodexStopReadsTranscriptAsOrchestratorUsage(t *testing.T) {
	home := codexBridgeHome(t, true)
	transcript := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := `{"type":"turn_context","payload":{"model":"gpt-5.6-sol","effort":"high"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":7,"total_tokens":9}}}}
`
	if err := os.WriteFile(transcript, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: codexRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		event, err := telemetry.ParseRuntimeEvent(body)
		if err != nil {
			t.Fatal(err)
		}
		row := event.Rows[0]
		if row.AgentKind != "orchestrator" || row.AgentClass != "orchestrator" || row.Model.ID != "gpt-5.6-sol" || string(row.Input) != tokenReportedBridge("7") || string(row.TotalTokens) != tokenReportedBridge("9") {
			t.Fatalf("row=%+v", row)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"schema":"gentle-ai.telemetry-runtime-delivery/v1","decision":"stored"}`))}, nil
	})}
	t.Setenv(telemetry.EndpointEnvVar, "https://telemetry.example.invalid")
	if got := SendCodex(context.Background(), home, os.Getenv, strings.NewReader(codexStopBridgeHook(transcript)), client); got != "stored" {
		t.Fatal(got)
	}
}

func TestCodexTranscriptTailIsBoundedAndDropsPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	prefix := bytes.Repeat([]byte("x"), telemetry.CodexTranscriptMaxBytes)
	want := []byte("{\"type\":\"turn_context\",\"payload\":{\"model\":\"gpt-5.4\",\"effort\":\"low\"}}\n")
	if err := os.WriteFile(path, append(append(prefix, '\n'), want...), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readCodexTranscriptTail(path)
	if err != nil || !bytes.Equal(got, want) || len(got) > telemetry.CodexTranscriptMaxBytes {
		t.Fatalf("tail len=%d err=%v got-prefix=%q", len(got), err, got[:min(len(got), 32)])
	}
}

func TestCodexTranscriptTailKeepsRecordAtExactBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	record := []byte("{\"type\":\"turn_context\",\"payload\":{\"model\":\"gpt-5.6-sol\",\"effort\":\"high\"}}\n")
	want := append([]byte(nil), record...)
	want = append(want, bytes.Repeat([]byte("\n"), telemetry.CodexTranscriptMaxBytes-len(want))...)
	if err := os.WriteFile(path, append([]byte("discarded\n"), want...), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readCodexTranscriptTail(path)
	if err != nil || !bytes.Equal(got, want) || len(got) != telemetry.CodexTranscriptMaxBytes {
		t.Fatalf("tail len=%d err=%v starts-with-record=%t", len(got), err, bytes.HasPrefix(got, record))
	}
}

func tokenReportedBridge(value string) string {
	return `{"reported":1,"unavailable":0,"unsupported":0,"sum":` + value + `}`
}

func codexBridgeDisk(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result[rel] = info.Mode().String()
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[rel] += string(data)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
