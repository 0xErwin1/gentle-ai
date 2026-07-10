package sdd

import (
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/assets"
	"github.com/gentleman-programming/gentle-ai/internal/model"
)

const boundedReviewContractAsset = "skills/_shared/review-ledger-contract.md"

func boundedReviewContract() string {
	return strings.TrimSpace(assets.MustRead(boundedReviewContractAsset))
}

func renderSDDOrchestratorAsset(agent model.AgentID) string {
	return renderBoundedReviewAsset(sddOrchestratorAsset(agent))
}

func renderBoundedReviewAsset(path string) string {
	content := assets.MustRead(path)
	if strings.HasSuffix(path, "/sdd-orchestrator.md") {
		return replaceBoundedReviewSection(content, "#### Review Execution Contract", "Cost and Context Balance")
	}
	if strings.Contains(content, "## Review ledger contract") {
		return replaceBoundedReviewSection(content, "## Review ledger contract", "")
	}
	return content
}

func replaceBoundedReviewSection(content, heading, nextHeading string) string {
	start := strings.Index(content, heading)
	if start < 0 {
		return content
	}
	end := len(content)
	if nextHeading != "" {
		remainder := content[start+len(heading):]
		for _, candidate := range []string{"\n#### " + nextHeading, "\n### " + nextHeading, "\n## " + nextHeading} {
			if relative := strings.Index(remainder, candidate); relative >= 0 {
				end = start + len(heading) + relative + 1
				break
			}
		}
	}
	replacement := heading + "\n\n" + boundedReviewContract() + "\n\n"
	return strings.TrimRight(content[:start], "\n") + "\n\n" + replacement + strings.TrimLeft(content[end:], "\n")
}
