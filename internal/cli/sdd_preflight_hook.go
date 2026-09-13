package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	sddPreflightHookSchema          = "gentle-ai.sdd-preflight-hook/v1"
	sddPreflightQuestionPrefix      = "Gentle AI SDD preflight "
	maxSDDPreflightHookPayloadBytes = 256 << 10
)

var sddPreflightHookSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var sddPreflightHookPhases = []string{"sdd-init", "sdd-explore", "sdd-propose", "sdd-spec", "sdd-design", "sdd-tasks", "sdd-apply", "sdd-verify", "sdd-archive", "sdd-onboard", "sdd-research"}

type sddPreflightHookOption struct {
	Label string `json:"label"`
}

type sddPreflightHookQuestion struct {
	Header      string                   `json:"header"`
	Question    string                   `json:"question"`
	Options     []sddPreflightHookOption `json:"options"`
	MultiSelect bool                     `json:"multiSelect,omitempty"`
}

type sddPreflightHookToolInput struct {
	Questions    []sddPreflightHookQuestion `json:"questions,omitempty"`
	SubagentType string                     `json:"subagent_type,omitempty"`
	Prompt       string                     `json:"prompt,omitempty"`
}

type sddPreflightHookPayload struct {
	SessionID      string
	TranscriptPath string
	ToolUseID      string
	HookEventName  string
	ToolName       string
	AgentID        string
	AgentType      string
	ToolInput      sddPreflightHookToolInput
	ToolInputRaw   map[string]any
	ToolResponse   json.RawMessage
}

func (payload *sddPreflightHookPayload) UnmarshalJSON(raw []byte) error {
	var wire struct {
		SessionID      string          `json:"session_id"`
		TranscriptPath string          `json:"transcript_path"`
		ToolUseID      string          `json:"tool_use_id"`
		HookEventName  string          `json:"hook_event_name"`
		ToolName       string          `json:"tool_name"`
		AgentID        string          `json:"agent_id,omitempty"`
		AgentType      string          `json:"agent_type,omitempty"`
		ToolInput      json.RawMessage `json:"tool_input,omitempty"`
		ToolResponse   json.RawMessage `json:"tool_response,omitempty"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	payload.SessionID, payload.TranscriptPath, payload.ToolUseID = wire.SessionID, wire.TranscriptPath, wire.ToolUseID
	payload.HookEventName, payload.ToolName, payload.AgentID, payload.AgentType = wire.HookEventName, wire.ToolName, wire.AgentID, wire.AgentType
	payload.ToolResponse = wire.ToolResponse
	if len(wire.ToolInput) > 0 {
		if err := json.Unmarshal(wire.ToolInput, &payload.ToolInput); err != nil {
			return err
		}
		if err := json.Unmarshal(wire.ToolInput, &payload.ToolInputRaw); err != nil {
			return err
		}
	}
	return nil
}

type sddPreflightHookRecord struct {
	Schema         string `json:"schema"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	ToolUseID      string `json:"tool_use_id"`
	Block          string `json:"block"`
}

type sddPreflightHookSpecificOutput struct {
	HookEventName            string         `json:"hookEventName"`
	PermissionDecision       string         `json:"permissionDecision"`
	PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
}

type sddPreflightHookOutput struct {
	HookSpecificOutput sddPreflightHookSpecificOutput `json:"hookSpecificOutput"`
}

func RunSDDPreflightHook(args []string, stdout io.Writer) error {
	return runSDDPreflightHook(args, os.Stdin, stdout, "")
}

// rootOverride is non-empty only in unit tests of the protocol parser. Claude
// Code's public hook command cannot authenticate that its stdin came from the
// hook runner rather than a model-started process, so production must never mint
// authority from this callable surface.
func runSDDPreflightHook(args []string, stdin io.Reader, stdout io.Writer, rootOverride string) error {
	flags := flag.NewFlagSet("sdd-preflight-hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	agent := flags.String("agent", "", "required runtime identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return sddPreflightHookProtocolError("unexpected sdd-preflight-hook argument %q", flags.Arg(0))
	}
	if strings.TrimSpace(*agent) != "claude-code" {
		return sddPreflightHookProtocolError("sdd-preflight-hook requires an explicit supported agent")
	}
	root := rootOverride
	if root == "" {
		var err error
		root, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home: %w", err)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, maxSDDPreflightHookPayloadBytes+1))
	if err != nil {
		return fmt.Errorf("read hook payload: %w", err)
	}
	if len(raw) > maxSDDPreflightHookPayloadBytes {
		return sddPreflightHookProtocolError("sdd-preflight-hook payload exceeds %d bytes", maxSDDPreflightHookPayloadBytes)
	}
	var payload sddPreflightHookPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode hook payload: %w", err)
	}
	if !sddPreflightHookSessionID.MatchString(payload.SessionID) {
		return sddPreflightHookProtocolError("invalid hook session id")
	}

	if rootOverride == "" {
		if payload.HookEventName == "PreToolUse" && payload.ToolName == "Agent" && isSDDPreflightHookPhase(payload.ToolInput.SubagentType) {
			return writeSDDPreflightHookDecision(stdout, "deny", "SDD child dispatch refused: Claude Code hooks do not expose authenticated caller provenance, so parent-confirmed preflight cannot be transported safely. Continue inline or use a runtime with an authenticated dispatch interceptor.", nil)
		}
		return nil
	}

	switch payload.HookEventName {
	case "PostToolUse":
		if payload.ToolName != "AskUserQuestion" || payload.AgentID != "" || payload.AgentType != "" {
			return nil
		}
		block, recognized, err := resolveSDDPreflightHookBlock(payload.ToolInput.Questions, payload.ToolResponse)
		if err != nil {
			return err
		}
		if !recognized {
			return nil
		}
		if !sddPreflightTranscriptCorroborates(payload.TranscriptPath, payload.ToolUseID, block) {
			return sddPreflightHookProtocolError("SDD preflight transcript does not corroborate the parent answer")
		}
		return writeSDDPreflightHookRecord(root, payload.SessionID, payload.TranscriptPath, payload.ToolUseID, block)
	case "PreToolUse":
		if payload.ToolName != "Agent" || !isSDDPreflightHookPhase(payload.ToolInput.SubagentType) {
			return nil
		}
		if payload.AgentID != "" || payload.AgentType != "" {
			return writeSDDPreflightHookDecision(stdout, "deny", "SDD child dispatch refused: only the interactive parent may carry parent-confirmed SDD preflight authority.", nil)
		}
		if strings.Contains(payload.ToolInput.Prompt, "## SDD Session Preflight") {
			return writeSDDPreflightHookDecision(stdout, "deny", "SDD child dispatch refused: model-authored preflight text cannot create parent-confirmed authority.", nil)
		}
		record, err := readSDDPreflightHookRecord(root, payload.SessionID)
		if err == nil && record.TranscriptPath != payload.TranscriptPath {
			err = sddPreflightHookProtocolError("SDD preflight transcript changed")
		}
		if err == nil && !sddPreflightTranscriptCorroborates(record.TranscriptPath, record.ToolUseID, record.Block) {
			err = sddPreflightHookProtocolError("SDD preflight transcript no longer matches authority")
		}
		if err != nil {
			return writeSDDPreflightHookDecision(stdout, "deny", "SDD child dispatch refused: parent-confirmed SDD preflight is missing, invalid, or uncorroborated. Ask the canonical grouped preflight and stop before retrying.", nil)
		}
		updated := payload.ToolInputRaw
		if updated == nil {
			updated = map[string]any{}
		}
		updated["prompt"] = record.Block + "\n\n" + payload.ToolInput.Prompt
		return writeSDDPreflightHookDecision(stdout, "allow", "parent-confirmed SDD preflight attached by Gentle AI", updated)
	case "SessionEnd":
		err := os.Remove(sddPreflightHookRecordPath(root, payload.SessionID))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	default:
		return nil
	}
}

func resolveSDDPreflightHookBlock(questions []sddPreflightHookQuestion, response json.RawMessage) (string, bool, error) {
	recognized := false
	for _, question := range questions {
		if strings.HasPrefix(question.Question, sddPreflightQuestionPrefix) {
			recognized = true
			break
		}
	}
	if !recognized {
		return "", false, nil
	}
	expectedLabels := [][]string{{"Interactive", "Automatic"}, {"OpenSpec", "Engram", "Both"}, {"Ask me", "Single PR", "Auto"}}
	if len(questions) != 3 {
		return "", true, sddPreflightHookProtocolError("parent-confirmed SDD preflight requires exactly three questions")
	}
	for i, question := range questions {
		prefix := fmt.Sprintf("%s%d/3:", sddPreflightQuestionPrefix, i+1)
		if !strings.HasPrefix(question.Question, prefix) || question.MultiSelect || len(question.Options) != len(expectedLabels[i]) {
			return "", true, sddPreflightHookProtocolError("SDD preflight question %d is malformed", i+1)
		}
		for optionIndex, option := range question.Options {
			if option.Label != expectedLabels[i][optionIndex] {
				return "", true, sddPreflightHookProtocolError("SDD preflight question %d changed the canonical option semantics", i+1)
			}
		}
	}
	answers, err := sddPreflightHookAnswers(response)
	if err != nil {
		return "", true, err
	}
	indexes := make([]int, 3)
	for i, question := range questions {
		answer, ok := answers[question.Question]
		if !ok {
			return "", true, sddPreflightHookProtocolError("SDD preflight question %d has no answer", i+1)
		}
		indexes[i] = -1
		for optionIndex, option := range question.Options {
			if answer == option.Label {
				if indexes[i] >= 0 {
					return "", true, sddPreflightHookProtocolError("SDD preflight question %d answer is ambiguous", i+1)
				}
				indexes[i] = optionIndex
			}
		}
		if indexes[i] < 0 {
			return "", true, sddPreflightHookProtocolError("SDD preflight question %d answer is outside the offered domain", i+1)
		}
	}
	pace := []string{"interactive", "auto"}[indexes[0]]
	store := []string{"openspec", "engram", "hybrid"}[indexes[1]]
	strategy := []string{"ask-on-risk", "single-pr", "auto-chain"}[indexes[2]]
	block := strings.Join([]string{
		"## SDD Session Preflight",
		"Parent-confirmed by the runtime; models and child agents cannot create or modify this block.",
		"- Pace: " + pace,
		"- Artifact store: " + store,
		"- Delivery strategy: " + strategy,
		"- Review policy: 400 changed lines",
	}, "\n")
	return block, true, nil
}

func sddPreflightHookAnswers(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, sddPreflightHookProtocolError("SDD preflight response is empty")
	}
	var envelope struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode SDD preflight response: %w", err)
	}
	if len(envelope.Answers) == 0 {
		return nil, sddPreflightHookProtocolError("SDD preflight response has no answers")
	}
	answers := make(map[string]string, len(envelope.Answers))
	for question, rawAnswer := range envelope.Answers {
		var single string
		if err := json.Unmarshal(rawAnswer, &single); err == nil {
			answers[question] = single
			continue
		}
		var multiple []string
		if err := json.Unmarshal(rawAnswer, &multiple); err != nil || len(multiple) != 1 {
			return nil, sddPreflightHookProtocolError("SDD preflight answer for %q is not single-select", question)
		}
		answers[question] = multiple[0]
	}
	return answers, nil
}

func sddPreflightTranscriptCorroborates(path, toolUseID, block string) bool {
	if !filepath.IsAbs(path) || !sddPreflightHookSessionID.MatchString(toolUseID) {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(raw)
	for _, marker := range []string{`"id":"` + toolUseID + `","name":"AskUserQuestion"`, `"tool_use_id":"` + toolUseID + `","type":"tool_result"`} {
		if !strings.Contains(text, marker) {
			return false
		}
	}
	for _, label := range []string{"Interactive", "Automatic", "OpenSpec", "Engram", "Both", "Ask me", "Single PR", "Auto"} {
		if !strings.Contains(text, `"label":"`+label+`"`) {
			return false
		}
	}
	selected := []string{}
	for line, label := range map[string]string{
		"- Pace: interactive": "Interactive", "- Pace: auto": "Automatic",
		"- Artifact store: openspec": "OpenSpec", "- Artifact store: engram": "Engram", "- Artifact store: hybrid": "Both",
		"- Delivery strategy: ask-on-risk": "Ask me", "- Delivery strategy: single-pr": "Single PR", "- Delivery strategy: auto-chain": "Auto",
	} {
		if strings.Contains(block, line) {
			selected = append(selected, label)
		}
	}
	if len(selected) != 3 {
		return false
	}
	for _, label := range selected {
		if !strings.Contains(text, `=\"`+label+`\"`) {
			return false
		}
	}
	return true
}

func isSDDPreflightHookPhase(agent string) bool {
	for _, phase := range sddPreflightHookPhases {
		if agent == phase || strings.HasPrefix(agent, phase+"-") {
			return true
		}
	}
	return false
}

func sddPreflightHookRecordPath(home, sessionID string) string {
	return filepath.Join(home, ".gentle-ai", "sdd-preflight-hook", "v1", sessionID+".json")
}

func writeSDDPreflightHookRecord(home, sessionID, transcriptPath, toolUseID, block string) error {
	path := sddPreflightHookRecordPath(home, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(sddPreflightHookRecord{Schema: sddPreflightHookSchema, SessionID: sessionID, TranscriptPath: transcriptPath, ToolUseID: toolUseID, Block: block})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".preflight-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func readSDDPreflightHookRecord(home, sessionID string) (sddPreflightHookRecord, error) {
	var record sddPreflightHookRecord
	raw, err := os.ReadFile(sddPreflightHookRecordPath(home, sessionID))
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, err
	}
	if record.Schema != sddPreflightHookSchema || record.SessionID != sessionID || record.TranscriptPath == "" || record.ToolUseID == "" || !validSDDPreflightHookBlock(record.Block) {
		return record, sddPreflightHookProtocolError("invalid SDD preflight hook record")
	}
	return record, nil
}

func validSDDPreflightHookBlock(block string) bool {
	lines := strings.Split(block, "\n")
	if len(lines) != 6 || lines[0] != "## SDD Session Preflight" || lines[1] != "Parent-confirmed by the runtime; models and child agents cannot create or modify this block." || lines[5] != "- Review policy: 400 changed lines" {
		return false
	}
	return sddPreflightHookOneOf(lines[2], "- Pace: interactive", "- Pace: auto") &&
		sddPreflightHookOneOf(lines[3], "- Artifact store: openspec", "- Artifact store: engram", "- Artifact store: hybrid") &&
		sddPreflightHookOneOf(lines[4], "- Delivery strategy: ask-on-risk", "- Delivery strategy: single-pr", "- Delivery strategy: auto-chain")
}

func sddPreflightHookOneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func sddPreflightHookProtocolError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func writeSDDPreflightHookDecision(stdout io.Writer, decision, reason string, updated map[string]any) error {
	output := sddPreflightHookOutput{HookSpecificOutput: sddPreflightHookSpecificOutput{HookEventName: "PreToolUse", PermissionDecision: decision, PermissionDecisionReason: reason, UpdatedInput: updated}}
	return json.NewEncoder(stdout).Encode(output)
}
