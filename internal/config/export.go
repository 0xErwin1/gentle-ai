package config

import "encoding/json"

type ExportResult struct {
	Document    Document     `json:"document"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Lossless    bool         `json:"lossless"`
}

// Export returns the common desired intent and diagnoses any omitted provider data.
func Export(state DesiredState) ExportResult {
	result := ExportResult{
		Document: Document{
			Version:   CurrentVersion,
			Selection: state.Selection,
			Roles:     append([]Role(nil), state.Roles...),
		},
		Lossless: true,
	}

	for provider := range state.Extensions {
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
