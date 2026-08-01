# Downstream Release Notification Specification

## Purpose

Defines the ordering, gating, and failure semantics of notifying the downstream consumer repository after a Gentle AI release publishes and passes remote verification, without ever affecting the validity of the already-published, already-verified release.

## Requirements

### Requirement: Notification Runs Only After Publish AND Verification

The notification step MUST run only after the release has published successfully AND remote asset verification has passed. It MUST NOT run before either condition is satisfied.

#### Scenario: Notification runs after both preconditions succeed

- GIVEN a release that has published and whose remote verification job has succeeded
- WHEN the pipeline proceeds
- THEN the notification job executes

#### Scenario: Notification does not run when verification has not completed

- GIVEN a release that has published but whose verification job has not yet completed or has failed
- WHEN the pipeline evaluates the notification job's preconditions
- THEN the notification job MUST NOT execute

### Requirement: Credential-Gated, Inert-by-Default Activation

The notification step MUST be gated on the presence of a scoped cross-repository credential. In the absence of that credential, the step MUST remain inert (no dispatch attempt that could error the pipeline) rather than fail the workflow.

#### Scenario: Credential absent — inert

- GIVEN no cross-repository dispatch credential is configured
- WHEN the notification job runs
- THEN it completes without attempting a dispatch and does not fail the pipeline

#### Scenario: Credential present — dispatch attempted

- GIVEN a valid scoped cross-repository credential is configured
- WHEN the notification job runs
- THEN it attempts the cross-repository dispatch to the downstream repository

### Requirement: Visible and Retryable Failure

A notification failure MUST be visible (e.g., a failed job status) and MUST be retryable (re-running the job or replaying it for the same release tag) without requiring any change to the published release.

#### Scenario: Failed notification is visibly reported

- GIVEN a dispatch attempt that fails (e.g., network or downstream error)
- WHEN the job completes
- THEN the job status is reported as failed and the failure is visible to maintainers

#### Scenario: Failed notification can be retried

- GIVEN a previously failed notification job for a specific release tag
- WHEN a maintainer re-runs or replays the notification for that tag
- THEN the retry executes the dispatch attempt again without re-running publish or verification

### Requirement: Non-Invalidating Failure

A notification failure MUST NEVER invalidate, unpublish, or mark as unverified an already-published, already-verified release.

#### Scenario: Release remains valid after notification failure

- GIVEN a release that has published and passed verification
- WHEN the subsequent notification job fails
- THEN the release's published and verified status is unchanged
