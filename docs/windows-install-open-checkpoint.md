# Windows install/open draft checkpoint

This is scoped lab evidence for the unfinished Windows Separate installer, not general Windows or release approval.

## Observed evidence
- Fresh run `4a890cf18dbd463397afb027b3a7aa6b`: existing installer TUI, installation exit 0, completed destination, fresh Gentle Shell UI and `/quit` exit 0.
- Compiled product source tree: `921353370c4e16201346e62b7364ab82b5e25e49`.
- Executable SHA-256: `0c328410602921a57cdc2933986506e8592b625fefbfd87eff635c66d99efaed` (25,843,200 bytes).
- Native run `1f710208d4894b32b8710c88d4b71487`: focused installer/CLI suites and Windows build exited 0; an unelevated symlink negative was skipped.
- Windows 11 Enterprise 26100 x64, unelevated credentialless lab account, physical Job memory/process/CPU limits and NTFS volume readback. Caller CWD, environment, PATH and fixture checks passed.

## Settings compatibility and repeated entries
- RED run `cddf7111bab54b388e01f33956c735d2`: installer exit 0, 18/19 fixtures passed; only `sdk-changelog-version` failed its own acceptance assertion, not infrastructure.
- The correction preserves exact `packages` and `npmCommand`, permits only string-valued `lastChangelogVersion`, and rejects unknown keys. It does not authorize arbitrary SDK configuration mutations.
- Native build `013a1697633f40aaa87fabe5a245d877`: focused installer/CLI suites and build exited 0, with the same symlink-negative skip.
- Corrected source tree: `9eed2a6200fb2d41363996ca59c3ce2ce5d25915`; executable SHA-256: `4320710ab25ede3e9850acdf82a671452c8b93a9b3a8268e0a58f0c8b4a2c8a9` (25,843,712 bytes).
- Fresh UI run `4753f431f9544464b938d85ea483b92f`: 19/19 settings fixtures passed, then installed `gentle-shell.cmd`, `pi.cmd`, `gentle-shell.cmd`, `pi.cmd` each produced a fresh opening and `/quit` return with exit 0.
- This exercises the real launcher verification after normal SDK state mutation. Caller CWD/environment/PATH, fixture preservation, resource readbacks, capture and owned CMD exit passed.

## Limits and follow-up
- The disposable guests' system-root ACLs were modified. Untouched/default Windows and independent stock-UI authenticity are not qualified.
- Full receipts remain `QualifiedCandidateGuest=false` and `FunctionalReady=false`; these are scoped lab checks, not native review closure or release approval.
- Windows pins version 4.0.0. Main/channel selection, Shared/existing Pi integration and bare-command PATH discovery remain unfinished. Both wrapper labels currently launch the same owned CLI; distinct role behavior and configured-model operation are not qualified.
- `e2e/windows-ui-acceptance-normal.ps1` is a guest payload, not a standalone host command. It requires the external lab controller, qualified Job helper and ConPTY helper; those dependencies are not delivered by this checkpoint. Do not execute it on the operator host.

## Rollback
Revert this checkpoint's product changes with their tests, control and evidence document. It introduces no personal PATH/configuration migration.
