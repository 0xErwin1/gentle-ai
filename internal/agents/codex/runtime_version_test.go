package codex

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCompareSemanticVersionPrerelease(t *testing.T) {
	tests := []struct {
		name, older, newer string
	}{
		{name: "lexical identifiers", older: "0.145.0-alpha", newer: "0.145.0-beta"},
		{name: "numeric identifiers", older: "0.145.0-beta.2", newer: "0.145.0-beta.10"},
		{name: "numeric before nonnumeric", older: "0.145.0-1", newer: "0.145.0-alpha"},
		{name: "shorter identifier list", older: "0.145.0-beta", newer: "0.145.0-beta.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			older, err := parseSemanticVersion(tt.older)
			if err != nil {
				t.Fatalf("parseSemanticVersion(%q): %v", tt.older, err)
			}
			newer, err := parseSemanticVersion(tt.newer)
			if err != nil {
				t.Fatalf("parseSemanticVersion(%q): %v", tt.newer, err)
			}
			if got := compareSemanticVersion(older, newer); got >= 0 {
				t.Fatalf("compareSemanticVersion(%q, %q) = %d, want < 0", tt.older, tt.newer, got)
			}
		})
	}
}

func TestValidateGPT56Runtime(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		cmdErr  error
		wantErr bool
	}{
		{name: "missing command", cmdErr: exec.ErrNotFound, wantErr: true},
		{name: "malformed version", output: "codex-cli development", wantErr: true},
		{name: "false match on trailing line", output: "codex-cli development\nhelper 9.9.9", wantErr: true},
		{name: "trailing token", output: "codex-cli 0.145.0 helper", wantErr: true},
		{name: "unsupported command name", output: "helper 0.145.0", wantErr: true},
		{name: "bare version", output: "0.145.0", wantErr: true},
		{name: "older 0.143 patch", output: "codex-cli 0.143.7", wantErr: true},
		{name: "exact minimum", output: "codex-cli 0.144.0"},
		{name: "codex command shape", output: "codex 0.145.2"},
		{name: "v prefix", output: "codex-cli v0.145.2"},
		{name: "build metadata", output: "codex-cli 0.145.0+linux.x86-64"},
		{name: "large numeric core", output: "codex-cli 999999999999999999999.0.0"},
		{name: "prerelease and build metadata", output: "codex v0.145.0-rc.1+linux.1"},
		{name: "invalid empty prerelease identifiers", output: "codex-cli 0.145.0-..", wantErr: true},
		{name: "invalid empty build identifiers", output: "codex-cli 0.145.0+..", wantErr: true},
		{name: "leading zero major", output: "codex-cli 00.145.0", wantErr: true},
		{name: "leading zero minor", output: "codex-cli 0.0145.0", wantErr: true},
		{name: "leading zero patch", output: "codex-cli 0.145.00", wantErr: true},
		{name: "leading zero numeric prerelease", output: "codex-cli 0.145.0-rc.01", wantErr: true},
		{name: "prerelease at minimum is older", output: "codex-cli 0.144.0-beta.1", wantErr: true},
		{name: "prerelease above minimum is newer", output: "codex-cli 0.145.0-beta.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := SetRuntimeVersionCommandForTest(tt.output, tt.cmdErr)
			t.Cleanup(restore)

			err := ValidateGPT56Runtime()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateGPT56Runtime() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, want := range []string{"Codex >=0.144.0", "npm install -g --ignore-scripts @openai/codex@0.144.0"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q missing %q", err, want)
					}
				}
			}
		})
	}
}
