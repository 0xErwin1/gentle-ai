# Semantic Capability Snapshot Specification

## Purpose

Defines the deterministic, platform-independent snapshot of Gentle AI's declared capability surface (contract, protocol, operations, gates, projections, schemas, features, bootstrap, compatibility) that ships inside the release assets archive, and the policy a consumer applies when comparing its supported set against it.

## Requirements

### Requirement: Deterministic, Generated Snapshot

The snapshot MUST be generated from the existing pure capability surface (no OS, runtime, build, or host calls) rather than hand-authored, using a canonical encoder (fixed field order, stable indentation, single trailing LF, no BOM).

#### Scenario: Snapshot generated from source, not authored

- GIVEN the provider's static capability surface function
- WHEN the release build runs the snapshot generator
- THEN the emitted snapshot bytes are produced by that generator, and a golden test fails if the generator's output diverges from the source surface without the golden being updated

### Requirement: Platform-Independent Byte Identity

The snapshot MUST be byte-identical regardless of which supported platform's binary produced it.

#### Scenario: Identical bytes across platforms

- GIVEN the same provider revision built on Linux and on macOS
- WHEN each build emits its semantic snapshot
- THEN the two snapshot files are byte-for-byte identical

### Requirement: Excluded Fields

The snapshot MUST exclude executable identity (e.g., binary hash), build/VCS identity (e.g., commit, build time, dirty state), and package release identity (release identity is carried only in the manifest's release block).

#### Scenario: No excluded field present

- GIVEN a generated snapshot
- WHEN a consumer inspects its fields
- THEN no executable hash, VCS/build metadata, or package version/channel field is present

### Requirement: Included Capability Surface

The snapshot MUST include contract identity, protocol version, operations, gates, projections, schemas, features (mandatory and optional), bootstrap, and compatibility policy.

#### Scenario: Required surface present

- GIVEN a generated snapshot
- WHEN a consumer decodes it
- THEN every listed surface element is present and matches the provider's live declared capability data for that release

### Requirement: Compatibility Policy Preserved and Non-Widening

The snapshot MUST carry the same mandatory-feature rejection policy the provider enforces at runtime (`unknown_mandatory: reject`). Adding entries to the snapshot MUST NOT silently widen what a consumer treats as supported.

#### Scenario: Unknown mandatory feature still rejected via snapshot

- GIVEN a snapshot whose features block lists an unrecognized mandatory feature
- WHEN a consumer applies the compatibility policy
- THEN it rejects the artifact rather than treating the new feature as supported

#### Scenario: Unknown optional feature ignored per policy

- GIVEN a snapshot whose features block lists an unrecognized optional feature
- WHEN a consumer applies the compatibility policy
- THEN it ignores the unrecognized optional feature and continues, per the provider's declared optional-feature policy

### Requirement: Required-Floor Compatibility for Operations, Schemas, Gates, and Projections

For operations, schemas, gates, and projections, the snapshot MUST declare a required floor that a consumer verifies as present; additions beyond the floor MUST be decodable without failure.

#### Scenario: New optional operation does not break older consumer logic

- GIVEN a snapshot that adds an operation beyond the previously required floor
- WHEN an existing consumer decodes it
- THEN it verifies the required floor is still present and does not fail merely because of the addition
