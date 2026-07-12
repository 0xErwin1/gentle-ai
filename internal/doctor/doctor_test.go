package doctor

import (
	"context"
	"reflect"
	"testing"
)

func TestToolCheckID(t *testing.T) {
	tests := []struct {
		tool string
		want CheckID
	}{
		{tool: "gentle-ai", want: "tool:gentle-ai"},
		{tool: "engram", want: "tool:engram"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			if got := ToolCheckID(tt.tool); got != tt.want {
				t.Fatalf("ToolCheckID(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestRunnerPreservesDeclarationOrderAndStableIDs(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   []Result
	}{
		{
			name: "empty",
			want: []Result{},
		},
		{
			name: "ordered checks override returned IDs",
			checks: []Check{
				{ID: CheckStateJSON, Run: resultCheck(Result{ID: "unstable", Status: StatusPass, Evidence: "state ok"})},
				{ID: CheckDiskSpace, Run: resultCheck(Result{Status: StatusWarn, Evidence: "disk low", Remedy: &Remedy{ID: RemedyFreeDiskSpace, Description: "free space"}})},
			},
			want: []Result{
				{ID: CheckStateJSON, Status: StatusPass, Evidence: "state ok"},
				{ID: CheckDiskSpace, Status: StatusWarn, Evidence: "disk low", Remedy: &Remedy{ID: RemedyFreeDiskSpace, Description: "free space"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (Runner{Checks: tt.checks}).Run(context.Background())
			if !reflect.DeepEqual(got.Checks, tt.want) {
				t.Fatalf("checks = %#v, want %#v", got.Checks, tt.want)
			}
		})
	}
}

func resultCheck(result Result) func(context.Context) Result {
	return func(context.Context) Result { return result }
}
