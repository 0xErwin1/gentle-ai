package sdd

import (
	"fmt"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
)

// RenderTriggerRules preserves the managed trigger-rules installation marker
// while replacing its retired prompt-owned review router with a projection of
// native provider contracts. The model may coordinate work, but it may not
// select review lenses, invent lifecycle transitions, or turn SDD into the
// universal implementation container.
func RenderTriggerRules() string {
	routing := capabilitymanifest.CanonicalImplementationRouting()

	var output strings.Builder
	output.WriteString("## Agent Trigger Rules\n\n")
	output.WriteString("Route work for the requested outcome with the smallest useful topology. ")
	output.WriteString("Gentle AI owns verification, review, delivery policy, and lifecycle transitions; ")
	output.WriteString("never infer them from prose, file extensions, a successful exit code, or adapter capabilities.\n\n")

	output.WriteString("### Organic implementation routing\n\n")
	_, _ = fmt.Fprintf(
		&output,
		"- **Direct inline:** decide or verify from %d–%d files inline. Keep one mechanical, already-understood file change inline only when it needs no research and has no unresolved design decision.\n",
		routing.DirectInline.MinUnderstandingFiles,
		routing.DirectInline.MaxUnderstandingFiles,
	)
	_, _ = fmt.Fprintf(
		&output,
		"- **Delegated direct:** delegate narrow exploration when understanding needs %d+ files; delegate one writer for %d+ non-trivial files. Reading that prepares a write and broad research also delegate.\n",
		routing.DelegatedDirect.MappingMinUnderstandingFiles,
		routing.DelegatedDirect.WriterMinNonTrivialFiles,
	)
	output.WriteString("- **Optional SDD:** propose SDD only when substantial ambiguity or durable proposal/spec/design/tasks would materially reduce uncertainty. Select SDD only after explicit request or accepted proposal; risk alone never forces SDD.\n")
	output.WriteString("- These are implementation routes, not a ban on per-action delegation. Tests, builds, installs, and native review actors may still use fresh workers without changing the route or creating an SDD run.\n")
	output.WriteString("- Direct and delegated work never create SDD artifacts, prompts, phase attempts, or synthetic SDD runs.\n\n")

	output.WriteString("### Native progress and authority\n\n")
	output.WriteString("- Present only **Working**, **Checking**, **Ready**, or **Needs your decision**. Keep hashes, lenses, receipts, budgets, and recovery internals out of the normal interaction.\n")
	output.WriteString("- When a managed WorkRun exists, request exactly `gentle-ai work-status --cwd <repo> --work-run <id> --contract gentle-ai.work-status/v1 --json`.\n")
	output.WriteString("- Apply only the zero-or-one `authorizedTransition` returned by that status using `gentle-ai work-transition apply --cwd <repo> --work-run <id> --contract gentle-ai.work-transition/v1 --authorization-ref <ref> --expected-revision <revision> --json`.\n")
	output.WriteString("- Never synthesize alternate flags, choose a review lens, rebuild recovery policy, or retry a stale, expired, mismatched, or replayed authorization.\n")
	output.WriteString("- If the capability is unavailable, disabled, unknown, or read-only, report the typed stop and do not fall back to prompt-owned authority. Existing SDD v1 runs continue through their SDD-specific status contract.\n")

	return output.String()
}
