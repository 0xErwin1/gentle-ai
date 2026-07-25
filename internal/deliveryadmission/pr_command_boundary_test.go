package deliveryadmission

import (
	"errors"
	"testing"
)

// TestPRHeadIsOwnedByStructuredBindingsNotCommandStrings proves the "PR
// commands" threat-matrix row at the only surface PAD exposes: the PR head and
// its destination are typed bindings, never a command line. PAD itself never
// spawns a process, so the fail-closed obligation here is that no
// environment-prefix form and no composed-shell form can ever become bound
// authority. A ref that survives validation is later replayed verbatim into
// delivery machinery, so accepting `refs/heads/main;id` or a leading `-` would
// hand an attacker either a shell fragment or an argument-injection option.
func TestPRHeadIsOwnedByStructuredBindingsNotCommandStrings(t *testing.T) {
	t.Parallel()

	digest := testDigest("a")
	revision := testDigest("b")

	tests := []struct {
		name   string
		ref    string
		accept bool
	}{
		{name: "structured branch head", ref: "refs/heads/feature/organic-recovery", accept: true},
		{name: "structured default head", ref: "refs/heads/main", accept: true},

		// Environment-prefix forms: the head is smuggled behind an assignment
		// or an `env` invocation that only a shell would strip.
		{name: "env command prefix", ref: "env GIT_SSH_COMMAND=id refs/heads/main"},
		{name: "assignment prefix", ref: "GIT_DIR=/tmp/evil refs/heads/main"},
		{name: "unspaced assignment prefix", ref: "GIT_DIR=/tmp/evil;refs/heads/main"},

		// Composed-shell forms: separators, pipes, substitutions, and
		// redirections that only mean anything to a shell.
		{name: "semicolon composition", ref: "refs/heads/main;id"},
		{name: "and composition", ref: "refs/heads/main&&id"},
		{name: "background composition", ref: "refs/heads/main&id"},
		{name: "pipe composition", ref: "refs/heads/main|id"},
		{name: "dollar substitution", ref: "refs/heads/$(id)"},
		{name: "backtick substitution", ref: "refs/heads/`id`"},
		{name: "brace expansion", ref: "refs/heads/{a,b}"},
		{name: "output redirection", ref: "refs/heads/main>/tmp/owned"},
		{name: "input redirection", ref: "refs/heads/main</tmp/owned"},
		{name: "glob expansion", ref: "refs/heads/*"},
		{name: "newline composition", ref: "refs/heads/main\nid"},
		{name: "quote escape", ref: "refs/heads/main'"},

		// Argument-injection forms: a value that a structured argument list
		// would still hand to the tool as an option.
		{name: "long option", ref: "--upload-pack=id"},
		{name: "short option", ref: "-o"},

		// Git ref-format forms that fail closed for the same reason.
		{name: "parent traversal", ref: "refs/heads/../../etc"},
		{name: "reflog selector", ref: "refs/heads/main@{1}"},
		{name: "control character", ref: "refs/heads/main\x00id"},
		{name: "leading whitespace", ref: " refs/heads/main"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			destination := DestinationBinding{
				RepositoryRef: "github:gentle-ai", TargetRef: tt.ref,
				ObservedRevision: revision, DefaultBranch: true,
			}
			destinationErr := destination.validate()
			if tt.accept && destinationErr != nil {
				t.Fatalf("DestinationBinding.validate(%q) error = %v, want accepted", tt.ref, destinationErr)
			}
			if !tt.accept && !errors.Is(destinationErr, ErrInvalid) {
				t.Fatalf("DestinationBinding.validate(%q) error = %v, want ErrInvalid", tt.ref, destinationErr)
			}

			candidate := CandidateBinding{Ref: tt.ref, Digest: digest}
			candidateErr := candidate.validate()
			if tt.accept && candidateErr != nil {
				t.Fatalf("CandidateBinding.validate(%q) error = %v, want accepted", tt.ref, candidateErr)
			}
			if !tt.accept && !errors.Is(candidateErr, ErrInvalid) {
				t.Fatalf("CandidateBinding.validate(%q) error = %v, want ErrInvalid", tt.ref, candidateErr)
			}
		})
	}
}

// TestCandidateRefKeepsOpaqueOwnerLabels proves the hardening above stays a
// shell/argument boundary rather than a ref-name policy: PAD candidates are
// owner-authored opaque labels, so the scheme-prefixed shapes already used
// across the control plane must keep validating.
func TestCandidateRefKeepsOpaqueOwnerLabels(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"candidate:normalized/1", "candidate:owner-verified", "work-run:1794",
		"maintainer:one", "github:issue/1794", "bare:organic-runtime-e2e",
		"candidate:human-readable-label", "refs/heads/main",
	} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			if err := (CandidateBinding{Ref: ref, Digest: testDigest("a")}).validate(); err != nil {
				t.Fatalf("CandidateBinding.validate(%q) error = %v, want accepted", ref, err)
			}
		})
	}
}
