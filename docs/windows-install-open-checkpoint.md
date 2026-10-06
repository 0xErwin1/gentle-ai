# Windows install/open draft checkpoint

This is scoped lab evidence for the unfinished Windows Separate installer, not general Windows or release approval.

## Observed evidence
- Fresh run `4a890cf18dbd463397afb027b3a7aa6b`: existing installer TUI, installation exit 0, completed destination, fresh Gentle Shell UI and `/quit` exit 0.
- Compiled product source tree: `921353370c4e16201346e62b7364ab82b5e25e49`.
- Executable SHA-256: `0c328410602921a57cdc2933986506e8592b625fefbfd87eff635c66d99efaed` (25,843,200 bytes).
- Native run `1f710208d4894b32b8710c88d4b71487`: focused installer/CLI suites and Windows build exited 0; an unelevated symlink negative was skipped.
- Windows 11 Enterprise 26100 x64, unelevated credentialless lab account, physical Job memory/process/CPU limits and NTFS volume readback. Caller CWD, environment, PATH and fixture checks passed.

## Limits and follow-up
- The disposable guest's system-root ACL was hardened. Untouched/default Windows and independent stock-UI authenticity are not qualified.
- Full receipts remain `QualifiedCandidateGuest=false` and `FunctionalReady=false`; first opening is not a repeat-launch or native review closure proof.
- Windows pins version 4.0.0. Main/channel selection, Shared/existing Pi integration, Pi opening and bare-command PATH discovery are unfinished.
- The SDK added `lastChangelogVersion` after first opening. Strict whole-settings verification must be corrected and repeat launches tested without weakening package bindings.
- `e2e/windows-ui-acceptance-normal.ps1` is a guest payload, not a standalone host command. It requires the external lab controller, qualified Job helper and ConPTY helper; those dependencies are not delivered by this checkpoint. Do not execute it on the operator host.

## Rollback
Revert this checkpoint's product changes with their tests, control and evidence document. It introduces no personal PATH/configuration migration.
