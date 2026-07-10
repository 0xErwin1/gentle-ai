package reviewtransaction

import (
	"fmt"
	"path"
	"strings"
)

const LargeChangeLines = 400

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type RiskSignal string

const (
	SignalAuth         RiskSignal = "auth"
	SignalUpdate       RiskSignal = "update"
	SignalSecurity     RiskSignal = "security"
	SignalPayments     RiskSignal = "payments"
	SignalDataExposure RiskSignal = "data_exposure"
	SignalDataLoss     RiskSignal = "data_loss"
	SignalPermissions  RiskSignal = "permissions"
	SignalShellProcess RiskSignal = "shell_process"
)

type DiffStat struct {
	Path      string
	Additions int
	Deletions int
	Binary    bool
	Generated bool
	ModeOnly  bool
}

type RiskInput struct {
	Stats                    []DiffStat
	Signals                  []RiskSignal
	OnlyNonExecutableChanges bool
	TouchesConfiguration     bool
}

// ClassifyRisk evaluates high, low, then medium. The first matching tier wins.
// Model, provider, profile, and effort are intentionally not classifier inputs.
func ClassifyRisk(input RiskInput) (RiskLevel, error) {
	changedLines, err := CountChangedLines(input.Stats)
	if err != nil {
		return "", err
	}
	for _, signal := range input.Signals {
		if !validRiskSignal(signal) {
			return "", fmt.Errorf("unknown risk signal %q", signal)
		}
	}

	if hasHighSignal(input.Signals) || touchesHotPath(input.Stats) || changedLines > LargeChangeLines {
		return RiskHigh, nil
	}
	if input.OnlyNonExecutableChanges && !input.TouchesConfiguration {
		return RiskLow, nil
	}
	return RiskMedium, nil
}

// CountChangedLines is the cross-adapter counting contract. Callers provide the
// canonical base-to-candidate union with one entry per repository-relative path.
// Text additions and deletions count once, including generated text and complete
// untracked/deleted text. Binary, mode-only, and unchanged rename entries count
// as zero. Generated files remain part of the snapshot and are never discounted.
func CountChangedLines(stats []DiffStat) (int, error) {
	total := 0
	seen := make(map[string]struct{}, len(stats))
	for _, stat := range stats {
		logicalPath, err := normalizeLogicalPath(stat.Path)
		if err != nil {
			return 0, err
		}
		if _, duplicate := seen[logicalPath]; duplicate {
			return 0, fmt.Errorf("duplicate diff stat path %q", logicalPath)
		}
		seen[logicalPath] = struct{}{}
		if stat.Additions < 0 || stat.Deletions < 0 {
			return 0, fmt.Errorf("negative diff stat for %q", logicalPath)
		}
		if stat.Binary || stat.ModeOnly {
			continue
		}
		total += stat.Additions + stat.Deletions
	}
	return total, nil
}

func hasHighSignal(signals []RiskSignal) bool {
	return len(signals) > 0
}

func validRiskSignal(signal RiskSignal) bool {
	switch signal {
	case SignalAuth, SignalUpdate, SignalSecurity, SignalPayments,
		SignalDataExposure, SignalDataLoss, SignalPermissions, SignalShellProcess:
		return true
	default:
		return false
	}
}

func touchesHotPath(stats []DiffStat) bool {
	for _, stat := range stats {
		lower := strings.ToLower(stat.Path)
		for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
			return r == '/' || r == '\\' || r == '.' || r == '-' || r == '_'
		}) {
			switch token {
			case "auth", "update", "security", "payments":
				return true
			}
		}
	}
	return false
}

func normalizeLogicalPath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("invalid logical path %q", value)
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != value {
		return "", fmt.Errorf("logical path is not canonical: %q", value)
	}
	return cleaned, nil
}
