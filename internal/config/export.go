package config

import (
	"encoding/json"
)

type ExportResult struct {
	Document    Document     `json:"document"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Lossless    bool         `json:"lossless"`
}

func Export(state DesiredState) ExportResult {
	return ExportResult{
		Document: Document{
			Version:   CurrentVersion,
			Selection: state.Selection,
		},
		Lossless: true,
	}
}

func EncodeExport(result ExportResult) ([]byte, error) {
	return json.MarshalIndent(result, "", "  ")
}
