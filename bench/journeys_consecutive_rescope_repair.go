package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// These bytes were created by the v2.4.0-rc.1 binary in the #2839 RED run.
// The bench cannot create this state itself because the current writer rightly
// refuses it, so it verifies every copied record before exercising recovery.
//
//go:embed testdata/consecutive-rescope-rc1/HEAD testdata/consecutive-rescope-rc1/provenance.json testdata/consecutive-rescope-rc1/records/*.json
var consecutiveRescopeRC1 embed.FS

const (
	rc1ConsecutiveRescopeChange = "consecutive-rescope-repair"
	rc1ConsecutiveRescopeHead   = "sha256:da84114f95f9d1674cd23a3d06f5d92a3d5d36a029d5d40931500b91854ec622"
)

type rc1ConsecutiveRescopeManifest struct {
	Schema            string            `json:"schema"`
	PublicTag         string            `json:"public_tag"`
	Commit            string            `json:"commit"`
	GeneratorCommands []string          `json:"generator_commands"`
	OperationShape    []string          `json:"operation_shape"`
	Records           map[string]string `json:"records"`
}

func rc1ConsecutiveRescopeRecords() (map[string]string, error) {
	payload, err := consecutiveRescopeRC1.ReadFile("testdata/consecutive-rescope-rc1/provenance.json")
	if err != nil {
		return nil, err
	}
	var manifest rc1ConsecutiveRescopeManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return nil, fmt.Errorf("parse RC fixture provenance: %w", err)
	}
	if manifest.Schema != "gentle-ai.sdd-runtime-fixture-provenance/v1" || manifest.PublicTag != "v2.4.0-rc.1" || manifest.Commit != "3d1e673553c9afb0bf91a710121f415d6a7e4ed1" || len(manifest.GeneratorCommands) != 3 || len(manifest.OperationShape) != 4 || len(manifest.Records) != 4 {
		return nil, errors.New("invalid bounded RC fixture provenance")
	}
	for name, expected := range manifest.Records {
		payload, err := consecutiveRescopeRC1.ReadFile("testdata/consecutive-rescope-rc1/records/" + name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(payload)
		if expected != "sha256:"+hex.EncodeToString(sum[:]) || expected != "sha256:"+strings.TrimSuffix(name, ".json") {
			return nil, fmt.Errorf("RC fixture %s does not match provenance", name)
		}
	}
	return manifest.Records, nil
}

func rc1ConsecutiveRescopeStore(sandbox *Sandbox) error {
	if err := sandbox.initRepo(sandbox.Repo); err != nil {
		return err
	}
	if err := sandbox.write(filepath.Join(sandbox.Repo, "README.md"), "# RC fixture\n"); err != nil {
		return err
	}
	if err := sandbox.git(sandbox.Repo, "add", "README.md"); err != nil {
		return err
	}
	if err := sandbox.git(sandbox.Repo, "commit", "-qm", "fixture baseline"); err != nil {
		return err
	}
	common, err := gitCommonDir(sandbox, sandbox.Repo)
	if err != nil {
		return err
	}
	records, err := rc1ConsecutiveRescopeRecords()
	if err != nil {
		return err
	}
	destination := filepath.Join(common, "gentle-ai", "sdd-runtime", "v1", rc1ConsecutiveRescopeChange)
	for _, path := range []string{"HEAD"} {
		payload, err := consecutiveRescopeRC1.ReadFile("testdata/consecutive-rescope-rc1/" + path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, payload, 0o644); err != nil {
			return err
		}
	}
	for name := range records {
		payload, err := consecutiveRescopeRC1.ReadFile("testdata/consecutive-rescope-rc1/records/" + name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, "records", name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, payload, 0o644); err != nil {
			return err
		}
	}
	sandbox.Scratch["rc1-consecutive-rescope-record"] = filepath.Join(destination, "records", strings.TrimPrefix(rc1ConsecutiveRescopeHead, "sha256:")+".json")
	return nil
}

func runPrintedConsecutiveRescopeRepair(r *journeyRun) error {
	records, err := rc1ConsecutiveRescopeRecords()
	if err != nil {
		return err
	}
	recordDir := filepath.Dir(r.sandbox.Scratch["rc1-consecutive-rescope-record"])
	beforeRecords := map[string][]byte{}
	for name := range records {
		beforeRecords[name], err = os.ReadFile(filepath.Join(recordDir, name))
		if err != nil {
			return err
		}
	}
	headBefore, err := os.ReadFile(filepath.Join(filepath.Dir(recordDir), "HEAD"))
	if err != nil {
		return err
	}
	before := r.run([]string{"sdd-attempt", "status", "--cwd", r.sandbox.Repo, "--change", rc1ConsecutiveRescopeChange}, false)
	if before.ExitCode == 0 || !strings.Contains(before.Stderr, rc1ConsecutiveRescopeHead) {
		return fmt.Errorf("RC-shaped poisoned status = exit %d, stderr %q", before.ExitCode, firstLine(before.Stderr))
	}
	start := strings.Index(before.Stderr, "run `")
	if start < 0 {
		return errors.New("poisoned status named no runnable repair command")
	}
	command := before.Stderr[start+len("run `"):]
	if end := strings.Index(command, "`"); end >= 0 {
		command = command[:end]
	}
	args, err := printedCommandArguments(command)
	if err != nil {
		return fmt.Errorf("printed repair command: %w", err)
	}
	repaired := r.run(args, false)
	if repaired.ExitCode != 0 {
		return fmt.Errorf("printed repair command exit=%d: %s", repaired.ExitCode, firstLine(repaired.Stderr))
	}
	var repairedStatus struct {
		Revision         string `json:"revision"`
		DecisionRequired bool   `json:"decision_required"`
		NextAction       string `json:"next_action"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(repaired.Stdout)), &repairedStatus); err != nil || !repairedStatus.DecisionRequired || repairedStatus.NextAction != "reset" {
		return fmt.Errorf("repair result = %#v, parse=%v", repairedStatus, err)
	}
	entries, err := os.ReadDir(recordDir)
	if err != nil || len(entries) != len(beforeRecords)+1 {
		return fmt.Errorf("repair record count = %d, err=%v", len(entries), err)
	}
	headAfter, err := os.ReadFile(filepath.Join(filepath.Dir(recordDir), "HEAD"))
	if err != nil || bytes.Equal(headBefore, headAfter) {
		return fmt.Errorf("repair HEAD changed=%t, err=%v", !bytes.Equal(headBefore, headAfter), err)
	}
	for name, want := range beforeRecords {
		after, err := os.ReadFile(filepath.Join(recordDir, name))
		if err != nil || !bytes.Equal(after, want) {
			return fmt.Errorf("repair changed preserved RC record %s: %v", name, err)
		}
	}
	status := r.run([]string{"sdd-attempt", "status", "--cwd", r.sandbox.Repo, "--change", rc1ConsecutiveRescopeChange}, false)
	if status.ExitCode != 0 || !strings.Contains(status.Stdout, `"last_repair"`) || !strings.Contains(status.Stdout, `"decision_required": true`) || !strings.Contains(status.Stdout, `"next_action": "reset"`) {
		return fmt.Errorf("status after printed repair = exit %d, stdout %q, stderr %q", status.ExitCode, firstLine(status.Stdout), firstLine(status.Stderr))
	}
	reset := r.run([]string{"sdd-attempt", "reset", "--cwd", r.sandbox.Repo, "--change", rc1ConsecutiveRescopeChange, "--expected-revision", repairedStatus.Revision, "--request-id", "bench-2839-reset", "--actor", "bench-maintainer", "--reason", "reset repaired exhausted objective"}, false)
	if reset.ExitCode != 0 || !strings.Contains(reset.Stdout, `"next_action": "begin"`) {
		return fmt.Errorf("reset after repair = exit %d, stdout %q, stderr %q", reset.ExitCode, firstLine(reset.Stdout), firstLine(reset.Stderr))
	}
	var resetStatus struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(reset.Stdout)), &resetStatus); err != nil {
		return fmt.Errorf("parse reset after repair: %w", err)
	}
	if begin := r.run([]string{"sdd-attempt", "begin", "--cwd", r.sandbox.Repo, "--change", rc1ConsecutiveRescopeChange, "--expected-revision", resetStatus.Revision, "--request-id", "bench-2839-begin", "--work-unit", "objective-b", "--evidence-goal", "prove-b", "--max-attempts", "1", "--max-changed-lines", "10"}, false); begin.ExitCode != 0 {
		return fmt.Errorf("begin after reset = exit %d: %s", begin.ExitCode, firstLine(begin.Stderr))
	}
	return nil
}

func consecutiveRescopeRepairJourneys() []Journey {
	return []Journey{{
		ID:     "j81-rc1-consecutive-rescope-repair-executes-printed-command",
		Title:  "RC-created poison: status names and executes the audited repair without rewriting C",
		Source: "issue #2839; fixture extracted byte-for-byte from the v2.4.0-rc.1 RED run",
		Steps: []Step{
			{Name: "fixture: exact RC-created consecutive-rescope records", Fixture: rc1ConsecutiveRescopeStore},
			{Name: "run printed repair, audited reset, then begin", Composite: runPrintedConsecutiveRescopeRepair},
		},
	}}
}
