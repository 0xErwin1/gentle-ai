package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
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
//go:embed testdata/consecutive-rescope-rc1/HEAD testdata/consecutive-rescope-rc1/records/*.json
var consecutiveRescopeRC1 embed.FS

const (
	rc1ConsecutiveRescopeChange = "consecutive-rescope-repair"
	rc1ConsecutiveRescopeHead   = "sha256:da84114f95f9d1674cd23a3d06f5d92a3d5d36a029d5d40931500b91854ec622"
)

var sddAttemptRepairCapability = &Capability{
	Verb:  []string{"sdd-attempt", "repair"},
	Probe: []string{"sdd-attempt", "repair", "--expected-revision=probe", "--request-id=probe", "--actor=bench", "--reason=probe"},
}

func rc1ConsecutiveRescopeStore(sandbox *Sandbox) error {
	if err := sandbox.initRepo(sandbox.Repo); err != nil {
		return err
	}
	common, err := gitCommonDir(sandbox, sandbox.Repo)
	if err != nil {
		return err
	}
	destination := filepath.Join(common, "gentle-ai", "sdd-runtime", "v1", rc1ConsecutiveRescopeChange)
	paths := []string{"HEAD", "records/00357f75e9bd3b44b2e1a752fb22476547041deb9b062a246c9f21c70d225640.json", "records/2d5661e29641c65b4a0da2ddfe9d94e2ab0b429e3c1093d7400a7142d1c325bb.json", "records/da84114f95f9d1674cd23a3d06f5d92a3d5d36a029d5d40931500b91854ec622.json", "records/ff5759db66fe3beed65d5ae132e066457cfa81695673ebd778a9b7d7bcc96abd.json"}
	for _, path := range paths {
		payload, err := consecutiveRescopeRC1.ReadFile("testdata/consecutive-rescope-rc1/" + path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(path, "records/") {
			sum := sha256.Sum256(payload)
			if hex.EncodeToString(sum[:]) != strings.TrimSuffix(filepath.Base(path), ".json") {
				return fmt.Errorf("RC fixture %s does not match its content-addressed filename", path)
			}
		}
		target := filepath.Join(destination, path)
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
	if repaired := r.run(args, false); repaired.ExitCode != 0 {
		return fmt.Errorf("printed repair command exit=%d: %s", repaired.ExitCode, firstLine(repaired.Stderr))
	}
	status := r.run([]string{"sdd-attempt", "status", "--cwd", r.sandbox.Repo, "--change", rc1ConsecutiveRescopeChange}, false)
	if status.ExitCode != 0 || !strings.Contains(status.Stdout, `"last_repair"`) || !strings.Contains(status.Stdout, `"next_action": "begin"`) {
		return fmt.Errorf("status after printed repair = exit %d, stdout %q, stderr %q", status.ExitCode, firstLine(status.Stdout), firstLine(status.Stderr))
	}
	path := r.sandbox.Scratch["rc1-consecutive-rescope-record"]
	after, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	beforeBytes, err := consecutiveRescopeRC1.ReadFile("testdata/consecutive-rescope-rc1/records/" + strings.TrimPrefix(rc1ConsecutiveRescopeHead, "sha256:") + ".json")
	if err != nil || !bytes.Equal(after, beforeBytes) {
		return fmt.Errorf("repair changed the preserved RC record: %v", err)
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
			{Name: "run the repair command printed by unreadable status", Requires: sddAttemptRepairCapability, Composite: runPrintedConsecutiveRescopeRepair},
		},
	}}
}
