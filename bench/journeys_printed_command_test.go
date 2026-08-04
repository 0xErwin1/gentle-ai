package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestPrintedCommandArguments covers the split that lets a journey run the
// command the product printed instead of one the benchmark assembled for it.
// The product quotes with POSIX single quotes and escapes an embedded quote as
// '\” (review_next_transition.go's reviewTransitionShellWord), so a value
// carrying spaces or newlines -- a recovery authorization is six LF-joined
// lines -- has to survive the round trip byte for byte.
func TestPrintedCommandArguments(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
		wantErr string
	}{
		{
			name:    "plain flags",
			command: "gentle-ai review finalize --lineage=review-1 --captured-results=true",
			want:    []string{"review", "finalize", "--lineage=review-1", "--captured-results=true"},
		},
		{
			name:    "single quoted value keeps its spaces",
			command: "gentle-ai review repair --reason='the store lost a leaf'",
			want:    []string{"review", "repair", "--reason=the store lost a leaf"},
		},
		{
			name:    "single quoted value keeps its newlines",
			command: "gentle-ai review recover '--maintainer-authorization=schema/v1\nactor=maintainer'",
			want:    []string{"review", "recover", "--maintainer-authorization=schema/v1\nactor=maintainer"},
		},
		{
			name:    "escaped quote survives",
			command: `gentle-ai review repair --reason='it'\''s broken'`,
			want:    []string{"review", "repair", "--reason=it's broken"},
		},
		{
			name:    "surrounding whitespace is not an argument",
			command: "  gentle-ai   review status  ",
			want:    []string{"review", "status"},
		},
		{
			name:    "an empty command names nothing to run",
			command: "",
			wantErr: "carried no command to run",
		},
		{
			name:    "whitespace only names nothing to run",
			command: "   ",
			wantErr: "carried no command to run",
		},
		{
			name:    "the product name alone is not runnable",
			command: "gentle-ai",
			wantErr: "names no arguments",
		},
		{
			name:    "another program is not the product",
			command: "git status",
			wantErr: `starts with "git"`,
		},
		{
			name:    "an unterminated quote is not runnable as printed",
			command: "gentle-ai review repair --reason='never closed",
			wantErr: "unterminated quote",
		},
		{
			name:    "a trailing escape is not runnable as printed",
			command: `gentle-ai review repair --reason=x\`,
			wantErr: "trailing escape",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := printedCommandArguments(testCase.command)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("printedCommandArguments(%q) error = %v, want one containing %q", testCase.command, err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("printedCommandArguments(%q) = %v", testCase.command, err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("printedCommandArguments(%q) = %#v, want %#v", testCase.command, got, testCase.want)
			}
		})
	}
}

// TestRunPrintedTransitionRefusesATransitionWithNothingToRun is the corpus-side
// half of the same defect. A journey that re-derives the verb from the
// operation name assembles the command the product owed the reader, and then
// reports success for a transition that handed the reader nothing. The refusal
// happens before the product is ever invoked, so a nil run is never touched.
func TestRunPrintedTransitionRefusesATransitionWithNothingToRun(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		operation string
		command   string
		wantErr   string
	}{
		{
			name: "collect is not an execute transition", kind: "collect",
			wantErr: `expected an execute transition, got "collect"`,
		},
		{
			name: "execute with no command", kind: "execute", operation: "review.recover",
			wantErr: "carried no command to run",
		},
		{
			name: "execute with a templated command", kind: "execute", operation: "review.validate",
			command: "gentle-ai review validate --gate <gate>",
			wantErr: "is not runnable as printed",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var envelope statusEnvelope
			envelope.NextTransition.Kind = testCase.kind
			envelope.NextTransition.Execute.Operation = testCase.operation
			envelope.NextTransition.Execute.Command = testCase.command
			_, err := runPrintedTransition(nil, envelope)
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("runPrintedTransition error = %v, want one containing %q", err, testCase.wantErr)
			}
		})
	}
}
