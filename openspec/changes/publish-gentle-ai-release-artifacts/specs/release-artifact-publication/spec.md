# Release Artifact Publication Specification

## Purpose

Defines how the platform-independent assets archive is admitted into Gentle AI's release shape, covered by the existing signed checksum envelope, without weakening the existing four-platform binary matrix, per-platform-archive binary requirement, or the absence of Windows/Scoop publication. Also defines the distinct, non-authoritative status of a local unsigned snapshot build.

## Requirements

### Requirement: Exact-Plus-One Release Shape

A published release MUST contain exactly the four required platform archives (each with its platform binary) plus exactly one named platform-independent assets archive — no more, no fewer, and no substitution of one for the other.

#### Scenario: Valid release shape admitted

- GIVEN a release build with four platform archives and one assets archive
- WHEN the release-shape policy validates the artifact set
- THEN validation passes

#### Scenario: Extra sixth archive rejected

- GIVEN a release build with four platform archives, one assets archive, and one additional unexpected archive
- WHEN the release-shape policy validates the artifact set
- THEN validation MUST fail, naming the unexpected archive

#### Scenario: Metadata archive with a platform axis rejected

- GIVEN a build where the assets archive is produced per-platform (i.e., carries a GOOS/GOARCH axis) instead of as one platform-independent archive
- WHEN the release-shape policy validates the artifact set
- THEN validation MUST fail, because the assets archive MUST be exactly one archive, not one per platform

#### Scenario: Platform archive missing its binary rejected

- GIVEN a platform archive that does not contain the expected platform binary
- WHEN the release-shape policy validates the artifact set
- THEN validation MUST fail, and the four-platform binary requirement is not weakened to permit this

### Requirement: Complete Signed Checksum Coverage

The assets archive MUST be covered by the same signed `checksums.txt`/minisign envelope that covers the platform archives, binding it to the same repository and tag.

#### Scenario: Assets archive entry present and signed

- GIVEN a published release
- WHEN a consumer inspects the signed `checksums.txt`
- THEN it contains exactly one entry for the assets archive, verifiable under the same minisign signature as the platform archive entries

#### Scenario: Missing checksum entry fails remote verification

- GIVEN a release where the assets archive was uploaded but its checksum entry is absent
- WHEN the remote verification script runs against the live release
- THEN it MUST fail, naming the missing entry

### Requirement: Homebrew Formula Unaffected

Adding the assets archive MUST NOT change which archive(s) the Homebrew formula packages or how it resolves platform binaries.

#### Scenario: Formula output unchanged

- GIVEN a snapshot build that includes the new assets archive
- WHEN the Homebrew formula is generated
- THEN the formula content is unchanged versus a build without the assets archive

### Requirement: Local Unsigned Snapshot Is Non-Authoritative

A local unsigned snapshot build MUST produce the same archive layout as a live release, to unblock consumer bootstrap development, but MUST NOT be usable to satisfy release provenance, final acceptance, or the immutable release pin.

#### Scenario: Snapshot layout matches live layout

- GIVEN a local snapshot build and a live release build from the same revision
- WHEN their assets archives are compared for internal layout (manifest, schema, entries)
- THEN the layout is structurally equivalent

#### Scenario: Snapshot evidence cannot satisfy final acceptance

- GIVEN a consumer process that only has a local unsigned snapshot archive
- WHEN it attempts to record final release-provenance evidence
- THEN it MUST be blocked from doing so and MUST label all resulting evidence as development/bootstrap only

### Requirement: Remote Verification Admits and Enforces the New Asset

The remote release-verification script MUST admit the assets archive as an expected asset and fail if it is absent from a live release.

#### Scenario: Verification passes with the new asset present

- GIVEN a live release containing all five expected archives plus checksums and signature
- WHEN the verification script runs
- THEN it passes

#### Scenario: Verification fails when the new asset is missing

- GIVEN a live release missing the assets archive
- WHEN the verification script runs
- THEN it fails, naming the missing asset
