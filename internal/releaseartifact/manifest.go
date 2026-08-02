package releaseartifact

import (
	"errors"
	"fmt"
)

// Contract identity, schema identity, and file name constants for the
// gentle-ai.release-artifact contract (design D1, Interfaces/Contracts).
const (
	ContractID       = "gentle-ai.release-artifact"
	ContractMajor    = 1
	ContractMinor    = 0
	ManifestSchema   = "gentle-ai.release-artifact-manifest/v1"
	ManifestSchemaID = "https://gentle-ai.dev/contracts/release-artifact/v1/schemas/artifact-manifest.schema.json"
	ManifestFileName = "artifact-manifest.json"
	SnapshotSchema   = "gentle-ai.release-semantic-capabilities/v1"
)

// Manifest is the release artifact contract's self-describing top-level
// document (design D1-D3, Worked Example). Field order matches the JSON
// declaration order used by EncodeCanonical.
type Manifest struct {
	Schema        string                `json:"schema"`
	Contract      ManifestContract      `json:"contract"`
	Release       ManifestRelease       `json:"release"`
	Layout        ManifestLayout        `json:"layout"`
	Archive       ManifestArchive       `json:"archive"`
	References    ManifestReferences    `json:"references"`
	Tree          Tree                  `json:"tree"`
	Compatibility ManifestCompatibility `json:"compatibility"`
	Entries       []Entry               `json:"entries"`
}

// ManifestContract carries contract identity (design D2: "self-describing
// decode without any external fetch").
type ManifestContract struct {
	ID         string `json:"id"`
	Major      int    `json:"major"`
	Minor      int    `json:"minor"`
	SchemaID   string `json:"schema_id"`
	SchemaPath string `json:"schema_path"`
}

// ManifestRelease carries release identity: repository, tag, semantic
// version, and immutable commit.
type ManifestRelease struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
}

// ManifestLayout carries layout identity, independent of release version.
type ManifestLayout struct {
	Version int `json:"version"`
}

// ManifestArchive names the published archive and where its digest comes
// from. DigestSource is always "signed-checksums.txt" — the manifest never
// carries an archive self-digest (design D2, spec "Archive authenticity
// from signed envelope").
type ManifestArchive struct {
	Asset        string `json:"asset"`
	DigestSource string `json:"digest_source"`
}

// ManifestReferences carries named semantic references to snapshot entries
// and applicable provider contracts.
type ManifestReferences struct {
	SemanticSnapshots []ManifestSemanticSnapshotRef `json:"semantic_snapshots"`
	Contracts         []ManifestContractRef         `json:"contracts"`
}

// ManifestSemanticSnapshotRef is one array element (design D2: array, not
// singular, to avoid a guaranteed future major bump).
type ManifestSemanticSnapshotRef struct {
	Contract string `json:"contract"`
	Path     string `json:"path"`
	Schema   string `json:"schema"`
}

// ManifestContractRef names one applicable provider contract root bundled
// in the archive.
type ManifestContractRef struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

// ManifestCompatibility carries unknown_mandatory: reject into the archive
// (design D2, spec "Mandatory Feature Rejection Preserved").
type ManifestCompatibility struct {
	MinimumContractMajor int    `json:"minimum_contract_major"`
	MaximumContractMajor int    `json:"maximum_contract_major"`
	AdditiveMinorPolicy  string `json:"additive_minor_policy"`
	UnknownMandatory     string `json:"unknown_mandatory"`
	UnknownOptional      string `json:"unknown_optional"`
}

// Validate enforces, in order: (1) contract major support — checked and
// reported before any other structural inference, so an unsupported major
// fails closed on the major alone (spec "Unsupported major fails closed");
// (2) presence of every mandatory field group; (3) per-entry admission and
// path uniqueness; (4) that every referenced entry path (contract schema
// path, semantic snapshot paths) resolves into the entries list; and
// (5) the non-self-referential tree digest.
func (m Manifest) Validate() error {
	if m.Contract.Major != ContractMajor {
		return fmt.Errorf("release artifact contract major %d is not supported; this consumer supports major %d", m.Contract.Major, ContractMajor)
	}
	if m.Contract.ID != ContractID {
		return fmt.Errorf("release artifact contract id %q is not supported; want %q", m.Contract.ID, ContractID)
	}

	if m.Release == (ManifestRelease{}) {
		return errors.New("release artifact manifest is missing the mandatory release identity field group")
	}
	if m.Release.Repository == "" || m.Release.Tag == "" || m.Release.Version == "" || m.Release.Commit == "" {
		return errors.New("release artifact manifest release identity field group is incomplete")
	}
	if m.Layout == (ManifestLayout{}) || m.Layout.Version < 1 {
		return errors.New("release artifact manifest is missing the mandatory layout identity field group")
	}
	if len(m.Entries) == 0 {
		return errors.New("release artifact manifest is missing the mandatory entries list")
	}
	if len(m.References.SemanticSnapshots) == 0 {
		return errors.New("release artifact manifest is missing the mandatory semantic snapshot references")
	}
	if len(m.References.Contracts) == 0 {
		return errors.New("release artifact manifest is missing the mandatory contract references")
	}
	if m.Compatibility.UnknownMandatory != "reject" {
		return fmt.Errorf("release artifact manifest compatibility.unknown_mandatory = %q, must be %q", m.Compatibility.UnknownMandatory, "reject")
	}

	if err := ValidateEntries(m.Entries); err != nil {
		return err
	}

	entryPaths := make(map[string]bool, len(m.Entries))
	for _, e := range m.Entries {
		entryPaths[e.Path] = true
	}
	if !entryPaths[m.Contract.SchemaPath] {
		return fmt.Errorf("release artifact manifest contract.schema_path %q does not resolve into entries", m.Contract.SchemaPath)
	}
	for _, ref := range m.References.SemanticSnapshots {
		if !entryPaths[ref.Path] {
			return fmt.Errorf("release artifact manifest semantic snapshot reference %q does not resolve into entries", ref.Path)
		}
	}

	if err := m.Tree.Validate(m.Entries); err != nil {
		return err
	}

	return nil
}
