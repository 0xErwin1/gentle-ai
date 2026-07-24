package sdd

import (
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/assets"
)

func TestRenderTriggerRulesProjectsNativeOrganicRouting(t *testing.T) {
	t.Parallel()

	rendered := RenderTriggerRules()
	for _, want := range []string{
		"## Agent Trigger Rules",
		"decide or verify from 1–3 files inline",
		"understanding needs 4+ files",
		"one writer for 2+ non-trivial files",
		"Reading that prepares a write and broad research also delegate",
		"Optional SDD",
		"explicit request or accepted proposal",
		"risk alone never forces SDD",
		"per-action delegation",
		"Direct and delegated work never create SDD artifacts",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderTriggerRules() missing %q\n%s", want, rendered)
		}
	}
}

func TestRenderTriggerRulesUsesOnlyProviderIssuedTransitions(t *testing.T) {
	t.Parallel()

	rendered := RenderTriggerRules()
	for _, want := range []string{
		"Working",
		"Checking",
		"Ready",
		"Needs your decision",
		"gentle-ai.work-status/v1",
		"gentle-ai.work-transition/v1",
		"zero-or-one `authorizedTransition`",
		"Never synthesize alternate flags",
		"do not fall back to prompt-owned authority",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderTriggerRules() missing %q\n%s", want, rendered)
		}
	}
}

func TestRenderTriggerRulesKeepsPreStartEscapeAndPostStartStopConsistent(
	t *testing.T,
) {
	t.Parallel()

	const legacyEscape = "legacy direct-inline, delegated-direct, and optional-SDD behavior"
	orchestrator := assets.MustRead("generic/sdd-orchestrator.md")
	if !strings.Contains(orchestrator, legacyEscape) ||
		!strings.Contains(
			orchestrator,
			"After start, never degrade a managed WorkRun to legacy behavior",
		) {
		t.Fatal("canonical normal intake contract lost its pre/post-START boundary")
	}

	rendered := RenderTriggerRules()
	for _, want := range []string{
		"After a managed WorkRun has started",
		"Do not retry or downgrade that WorkRun",
		"infer replacement authority",
		"Before `work-start`",
		legacyEscape,
		"without pretending that a managed WorkRun exists",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderTriggerRules() missing boundary %q\n%s", want, rendered)
		}
	}
	if strings.Contains(
		rendered,
		"If the capability is unavailable, disabled, unknown, or read-only",
	) {
		t.Fatal("trigger rules retained an unconditional pre-START stop")
	}
}

func TestRenderTriggerRulesRetiresPromptOwnedReviewCeremony(t *testing.T) {
	t.Parallel()

	rendered := RenderTriggerRules()
	for _, retired := range []string{
		"Review Lens Selection",
		"Risk table:",
		"exactly ONE dominant-risk lens",
		"four initial 4R lens sweeps",
		"review/start(target)",
		"loop until",
		"judgment-day",
	} {
		if strings.Contains(rendered, retired) {
			t.Errorf("RenderTriggerRules() retained prompt-owned ceremony %q\n%s", retired, rendered)
		}
	}
	if strings.Contains(rendered, "<!-- gentle-ai:") {
		t.Fatal("RenderTriggerRules() must remain marker-free")
	}
}

func TestRenderTriggerRulesIsDeterministicAndScannable(t *testing.T) {
	t.Parallel()

	first := RenderTriggerRules()
	second := RenderTriggerRules()
	if first != second {
		t.Fatal("RenderTriggerRules() is not deterministic")
	}
	if lines := strings.Count(first, "\n") + 1; lines > 32 {
		t.Fatalf("RenderTriggerRules() has %d lines, want at most 32\n%s", lines, first)
	}
}
