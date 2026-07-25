package app

import (
	"fmt"
	"io"
)

func printHelp(w io.Writer, version string) {
	fmt.Fprintf(w, `gentle-ai — Gentle-AI: Ecosystem, Frameworks, Workflows (%s)

USAGE
  gentle-ai                     Launch interactive TUI
  gentle-ai <command> [flags]

COMMANDS
  install      Configure AI coding agents on this machine
  uninstall    Remove Gentle AI managed files from this machine
  sync         Sync agent configs and skills to current version
  skill-registry refresh
               Refresh .atl/skill-registry.md with cache-hit fast path
  sdd-status [change]
               Print native SDD phase status for orchestrators
  sdd-continue [change]
               Print native SDD dispatcher routing output
  sdd-attempt <status|begin|finish|reset> --cwd <repo> --change <change>
               Read or mutate the artifact-store-agnostic runtime-attempt ledger
  sdd-verify-validate --input <path|-> --requirements <n> --scenarios <n>
               Validate exact verification-report bytes without persistence
  work-capabilities --cwd <repo> --agent <agent-id> --contract gentle-ai.work-capabilities/v2 --json
               Negotiate the authenticated resumable work-routing capability
  work-start --cwd <repo> --agent <agent-id> --contract gentle-ai.work-start/v1 --json
               Start an outcome-only WorkRun from a JSON request on stdin
  work-route decide --cwd <repo> --work-run <id> --expected-revision <revision>
               --contract gentle-ai.work-route/v1 --choice <accept_sdd|decline_sdd> --json
               Submit only the human SDD choice; the owner selects any fallback route
  work-route bind-sdd --cwd <repo> --work-run <id> --expected-revision <revision>
               --contract gentle-ai.work-route/v1 --run-ref <existing-run> --json
               Bind an already-existing SDD runtime to its accepted owner route
  work-advance --cwd <repo> --work-run <id> --expected-revision <revision> --contract gentle-ai.work-advance/v2 --json
               Run one bounded owner attempt; it may return one no-launch verification prompt
  work-verification-decide --cwd <repo> --work-run <id> --prompt-ref <ref>
               --contract gentle-ai.work-verification-decide/v1
               --choice <run|defer|reduce_scope|deferred_runner> --json
               Record one offered choice; the receipt never contains or triggers an advance
               Only run permits one later work-advance/v2; every other choice stops
  work-reconcile --cwd <repo> --work-run <id> --expected-revision <revision>
               --diagnostic-ref <ref> --contract gentle-ai.work-reconcile/v1 --json
               Explicitly reconcile one owner diagnostic; never runs automatically
  work-status --cwd <repo> --work-run <id> --contract gentle-ai.work-status/v1 --json
               Read the route-neutral common-work status
  work-transition apply --cwd <repo> --work-run <id> --contract gentle-ai.work-transition/v1
               --authorization-ref <ref> --expected-revision <revision> --json
               Apply only the exact owner-issued common-work transition
  review start [--cwd <repo>] [--base-ref <ref>] [--focus <risk|resilience|readability|reliability>]
  review finalize [--cwd <repo>] [--result <review.json> ...] [--evidence <path>]
  review validate --gate <gate> [--cwd <repo>]
               Normal review path; ordinary authority is compact state plus receipt
  review status [--cwd <repo>]
               Read-only inventory of compact-v2 and shipped legacy-v1 authority
  review repair --preflight [--cwd <repo>]
               Classify the complete authority inventory before provider-owned repair

WORK CONTRACT COMPATIBILITY
  work-capabilities --cwd <repo> --contract gentle-ai.work-capabilities/v1 --json
               Read the frozen dormant six-contract compatibility envelope
  work-advance --cwd <repo> --work-run <id> --expected-revision <revision> --contract gentle-ai.work-advance/v1 --json
               Preserve the frozen terminal advance contract; it cannot return a consent prompt

COMPATIBILITY COMMANDS
  review-start --cwd <repo> --lineage <id> --policy-file <path>
               Read-only legacy v1 surface; rejects new v1 authority and directs users to 'review start'
  review-step --cwd <repo> --lineage <id> --operation <operation> --input <json>
               Read-only legacy v1 surface; rejects mutation and directs users to 'review finalize'
  review-resume --cwd <repo> --lineage <id>
               Read shipped v1 authority without mutation
  review-bundle-export --cwd <repo> --lineage <id> --out <path>
               Export compact current-state transport or a legacy v1 chain transport
  review-bundle-import --cwd <repo> --bundle <path> [--receipt <path> --request <path>]
               Import compact transport; receipt/request extras apply only to legacy v1 transport
  review-validate --cwd <repo> --receipt <path> (--request <path> | --lineage <id> --gate <gate>)
               Validate legacy v1 authority; native mode needs lineage/gate and derives authority
               Bundle, policy, ledger, fix-delta, evidence, CI, and release flags are optional compatibility or exceptional inputs
  update       Check for available updates
  upgrade      Apply updates to managed tools
  restore      Restore a config backup
  doctor       Run ecosystem health diagnostics
  version      Print version

FLAGS
  --help, -h    Show global help; every review subcommand also supports help

Run 'gentle-ai help' for this message.
Documentation: https://github.com/Gentleman-Programming/gentle-ai
`, version)
}
