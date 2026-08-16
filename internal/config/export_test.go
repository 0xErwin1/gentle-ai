package config

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestExportReportsProviderExtensionsAsLoss(t *testing.T) {
	result := Export(DesiredState{
		Version:    CurrentVersion,
		Selection:  Selection{Agents: []model.AgentID{model.AgentOpenCode}},
		Extensions: map[string]json.RawMessage{"opencode": json.RawMessage(`{"model":"provider-only"}`)},
	})

	if result.Lossless {
		t.Fatal("Export() reported provider extension as lossless")
	}
	if got := diagnosticCodes(result.Diagnostics); !slices.Equal(got, []string{"config.export.loss.provider-extension"}) {
		t.Fatalf("diagnostics = %v", got)
	}
	if len(result.Document.Extensions) != 0 {
		t.Fatalf("extensions = %v, want provider-only extension omitted", result.Document.Extensions)
	}
}

func TestExportPreservesRepresentableState(t *testing.T) {
	state := DesiredState{
		Version:   CurrentVersion,
		Selection: Selection{Agents: []model.AgentID{model.AgentOpenCode}},
		Roles:     []Role{{ID: "writer"}},
	}

	result := Export(state)

	if !result.Lossless {
		t.Fatalf("Export() lossless = false, diagnostics = %v", result.Diagnostics)
	}
	if result.Document.Version != CurrentVersion || len(result.Document.Roles) != 1 {
		t.Fatalf("document = %#v, want representable state", result.Document)
	}
}

func TestExportLegacyProviderExtensionsAreSorted(t *testing.T) {
	state := DesiredState{
		Version: CurrentVersion,
		Extensions: map[string]json.RawMessage{
			"zeta":  json.RawMessage(`{"enabled":true}`),
			"alpha": json.RawMessage(`{"enabled":false}`),
		},
	}

	first, err := EncodeExport(Export(state))
	if err != nil {
		t.Fatal(err)
	}
	for range 32 {
		current, err := EncodeExport(Export(state))
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != string(current) {
			t.Fatalf("repeated export bytes differ\nfirst=%s\ncurrent=%s", first, current)
		}
	}

	var result ExportResult
	if err := json.Unmarshal(first, &result); err != nil {
		t.Fatal(err)
	}
	if got, want := diagnosticCodes(result.Diagnostics), []string{
		"config.export.loss.provider-extension",
		"config.export.loss.provider-extension",
	}; !slices.Equal(got, want) {
		t.Fatalf("diagnostic codes = %v, want %v", got, want)
	}
	if got, want := []string{result.Diagnostics[0].Path, result.Diagnostics[1].Path}, []string{"$.extensions.alpha", "$.extensions.zeta"}; !slices.Equal(got, want) {
		t.Fatalf("diagnostic paths = %v, want %v", got, want)
	}
}
