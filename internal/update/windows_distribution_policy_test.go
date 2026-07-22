package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestOfficialReleaseOmitsUnsignedWindowsDistribution(t *testing.T) {
	config := readRepositoryFile(t, ".goreleaser.yaml")
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`(?mi)^\s*-\s*windows\s*$`),
		regexp.MustCompile(`(?mi)^\s*scoops\s*:`),
	} {
		if forbidden.MatchString(config) {
			t.Errorf("GoReleaser config still enables forbidden Windows distribution: %s", forbidden)
		}
	}
	for _, required := range []string{"- linux", "- darwin", "brews:", "artifacts: checksum"} {
		if !strings.Contains(config, required) {
			t.Errorf("GoReleaser config lost non-Windows release behavior %q", required)
		}
	}

	workflow := readRepositoryFile(t, ".github", "workflows", "release.yml")
	if regexp.MustCompile(`(?i)mock[^\n]*sign`).MatchString(workflow) {
		t.Fatal("release workflow contains mock signing")
	}
	ci := readRepositoryFile(t, ".github", "workflows", "ci.yml")
	for _, required := range []string{"windows-runtime:", "runs-on: windows-latest", "go build -trimpath", "go test ./..."} {
		if !strings.Contains(ci, required) {
			t.Errorf("Windows source-compatibility CI is missing %q", required)
		}
	}

	verify := readRepositoryFile(t, "scripts", "verify-release-assets.sh")
	if strings.Contains(strings.ToLower(verify), "_windows_") {
		t.Fatal("remote release verifier still expects Windows assets")
	}
	for _, required := range []string{"_linux_amd64.tar.gz", "_linux_arm64.tar.gz", "_darwin_amd64.tar.gz", "_darwin_arm64.tar.gz"} {
		if !strings.Contains(verify, required) {
			t.Errorf("remote release verifier lost %q", required)
		}
	}

	preflight := readRepositoryFile(t, "scripts", "release-preflight.sh")
	if !strings.Contains(preflight, "verify-release-distribution-policy.sh") {
		t.Fatal("release preflight does not enforce the Windows omission policy")
	}
}

func TestWindowsInstallAndUpgradeContainNoRemoteBinaryOrScriptPath(t *testing.T) {
	installer := readRepositoryFile(t, "scripts", "install.ps1")
	strategy := readRepositoryFile(t, "internal", "update", "upgrade", "strategy.go")
	instructions := readRepositoryFile(t, "internal", "update", "instructions.go")
	for name, content := range map[string]string{"scripts/install.ps1": installer, "strategy.go": strategy, "instructions.go": instructions} {
		for _, forbidden := range []string{"Install-ViaBinary", "_windows_", "scripts/install.ps1", "ExecutionPolicy", "checksumsUrl"} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s retains forbidden Windows distribution path %q", name, forbidden)
			}
		}
	}
	for _, required := range []string{
		"Windows binary distribution and Scoop are temporarily unavailable",
		"go install github.com/gentleman-programming/gentle-ai/cmd/gentle-ai@latest",
	} {
		if !strings.Contains(installer, required) {
			t.Errorf("Windows installer is missing safe source guidance %q", required)
		}
	}
}

func TestReleaseDistributionPolicyAssertionFailsClosed(t *testing.T) {
	script := filepath.Join("..", "..", "scripts", "verify-release-distribution-policy.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("distribution policy assertion is unavailable: %v", err)
	}

	valid := filepath.Join(t.TempDir(), "valid.yaml")
	if err := os.WriteFile(valid, []byte("builds:\n  - goos:\n      - linux\n      - darwin\nbrews:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("bash", script, valid).CombinedOutput(); err != nil {
		t.Fatalf("policy rejected non-Windows fixture: %v\n%s", err, output)
	}

	for _, tc := range []struct {
		name   string
		config string
	}{
		{name: "Windows target", config: "builds:\n  - goos: [linux, windows]\n"},
		{name: "Scoop publisher", config: "scoops:\n  - name: gentle-ai\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "goreleaser.yaml")
			if err := os.WriteFile(path, []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("bash", script, path).CombinedOutput(); err == nil {
				t.Fatalf("policy accepted forbidden config:\n%s", output)
			}
		})
	}

	mockWorkflow := filepath.Join(t.TempDir(), "release.yml")
	if err := os.WriteFile(mockWorkflow, []byte("steps:\n  - run: echo Mock signing Windows binary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("bash", script, valid, mockWorkflow).CombinedOutput(); err == nil {
		t.Fatalf("policy accepted mock signing workflow:\n%s", output)
	}
}

func TestWindowsDistributionRestorationGateIsDocumented(t *testing.T) {
	docs := readRepositoryFile(t, "README.md") + readRepositoryFile(t, "docs", "release-signing.md")
	for _, required := range []string{
		"publicly trusted RSA Authenticode",
		"Azure Artifact Signing",
		"amd64 and arm64",
		"before archive and checksum generation",
		"fails if either executable is unsigned",
	} {
		if !strings.Contains(docs, required) {
			t.Errorf("Windows distribution restoration gate is missing %q", required)
		}
	}
}
