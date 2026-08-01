package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSDDTaskResultAxisRegistration(t *testing.T) {
	for _, axis := range Axes() {
		if axis.Name != sddTaskResultAxis {
			continue
		}
		if axis.BlackBox || len(axis.Journeys()) != 1 || axis.Journeys()[0].ID != "tr01-sdd-empty-task-result" ||
			!strings.Contains(axis.Properties[1], "GENTLE_AI_BENCH_SDD_PLUGIN") || !strings.Contains(axis.Properties[1], "type stripping") {
			t.Fatalf("SDD task-result axis contract = %+v", axis)
		}
		t.Setenv("GENTLE_AI_BENCH_SDD_PLUGIN", "")
		if reason := sddTaskResultUnavailable(nil); reason == "" {
			t.Fatal("provider-backed task-result journey ran without its required installed plugin")
		}
		return
	}
	t.Fatal("SDD task-result axis is not registered")
}

func TestSDDTaskResultNodeCapabilityRejectsRuntimeWithoutTypeStripping(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Node runtime requires a POSIX shell")
	}
	node := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf '%s\\n' 'TypeScript unsupported' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if reason := sddTaskResultNodeUnavailable(node); !strings.Contains(reason, "cannot execute .mts TypeScript with type stripping") || !strings.Contains(reason, "TypeScript unsupported") {
		t.Fatalf("unsupported Node runtime reason = %q", reason)
	}
}
