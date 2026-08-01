package main

import (
	"strings"
	"testing"
)

func TestSDDTaskResultAxisRegistration(t *testing.T) {
	for _, axis := range Axes() {
		if axis.Name != sddTaskResultAxis {
			continue
		}
		if axis.BlackBox || len(axis.Journeys()) != 1 || axis.Journeys()[0].ID != "tr01-sdd-empty-task-result" {
			t.Fatalf("SDD task-result axis = %+v", axis)
		}
		if !strings.Contains(axis.Properties[1], "GENTLE_AI_BENCH_SDD_PLUGIN") {
			t.Fatalf("axis does not document its skippable runtime dependency: %v", axis.Properties)
		}
		return
	}
	t.Fatal("SDD task-result axis is not registered")
}

func TestSDDTaskResultAxisSkipsWithoutExternalPlugin(t *testing.T) {
	t.Setenv("GENTLE_AI_BENCH_SDD_PLUGIN", "")
	if reason := sddTaskResultUnavailable(nil); reason == "" {
		t.Fatal("provider-backed task-result journey ran without its required installed plugin")
	}
}
