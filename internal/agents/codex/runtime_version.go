package codex

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

const (
	MinimumGPT56RuntimeVersion = "0.144.0"
	codexUpdateCommand         = "npm install -g --ignore-scripts @openai/codex@0.144.0"
)

var (
	codexOutputRE     = regexp.MustCompile(`^codex(?:-cli)?[ \t]+v?([^ \t\r\n]+)$`)
	semverRE          = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	numericRE         = regexp.MustCompile(`^[0-9]+$`)
	runVersionCommand = func() ([]byte, error) {
		return exec.Command("codex", "--version").CombinedOutput()
	}
)

type semanticVersion struct {
	core       [3]string
	prerelease []string
	raw        string
}

// ValidateGPT56Runtime verifies that the installed Codex can use GPT-5.6 profiles.
func ValidateGPT56Runtime() error {
	output, err := runVersionCommand()
	if err != nil {
		reason := fmt.Sprintf("codex --version failed: %v", err)
		if detail := strings.TrimSpace(string(output)); detail != "" {
			reason += ": " + detail
		}
		return runtimeRequirementError(reason)
	}
	installed, err := parseCodexVersion(string(output))
	if err != nil {
		return runtimeRequirementError(err.Error())
	}
	minimum, _ := parseSemanticVersion(MinimumGPT56RuntimeVersion)
	if compareSemanticVersion(installed, minimum) < 0 {
		return runtimeRequirementError("installed version is " + installed.raw)
	}
	return nil
}

func parseCodexVersion(output string) (semanticVersion, error) {
	trimmed := strings.TrimSpace(output)
	if match := codexOutputRE.FindStringSubmatch(trimmed); match != nil {
		if version, err := parseSemanticVersion(match[1]); err == nil {
			return version, nil
		}
	}
	return semanticVersion{}, fmt.Errorf("could not parse codex --version output %q", trimmed)
}

func parseSemanticVersion(raw string) (semanticVersion, error) {
	match := semverRE.FindStringSubmatch(raw)
	if match == nil {
		return semanticVersion{}, fmt.Errorf("invalid semantic version %q", raw)
	}
	version := semanticVersion{core: [3]string{match[1], match[2], match[3]}, raw: raw}
	if match[4] != "" {
		version.prerelease = strings.Split(match[4], ".")
		for _, id := range version.prerelease {
			if len(id) > 1 && id[0] == '0' && numericRE.MatchString(id) {
				return semanticVersion{}, fmt.Errorf("invalid semantic version %q", raw)
			}
		}
	}
	return version, nil
}

func compareSemanticVersion(a, b semanticVersion) int {
	for i := range a.core {
		if comparison := compareNumericIdentifier(a.core[i], b.core[i]); comparison != 0 {
			return comparison
		}
	}
	if len(a.prerelease) == 0 && len(b.prerelease) > 0 {
		return 1
	}
	if len(a.prerelease) > 0 && len(b.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		aID, bID := a.prerelease[i], b.prerelease[i]
		aNumeric, bNumeric := numericRE.MatchString(aID), numericRE.MatchString(bID)
		switch {
		case aNumeric && bNumeric:
			if comparison := compareNumericIdentifier(aID, bID); comparison != 0 {
				return comparison
			}
		case aNumeric:
			return -1
		case bNumeric:
			return 1
		case aID < bID:
			return -1
		case aID > bID:
			return 1
		}
	}
	if len(a.prerelease) < len(b.prerelease) {
		return -1
	}
	if len(a.prerelease) > len(b.prerelease) {
		return 1
	}
	return 0
}

func compareNumericIdentifier(a, b string) int {
	if len(a) < len(b) || len(a) == len(b) && a < b {
		return -1
	}
	if len(a) > len(b) || a > b {
		return 1
	}
	return 0
}

func runtimeRequirementError(reason string) error {
	return fmt.Errorf("Codex >=%s is required for GPT-5.6 profiles. Update Codex with: %s (%s)", MinimumGPT56RuntimeVersion, codexUpdateCommand, reason)
}

func SetRuntimeVersionProbeForTest(probe func() ([]byte, error)) func() {
	original := runVersionCommand
	runVersionCommand = probe
	return func() { runVersionCommand = original }
}

func SetRuntimeVersionCommandForTest(output string, err error) func() {
	return SetRuntimeVersionProbeForTest(func() ([]byte, error) { return []byte(output), err })
}
