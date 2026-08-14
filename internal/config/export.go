package config

import (
	"encoding/json"
	"sort"
)

type ExportResult struct {
	Document    Document     `json:"document"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Lossless    bool         `json:"lossless"`
}

func Export(state DesiredState) ExportResult {
	result := ExportResult{
		Document: Document{
			Version:   CurrentVersion,
			Selection: state.Selection,
			Roles:     append([]Role(nil), state.Roles...),
		},
		Lossless: true,
	}

	providers := make([]string, 0, len(state.Extensions))
	for provider := range state.Extensions {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	for _, provider := range providers {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Code:     "config.export.loss.provider-extension",
			Path:     "$.extensions." + provider,
			Severity: Error,
			Message:  "provider-specific extension has no common configuration representation",
		})
	}

	result.Lossless = len(result.Diagnostics) == 0
	return result
}

func EncodeExport(result ExportResult) ([]byte, error) {
	return json.MarshalIndent(result, "", "  ")
}
