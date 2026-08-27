package assets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssueCreationAuthorityBoundary(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	duplicatePath := filepath.Join(repositoryRoot, "skills", "issue-creation", "SKILL.md")
	if _, err := os.Stat(duplicatePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			t.Fatalf("duplicate project issue-creation authority still exists at %s", duplicatePath)
		}
		t.Fatalf("stat duplicate project issue-creation authority: %v", err)
	}

	agents, err := os.ReadFile(filepath.Join(repositoryRoot, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	const canonicalRegistryRow = "| `issue-creation` | When creating a GitHub issue, reporting a bug, or requesting a feature. | [`internal/assets/skills/issue-creation/SKILL.md`](internal/assets/skills/issue-creation/SKILL.md) |"
	if !strings.Contains(string(agents), canonicalRegistryRow) {
		t.Fatalf("AGENTS.md must route the canonical issue-creation identity directly to the embedded authority; missing row %q", canonicalRegistryRow)
	}
	for _, stale := range []string{"gentle-ai-issue-creation", "[`skills/issue-creation/SKILL.md`](skills/issue-creation/SKILL.md)"} {
		if strings.Contains(string(agents), stale) {
			t.Fatalf("AGENTS.md still references stale issue-creation authority %q", stale)
		}
	}

	canonical := MustRead("skills/issue-creation/SKILL.md")
	if !strings.Contains(canonical, "\nname: issue-creation\n") {
		t.Fatal("embedded issue-creation authority must retain canonical frontmatter identity name: issue-creation")
	}

	collaborationPath := filepath.Join(repositoryRoot, "skills", "gentle-ai-collab-perfect", "SKILL.md")
	collaboration, err := os.ReadFile(collaborationPath)
	if err != nil {
		t.Fatalf("read collaboration skill: %v", err)
	}
	collaborationText := string(collaboration)
	for _, required := range []string{
		"internal/assets/skills/issue-creation/SKILL.md",
		"CONTRIBUTING.md",
		".github/ISSUE_TEMPLATE",
		"discovered GitHub labels",
	} {
		if !strings.Contains(collaborationText, required) {
			t.Fatalf("collaboration skill must reference canonical issue policy source %q", required)
		}
	}
	if strings.Contains(collaborationText, "gh issue create") {
		t.Fatal("collaboration skill must delegate issue publication to the canonical authority, not carry direct gh issue create mechanics")
	}
	for _, stale := range []string{
		"status:approved` from a maintainer",
		"| Add `status:approved` to an issue | ❌ | ✅ |",
	} {
		if strings.Contains(collaborationText, stale) {
			t.Fatalf("collaboration skill retains stale approval authority %q", stale)
		}
	}
	if !strings.Contains(collaborationText, "canonical issue-creation workflow contract") {
		t.Fatal("collaboration skill must delegate approval actions to the canonical issue-creation workflow contract")
	}

	branch, err := os.ReadFile(filepath.Join(repositoryRoot, "skills", "branch-pr", "SKILL.md"))
	if err != nil {
		t.Fatalf("read branch-pr skill: %v", err)
	}
	if strings.Contains(string(branch), "Wait for maintainer to add `status:approved` to the issue") {
		t.Fatal("branch-pr skill retains stale maintainer-only approval instruction")
	}
	if !strings.Contains(string(branch), "canonical issue-creation workflow contract") {
		t.Fatal("branch-pr skill must route approval actions to the canonical issue-creation workflow contract")
	}
}

func TestPRLabelMutationsUseCanonicalIssueCreationAuthority(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	for _, path := range []string{
		filepath.Join(repositoryRoot, "skills", "gentle-ai-collab-perfect", "SKILL.md"),
		filepath.Join(repositoryRoot, "skills", "branch-pr", "SKILL.md"),
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(content)
		for _, required := range []string{
			"canonical issue-creation workflow contract",
			"current direct human instruction binds the exact target/action",
			"target-host capability",
			"one bounded mutation and target-host readback",
			"`size:exception` additionally requires documented over-budget rationale",
		} {
			if !strings.Contains(text, required) {
				t.Errorf("%s must require canonical PR-label authority marker %q", path, required)
			}
		}
		for _, stale := range []string{
			"| Apply `type:*` label to a PR | ❌ | ✅ |",
			"| Apply `size:exception` label | ❌ | ✅ |",
			"maintainer-applied `size:exception`",
			"Ask a maintainer to add the correct label; remove extras",
			"gh pr edit",
		} {
			if strings.Contains(text, stale) {
				t.Errorf("%s retains stale or unguarded PR-label guidance %q", path, stale)
			}
		}
	}
}

func TestDelegatedWorkflowMutationContract(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	canonical := MustRead("skills/issue-creation/SKILL.md")
	const delegatedWorkflowReference = "skills/issue-creation/references/delegated-workflow-actions.md"
	for _, required := range []string{
		"Before ANY post-publication workflow mutation, read `references/delegated-workflow-actions.md` completely and follow it.",
		"It is normative, not optional background.",
	} {
		if !strings.Contains(canonical, required) {
			t.Errorf("issue-creation skill must mandate delegated workflow reference loading %q", required)
		}
	}
	reference := MustRead(delegatedWorkflowReference)
	readReference, err := Read(delegatedWorkflowReference)
	if err != nil {
		t.Fatalf("Read(%q): %v", delegatedWorkflowReference, err)
	}
	if readReference != reference {
		t.Fatalf("Read(%q) differs from MustRead", delegatedWorkflowReference)
	}
	for _, required := range []string{
		`gh issue view "$NUMBER" --repo "$TARGET" --json number,url,state,labels >"$PRE_READ_FILE"`,
		`gh pr view "$NUMBER" --repo "$TARGET" --json number,url,state,labels >"$PRE_READ_FILE"`,
		`gh issue view "$NUMBER" --repo "$TARGET" --json number,url,state,labels >"$POST_READ_FILE"`,
		`gh pr view "$NUMBER" --repo "$TARGET" --json number,url,state,labels >"$POST_READ_FILE"`,
		`gh issue edit "$NUMBER" --repo "$TARGET" --add-label "status:approved" --remove-label "$CONFLICTING_LABELS"`,
		`gh pr edit "$NUMBER" --repo "$TARGET" --add-label "status:approved" --remove-label "$CONFLICTING_LABELS"`,
		"status:needs-review",
		"status:needs-design",
		"status:needs-info",
		"preserving every unrelated pre-state label",
		"`confirmed` requires exact target identity, the requested state or label delta, and every unrelated pre-state label preserved",
		"`no_write` requires both an authoritative target-host rejection proving no mutation was accepted and successful target-host post-readback whose complete target state equals pre-read exactly: same number, URL, open/closed state, and entire label set",
		"`unknown` includes ambiguity, partial readback, identity mismatch, unavailable post-read, lost response, or any difference at all in requested or unrelated state/labels, including missing labels, and stops all further mutations and retries",
	} {
		if !strings.Contains(reference, required) {
			t.Errorf("delegated workflow reference is missing contract marker %q", required)
		}
	}
	if strings.Contains(reference, "Replace `status:needs-review` with `status:approved`") {
		t.Error("delegated workflow reference retains single-conflict approval replacement guidance")
	}

	contributing, err := os.ReadFile(filepath.Join(repositoryRoot, "CONTRIBUTING.md"))
	if err != nil {
		t.Fatalf("read CONTRIBUTING.md: %v", err)
	}
	workflow, err := os.ReadFile(filepath.Join(repositoryRoot, ".github", "workflows", "pr-check.yml"))
	if err != nil {
		t.Fatalf("read pr-check workflow: %v", err)
	}
	for path, content := range map[string]string{
		"CONTRIBUTING.md":                string(contributing),
		".github/workflows/pr-check.yml": string(workflow),
	} {
		for _, stale := range []string{
			"a maintainer will add the `status:approved` label",
			"has been approved by a maintainer",
			"Issues must be approved by a maintainer before work begins.",
			"Please comment on the issue and wait for it to be labelled status:approved.",
		} {
			if strings.Contains(content, stale) {
				t.Errorf("%s retains stale maintainer-only approval authority %q", path, stale)
			}
		}
		if !strings.Contains(content, "canonical issue-creation workflow contract") {
			t.Errorf("%s must route approval authority to the canonical issue-creation workflow contract", path)
		}
	}
	if !strings.Contains(string(workflow), "if (!labels.includes('status:approved')) {") {
		t.Error("pr-check workflow must retain its status:approved enforcement condition")
	}
}
