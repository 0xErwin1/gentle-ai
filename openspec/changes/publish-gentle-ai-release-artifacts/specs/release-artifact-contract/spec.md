# Release Artifact Contract Specification

## Purpose

Defines the provider-owned, versioned, self-describing contract for the platform-independent assets archive that Gentle AI publishes with every release. This is the synchronization artifact a consumer decodes instead of hand-transcribing provider internals. It specifies observable manifest content, decode order, and fail-closed behavior — not implementation types.

## Requirements

### Requirement: Independent Contract Namespace and Versioning

The release-artifact contract MUST live in its own namespace, versioned independently of the `review-integration` runtime-negotiation contract. A contract-major change MUST be an explicit, documented compatibility event.

#### Scenario: Major version recognized

- GIVEN a consumer that supports contract major `1`
- WHEN it decodes a manifest declaring major `1`
- THEN it proceeds to validate the manifest against the bundled schema

#### Scenario: Unsupported major fails closed

- GIVEN a consumer that supports only contract major `1`
- WHEN it decodes a manifest declaring an unsupported major (per the `unsupported-major` fixture)
- THEN it MUST reject the archive with an actionable error naming the unsupported major
- AND it MUST NOT infer or guess a layout to continue

### Requirement: Self-Describing Assets Archive

The assets archive MUST bundle its own manifest, the exact schema the manifest identifies, and every content entry the manifest describes.

#### Scenario: Complete decode without external lookup

- GIVEN a valid assets archive
- WHEN a consumer extracts it
- THEN `artifact-manifest.json`, the schema file it references, and every entry path it lists are present inside the archive
- AND no entry requires fetching a file outside the archive to validate

#### Scenario: Missing referenced schema rejected

- GIVEN an archive whose manifest references a schema file
- WHEN that schema file is absent from the archive
- THEN validation MUST fail closed with an actionable error

### Requirement: Mandatory Manifest Field Groups

The manifest MUST carry, at minimum: contract identity (id, major, minor); release identity (repository, tag, semantic version, immutable commit); layout identity (independent of release version); an ordered list of canonical entries; named semantic references to snapshot entries and applicable provider contracts; and digest relationships linking the archive checksum source to a non-recursive tree digest.

#### Scenario: All field groups present and valid

- GIVEN a manifest conforming to the schema
- WHEN a consumer validates it
- THEN every mandatory field group is present and internally consistent (e.g., referenced entry paths exist in the entries list)

#### Scenario: Missing mandatory field group rejected

- GIVEN a manifest missing the release identity or layout identity group
- WHEN a consumer validates it against the bundled schema
- THEN validation MUST fail closed with an actionable error naming the missing group

### Requirement: Canonical Entry Records

Each canonical entry MUST carry a confined relative path (no absolute paths, no `..`, no traversal, forward slashes only), an entry type restricted to regular files, a normalized non-executable mode, and a `sha256:` content digest over exact file bytes. Entries MUST be unique and listed in ascending raw-byte path order.

#### Scenario: Path traversal rejected

- GIVEN an entry with a path containing `..` or an absolute path
- WHEN a consumer validates the manifest
- THEN it MUST reject the archive before extraction proceeds further

#### Scenario: Disallowed entry type rejected

- GIVEN an entry declared as a symlink, hardlink, device, or socket
- WHEN a consumer validates the manifest
- THEN it MUST reject the archive

#### Scenario: Tampered content digest detected

- GIVEN an entry whose declared digest does not match the extracted file's actual sha256
- WHEN a consumer verifies content digests
- THEN it MUST reject the archive with an actionable mismatch error

### Requirement: Non-Self-Referential Digest Binding

The manifest's internal tree digest MUST be computed over the sorted canonical entries and MUST exclude the manifest file itself. The complete-archive digest MUST come only from the externally signed `checksums.txt` envelope, never from a field inside the manifest.

#### Scenario: Tree digest excludes manifest

- GIVEN a manifest declaring a tree digest
- WHEN a consumer recomputes that digest from the archive's non-manifest entries
- THEN the recomputed digest MUST match the declared value without including the manifest file's own bytes

#### Scenario: Archive authenticity from signed envelope

- GIVEN a live release archive
- WHEN a consumer establishes authenticity
- THEN it MUST do so via the signed `checksums.txt`/minisign envelope, not via any digest embedded in the manifest

### Requirement: Mandatory Feature Rejection Preserved

The contract's compatibility declaration MUST continue to state `unknown_mandatory: reject`. A consumer MUST reject any unknown mandatory feature rather than silently widening its supported set.

#### Scenario: Unknown mandatory feature rejected

- GIVEN a manifest whose compatibility block declares an unrecognized mandatory feature
- WHEN a consumer validates compatibility
- THEN it MUST reject the artifact with an actionable error and MUST NOT proceed as if the feature were supported
