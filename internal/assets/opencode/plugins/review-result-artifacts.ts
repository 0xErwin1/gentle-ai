import type { Plugin } from "@opencode-ai/plugin"
import { spawn } from "node:child_process"

const REVIEW_AGENTS = new Set(["review-risk", "review-resilience", "review-readability", "review-reliability"])
const BINDING = /^GENTLE_AI_REVIEW_BINDING (\{[^\n]+\})(?:\n|$)/
const TASK_RESULT = /^<task id="[^"\r\n]+" state="completed">\n<task_result>\n([\s\S]*?)\n<\/task_result>\n<\/task>$/
const TASK_TAG = /<\/?task(?:\s|>)|<\/?task_result>/

// REVIEW_CONTEXT_BYTE_BUDGET bounds the immutable candidate evidence this
// plugin materializes into a reviewer task's `args.prompt` before the task
// ever launches. Measured empirically against the pinned OpenCode runtime
// (internal/versions.OpenCode): a plugin-mutated `task` prompt reached the
// subagent's own model call byte-for-byte, with zero truncation observed, at
// every tested size from 5 KB up to 16 MiB, including exact runs at 4 MiB and
// 8 MiB. This budget is set to exactly the native per-command
// MaxFrozenCandidateDiffBytes cap (internal/reviewtransaction/
// frozen_candidate_context.go) — the same ceiling every other immutable-diff
// read in this product already accepts — rather than an arbitrary fraction of
// it: a smaller budget refuses candidates whose *risk-tier-counted* changed
// lines are small but whose manifest includes large regenerated/golden files,
// which this plugin materializes in full for every manifest path exactly like
// the Claude Code transport does. 4 MiB sits comfortably inside the verified
// zero-truncation range, not at its edge, and is enforced by outright
// refusal: exceeding it fails the reviewer launch closed. It never
// truncates, because a truncated candidate view could fabricate a
// false-clean result.
const REVIEW_CONTEXT_BYTE_BUDGET = 4 * 1024 * 1024 // 4 MiB

// REQUIRED_ISOLATION_ENVIRONMENT closes a channel this plugin cannot touch by
// itself: OpenCode assembles every session's *system* prompt (not the `task`
// `args.prompt` this plugin controls) by concatenating the agent's base
// prompt with an environment block, every AGENTS.md/CLAUDE.md/CONTEXT.md
// found walking up from the worktree root, local `instructions` glob
// entries, and the live `<available_skills>` catalog -- unconditionally,
// regardless of the agent's `tools` map. A review-risk session holding no
// bash and no read tool still received this concatenated content in its
// system message in a real OpenCode 1.18.10 run: a marker planted in
// AGENTS.md after the candidate froze appeared verbatim above the injected
// evidence. These two OpenCode-native environment variables are the only
// verified way to close it, confirmed by the same measurement: with both
// set, no project-instruction or skill-catalog content reached the
// reviewer's system message; with either unset, it did.
//
// These two variables do not close every channel: an operator-configured
// `instructions` entry that is an http(s):// URL is fetched by OpenCode
// unconditionally and is NOT suppressed by OPENCODE_DISABLE_PROJECT_CONFIG
// (verified: a poisoned local HTTP endpoint referenced this way still
// reached the reviewer with both variables set). That gap is closed
// separately below (remoteInstructionsEntries), by reading the effective
// config through the plugin's own OpenCode client rather than by a sentence
// operators could fail to read.
const REQUIRED_ISOLATION_ENVIRONMENT = ["OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_DISABLE_EXTERNAL_SKILLS"] as const

function isolationFlagSet(value: string | undefined): boolean {
  return typeof value === "string" && /^(1|true)$/i.test(value.trim())
}

function missingIsolationEnvironment(): string[] {
  return REQUIRED_ISOLATION_ENVIRONMENT.filter((name) => !isolationFlagSet(process.env[name]))
}

// remoteInstructionsEntries closes the one channel REQUIRED_ISOLATION_ENVIRONMENT
// cannot: OpenCode fetches every http(s):// `instructions` config entry
// unconditionally, from any layer, regardless of OPENCODE_DISABLE_PROJECT_CONFIG.
// Verified against real OpenCode 1.18.10 via the plugin's own
// `client.config.get()` (the same endpoint the OpenCode TUI/CLI itself reads
// effective config from): a project-level opencode.json's `instructions`
// entries become invisible here exactly when OPENCODE_DISABLE_PROJECT_CONFIG
// also stops them from taking effect -- config visibility and config effect
// move together for that layer, so there is nothing to detect there because
// there is nothing to refuse. The global config's own `instructions` array,
// including http(s):// entries, remains visible here regardless of either
// variable, matching that it remains fetched regardless of either variable.
// No config layer was found that changes the reviewer's fetched instructions
// without also changing what this function observes.
function remoteInstructionsEntries(instructions: unknown): string[] {
  if (!Array.isArray(instructions)) return []
  return instructions.filter((entry): entry is string => typeof entry === "string" && /^https?:\/\//i.test(entry))
}

type ReviewBinding = {
  lineage: string
  target: string
  lens: string
  order: number
  revision?: string
  repository_context?: string
  subject_hash?: string
}

interface ReviewArtifactSubject {
  schema: string
  subject_hash: string
  lineage_id: string
  authority_revision: string
  target_identity: string
  base_tree: string
  candidate_tree: string
  changed_path_manifest_sha256: string
  lens: string
  selected_order: number
}

interface ChangedPathManifestEntry {
  path: string
  status: string
  old_mode: string
  new_mode: string
  deleted: boolean
  type_changed: boolean
  mode_only: boolean
  intended_untracked: boolean
}

interface ReviewCapturePreflight {
  schema: string
  capability: string
  lineage_id: string
  target_identity: string
  lens: string
  selected_order: number
  artifact_subject: ReviewArtifactSubject
  base_tree: string
  candidate_tree: string
  changed_path_manifest: ChangedPathManifestEntry[]
}

function parseBinding(prompt: unknown, lens: string): ReviewBinding {
  const match = BINDING.exec(typeof prompt === "string" ? prompt : "")
  if (!match) throw new Error("review task is missing GENTLE_AI_REVIEW_BINDING")

  let binding: unknown
  try {
    binding = JSON.parse(match[1])
  } catch {
    throw new Error("review task binding is malformed")
  }
  if (!binding || typeof binding !== "object" || Array.isArray(binding)) {
    throw new Error("review task binding must be an object")
  }
  const value = binding as Record<string, unknown>
  const fields = Object.keys(value).sort().join(",")
  const legacy = fields === "lens,lineage,order,target"
  const legacyBound = fields === "lens,lineage,order,subject_hash,target"
  const priorCurrent = fields === "lens,lineage,order,repository_context,revision,target"
  const current = fields === "lens,lineage,order,repository_context,revision,subject_hash,target"
  if ((!legacy && !legacyBound && !priorCurrent && !current) ||
      typeof value.lineage !== "string" || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value.lineage) ||
      typeof value.target !== "string" || !/^sha256:[a-f0-9]{64}$/.test(value.target) ||
      ((priorCurrent || current) && (typeof value.revision !== "string" || !/^sha256:[a-f0-9]{64}$/.test(value.revision) ||
        typeof value.repository_context !== "string" || !/^rctx1_[a-f0-9]{64}$/.test(value.repository_context))) ||
      ((legacyBound || current) && (typeof value.subject_hash !== "string" || !/^sha256:[a-f0-9]{64}$/.test(value.subject_hash))) ||
      value.lens !== lens || !Number.isSafeInteger(value.order) || (value.order as number) < 0) {
    throw new Error("review task binding does not match the selected lens")
  }
  return value as ReviewBinding
}

function reviewerResult(output: unknown): string {
  if (typeof output !== "string" || output.trim() === "") throw new Error("reviewer output must not be empty")
  const trimmed = output.trim()
  const envelope = TASK_RESULT.exec(trimmed)
  if (!envelope) {
    if (TASK_TAG.test(trimmed)) throw new Error("reviewer output contains a malformed task result envelope")
    return trimmed
  }
  if (envelope[1].trim() === "") {
    throw Object.assign(new Error("reviewer task result is empty"), { reviewClass: "empty_result" })
  }
  if (TASK_TAG.test(envelope[1])) {
    throw Object.assign(new Error("reviewer task result contains a nested task envelope"), { reviewClass: "nested_envelope" })
  }
  return envelope[1]
}

function extractionClass(cause: unknown): string | undefined {
  const value = (cause as { reviewClass?: unknown } | null)?.reviewClass
  return typeof value === "string" ? value : undefined
}

function captureCwd(worktree: string | undefined, directory: string): string {
  const override = process.env["GENTLE_AI_REVIEW_CWD"]
  if (typeof override === "string" && override.trim() !== "") return override.trim()
  return worktree || directory
}

function runNative(cwd: string, args: string[], stdin: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn("gentle-ai", args, { cwd, stdio: ["pipe", "pipe", "pipe"] })
    const stdout: Buffer[] = []
    const stderr: Buffer[] = []
    child.stdout.on("data", (chunk: Buffer) => stdout.push(chunk))
    child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk))
    child.stdin.on("error", reject)
    child.on("error", reject)
    child.on("close", (code) => {
      if (code === 0) {
        resolve(Buffer.concat(stdout).toString("utf8").trim())
        return
      }
      reject(new Error(`gentle-ai ${args[0]} ${args[1]} failed (${code ?? "signal"}): ${Buffer.concat(stderr).toString("utf8").trim()}`))
    })
    child.stdin.end(stdin)
  })
}

function repositoryBindingArgs(cwd: string, binding: ReviewBinding): string[] {
  if (binding.repository_context && binding.revision) {
    return ["--repository-context", binding.repository_context, "--expected-revision", binding.revision]
  }
  return ["--cwd", cwd]
}

function captureResult(cwd: string, binding: ReviewBinding, result: string): Promise<string> {
  const subjectArgs = binding.subject_hash ? ["--subject-hash", binding.subject_hash] : []
  return runNative(cwd, [
    "review", "capture-result", ...repositoryBindingArgs(cwd, binding),
    "--lineage", binding.lineage, "--target", binding.target,
    "--lens", binding.lens, "--order", String(binding.order), ...subjectArgs, "--input", "-",
  ], result)
}

async function preflightCapture(cwd: string, binding: ReviewBinding): Promise<ReviewCapturePreflight> {
  try {
    const subjectArgs = binding.subject_hash ? ["--subject-hash", binding.subject_hash] : []
    const response = await runNative(cwd, [
      "review", "capture-result", ...repositoryBindingArgs(cwd, binding),
      "--lineage", binding.lineage, "--target", binding.target,
      "--lens", binding.lens, "--order", String(binding.order), ...subjectArgs, "--preflight",
    ], "")
    let parsed: unknown
    try {
      parsed = JSON.parse(response)
    } catch {
      throw new Error("review capture preflight returned malformed artifact-subject JSON")
    }
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      throw new Error("review capture preflight returned malformed artifact-subject JSON")
    }
    const value = parsed as Record<string, unknown>
    const subject = value.artifact_subject as Record<string, unknown> | undefined
    const manifest = value.changed_path_manifest
    if (!subject || subject.schema !== "gentle-ai.review-artifact-subject/v2" ||
        typeof subject.subject_hash !== "string" || !/^sha256:[a-f0-9]{64}$/.test(subject.subject_hash) ||
        typeof subject.authority_revision !== "string" || !/^sha256:[a-f0-9]{64}$/.test(subject.authority_revision) ||
        typeof subject.base_tree !== "string" || !/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/.test(subject.base_tree) ||
        typeof subject.candidate_tree !== "string" || !/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/.test(subject.candidate_tree) ||
        typeof subject.changed_path_manifest_sha256 !== "string" || !/^sha256:[a-f0-9]{64}$/.test(subject.changed_path_manifest_sha256) ||
        subject.lineage_id !== binding.lineage || subject.target_identity !== binding.target ||
        (binding.revision !== undefined && subject.authority_revision !== binding.revision) ||
        subject.lens !== binding.lens || subject.selected_order !== binding.order ||
        value.schema !== "gentle-ai.review-capture-preflight/v1" || value.capability !== "review.native_capture_preflight" ||
        value.lineage_id !== binding.lineage || value.target_identity !== binding.target || value.lens !== binding.lens ||
        value.selected_order !== binding.order || value.base_tree !== subject.base_tree || value.candidate_tree !== subject.candidate_tree ||
        !validManifest(manifest)) {
      throw new Error("review capture preflight returned an incomplete artifact subject")
    }
    if (binding.subject_hash && subject.subject_hash !== binding.subject_hash) {
      throw new Error("review capture preflight returned a different artifact subject")
    }
    return value as unknown as ReviewCapturePreflight
  } catch (cause) {
    const scope = binding.repository_context ? "the provider-issued repository context" : cwd
    const recovery = gitTrustRefusal(binding, cause)
      ? GIT_TRUST_REFUSAL_RECOVERY
      : binding.repository_context
      ? `Refresh the exact native next_transition for lineage ${binding.lineage} before relaunching the lens.`
      : `If lineage ${binding.lineage} was started in a different repository (for example a nested one), ` +
        `set GENTLE_AI_REVIEW_CWD to that repository and relaunch the lens.`
    throw new Error(
      `review capture preflight failed for lens ${binding.lens} under ${scope}: ` +
      `${sessionErrorMessage(binding, cause, "repository_context_preflight_failed")}. ` +
      `The reviewer was not launched, so its exactly-once invocation is preserved. ` +
      recovery,
    )
  }
}

function validManifest(value: unknown): value is ChangedPathManifestEntry[] {
  if (!Array.isArray(value)) return false
  let previous = ""
  for (const entry of value) {
    if (!validManifestEntry(entry) ||
        (previous !== "" && Buffer.compare(Buffer.from(previous, "utf8"), Buffer.from(entry.path, "utf8")) >= 0)) return false
    previous = entry.path
  }
  return true
}

function validManifestEntry(entry: unknown): entry is ChangedPathManifestEntry {
  if (!entry || typeof entry !== "object" || Array.isArray(entry)) return false
  const value = entry as Record<string, unknown>
  return Object.keys(value).sort().join(",") ===
    "deleted,intended_untracked,mode_only,new_mode,old_mode,path,status,type_changed" &&
    typeof value.path === "string" && value.path !== "" &&
    typeof value.status === "string" && /^[ADMT]$/.test(value.status) &&
    typeof value.old_mode === "string" && /^[0-7]{6}$/.test(value.old_mode) &&
    typeof value.new_mode === "string" && /^[0-7]{6}$/.test(value.new_mode) &&
    typeof value.deleted === "boolean" && typeof value.type_changed === "boolean" &&
    typeof value.mode_only === "boolean" && typeof value.intended_untracked === "boolean"
}

// inspectionArgs builds one exact `review inspect-candidate` invocation from
// the injected binding. Global operations (name-status, numstat) take no
// path index; patch takes the zero-based canonical changed_path_manifest
// index, matching internal/cli/review_inspect_candidate.go exactly.
function inspectionArgs(binding: ReviewBinding, operation: string, pathIndex?: number): string[] {
  const args = [
    "review", "inspect-candidate",
    "--repository-context", binding.repository_context as string,
    "--expected-revision", binding.revision as string,
    "--lineage", binding.lineage, "--target", binding.target,
    "--lens", binding.lens, "--order", String(binding.order),
    "--operation", operation,
  ]
  if (pathIndex !== undefined) args.push("--path-index", String(pathIndex))
  return args
}

// materializeReviewEvidence runs every discovery and per-path patch command
// through the shell-less native channel (runNative), never through a tool
// the reviewer session itself could invoke. The reviewer holds no bash and
// no read tool: this provider-injected block is its only byte source. Any
// native failure or budget overflow throws before the reviewer ever
// launches; there is no partial-evidence or truncated-evidence outcome.
async function materializeReviewEvidence(cwd: string, binding: ReviewBinding, preflight: ReviewCapturePreflight): Promise<string> {
  if (!binding.repository_context || !binding.revision) {
    throw new Error(
      "immutable OpenCode candidate inspection requires a repository-context binding; " +
      "the reviewer was not launched, so its exactly-once invocation is preserved",
    )
  }
  try {
    let budget = REVIEW_CONTEXT_BYTE_BUDGET
    const sections: string[] = []
    const consume = (header: string, body: string, footer: string) => {
      const rendered = `${header}\n${body}\n${footer}\n`
      budget -= Buffer.byteLength(rendered, "utf8")
      if (budget < 0) {
        throw Object.assign(new Error("review context evidence budget exceeded"), { reviewBudgetExceeded: true })
      }
      sections.push(rendered)
    }
    consume(
      "GENTLE_AI_REVIEW_NAME_STATUS",
      await runNative(cwd, inspectionArgs(binding, "name-status"), ""),
      "GENTLE_AI_REVIEW_NAME_STATUS_END",
    )
    consume(
      "GENTLE_AI_REVIEW_NUMSTAT",
      await runNative(cwd, inspectionArgs(binding, "numstat"), ""),
      "GENTLE_AI_REVIEW_NUMSTAT_END",
    )
    for (let index = 0; index < preflight.changed_path_manifest.length; index++) {
      const entry = preflight.changed_path_manifest[index]
      const patch = await runNative(cwd, inspectionArgs(binding, "patch", index), "")
      // An empty patch for a path that is neither mode-only nor deleted is
      // never legitimate: even a newly added empty file still renders a
      // non-empty `diff --git`/`new file mode`/`index` header with no hunk.
      // A genuinely empty string here means the native read silently
      // produced nothing for a content-changing path -- the exact
      // fabricate-a-clean-review shape this plugin's own contract (no
      // partial-evidence outcome) forbids.
      if (patch === "" && !entry.mode_only && !entry.deleted) {
        throw Object.assign(
          new Error("review context evidence patch was empty for a content-changing path"),
          { reviewEmptyPatch: true, path: entry.path },
        )
      }
      consume(`GENTLE_AI_REVIEW_PATCH ${index} ${entry.path}`, patch, "GENTLE_AI_REVIEW_PATCH_END")
    }
    return sections.join("")
  } catch (cause) {
    if ((cause as { reviewBudgetExceeded?: boolean } | null)?.reviewBudgetExceeded) {
      throw new Error(
        `review context exceeds the ${REVIEW_CONTEXT_BYTE_BUDGET}-byte provider injection budget for lens ${binding.lens}; ` +
        "The reviewer was not launched, so its exactly-once invocation is preserved; " +
        "immutable candidate evidence is never truncated. " +
        "Split this candidate into smaller reviewable commits (a chained sequence, each under the budget) " +
        "and start a new review for the reduced scope; retrying the same candidate cannot succeed.",
      )
    }
    if ((cause as { reviewEmptyPatch?: boolean; path?: string } | null)?.reviewEmptyPatch) {
      const failedPath = (cause as { path?: string }).path
      throw new Error(
        `review context evidence patch was empty for content-changing path ${JSON.stringify(failedPath)} in lens ${binding.lens}; ` +
        "The reviewer was not launched, so its exactly-once invocation is preserved. " +
        "Refresh the exact native next_transition for lineage " + binding.lineage + " and relaunch the lens; " +
        "if the same path keeps returning an empty patch, treat it as a native inspection defect and stop relaunching.",
      )
    }
    throw new Error(
      `review context evidence materialization failed for lens ${binding.lens}: ` +
      `${sessionErrorMessage(binding, cause, "repository_context_evidence_failed")}. ` +
      "The reviewer was not launched, so its exactly-once invocation is preserved.",
    )
  }
}

async function injectReviewerContext(prompt: string, lens: string, cwd: string): Promise<string> {
  const binding = parseBinding(prompt, lens)
  const preflight = await preflightCapture(cwd, binding)
  const injectedBinding = { ...binding, subject_hash: preflight.artifact_subject.subject_hash }
  const evidence = await materializeReviewEvidence(cwd, injectedBinding, preflight)
  return `GENTLE_AI_REVIEW_BINDING ${JSON.stringify(injectedBinding)}\n` +
    `GENTLE_AI_REVIEW_CONTEXT ${JSON.stringify(preflight)}\n` +
    evidence +
    `GENTLE_AI_REVIEW_CONTEXT_END\n`
}

function preserveResult(cwd: string, binding: ReviewBinding, raw: string, cls?: string): Promise<string> {
  const args = [
    "review", "preserve-result", ...repositoryBindingArgs(cwd, binding),
    "--lineage", binding.lineage, "--target", binding.target,
    "--lens", binding.lens, "--order", String(binding.order), "--input", "-",
  ]
  if (typeof cls === "string" && cls !== "") args.push("--class", cls)
  return runNative(cwd, args, raw)
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}

// GIT_TRUST_REFUSAL_CODE is the typed, path-free code the native CLI emits
// when Git itself declines to open the bound repository because it is owned by
// a different account. It is the one native code an opaque binding surfaces,
// because the generic message ("refresh the exact native next_transition") is
// not merely vague for this cause: refreshing a transition cannot change the
// Git trust context of an already-running host process.
const GIT_TRUST_REFUSAL_CODE = "git_repository_untrusted"

// GIT_TRUST_REFUSAL_MESSAGE is authored here rather than forwarded from the
// native stderr, so the opaque path keeps its absolute rule that no native
// text ever reaches the session transcript. It mirrors the native wording in
// internal/cli/review_incident.go.
// It carries its own instruction, so every surface that renders it — including
// the post-launch capture path, which appends no separate recovery line —
// tells the caller something they can actually carry out.
const GIT_TRUST_REFUSAL_MESSAGE =
  `${GIT_TRUST_REFUSAL_CODE}: Git declined to open the bound repository in this process because it is owned by a ` +
  `different account; gentle-ai never provisions a safe.directory exception and never bypasses that protection. ` +
  `Restart the host process under a Git context that already trusts that repository.`

const GIT_TRUST_REFUSAL_RECOVERY =
  "Relaunch the lens once the host process runs under a Git context that trusts that repository."

function gitTrustRefusal(binding: ReviewBinding, cause: unknown): boolean {
  return Boolean(binding.repository_context) && new RegExp(`\\b${GIT_TRUST_REFUSAL_CODE}\\b`).test(errorMessage(cause))
}

// ADMISSION_REJECTION matches the typed decision the native CLI emits when it
// refused the reviewer RESULT itself (`reviewer artifact admission <decision>:`
// from internal/cli/review_artifact.go). Only the [a-z_]+ decision token is
// forwarded — never the native diagnostic, which can embed payload text — so
// the opaque path keeps its rule that no native prose reaches the transcript.
// Without this, an invalid result collapsed into "retry the same opaque
// binding", advice that deterministically fails: recapturing identical bytes
// can never satisfy admission, only a relaunched reviewer can.
const ADMISSION_REJECTION = /\breviewer artifact admission ([a-z_]+):/
const ADMISSION_DIAGNOSTIC = /; admission_diagnostic=(\{[^\r\n]{1,1024}\})$/
const ADMISSION_DIAGNOSTIC_REASONS = new Set([
  "expected_path_and_line", "line_suffix_not_integer", "line_must_be_positive",
  "path_must_be_repository_relative", "path_must_be_canonical", "line_not_changed_by_candidate",
])
const ADMISSION_DIAGNOSTIC_CODE = { INVALID_FINDING_LOCATION: "invalid_finding_location", CANDIDATE_CAUSALITY_UNPROVEN: "candidate_causality_unproven" } as const
interface AdmissionDiagnostic {
  code: (typeof ADMISSION_DIAGNOSTIC_CODE)[keyof typeof ADMISSION_DIAGNOSTIC_CODE]
  finding_id: string
  location: string
  reason: string
}
function safeAdmissionLocation(value: unknown, code: unknown, reason: unknown): value is string {
  if (typeof value !== "string" || value.length > 256 || /[\u0000-\u001f\u007f\\]/.test(value) || /^(?:[A-Za-z]:[\\/]|[\\/])/.test(value)) return false
  const parts = value.split(":")
  if (parts.length !== 2 || !/^[A-Za-z0-9+.-]{0,64}$/.test(parts[1]) ||
      !parts[0].split("/").every((segment) => segment !== "" && segment !== "." && segment !== "..")) return false
  if (code === ADMISSION_DIAGNOSTIC_CODE.CANDIDATE_CAUSALITY_UNPROVEN) {
    return reason === "line_not_changed_by_candidate" && /^\d+$/.test(parts[1]) && /[1-9]/.test(parts[1])
  }
  if (code !== ADMISSION_DIAGNOSTIC_CODE.INVALID_FINDING_LOCATION) return false
  if (reason === "expected_path_and_line") return parts[1] === ""
  if (reason === "line_must_be_positive") return /^(?:-\d+|\+?0+)$/.test(parts[1])
  return reason === "line_suffix_not_integer" && parts[1] !== "" &&
    !/^\d+$/.test(parts[1]) && !/^(?:-\d+|\+?0+)$/.test(parts[1])
}

function admissionRejection(cause: unknown): { decision: string, diagnostic?: AdmissionDiagnostic } | undefined {
  const message = errorMessage(cause)
  const match = ADMISSION_REJECTION.exec(message)
  if (!match) return undefined
  const detail = ADMISSION_DIAGNOSTIC.exec(message)
  if (!detail) return { decision: match[1] }
  try {
    const parsed = JSON.parse(detail[1]) as Partial<AdmissionDiagnostic>
    const safeLocation = safeAdmissionLocation(parsed.location, parsed.code, parsed.reason)
    const safeID = typeof parsed.finding_id === "string" && /^R[1-4]-[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(parsed.finding_id)
    const safeCode = Object.values(ADMISSION_DIAGNOSTIC_CODE).includes(parsed.code as AdmissionDiagnostic["code"])
    const safeReason = typeof parsed.reason === "string" && ADMISSION_DIAGNOSTIC_REASONS.has(parsed.reason)
    return safeLocation && safeID && safeCode && safeReason
      ? { decision: match[1], diagnostic: parsed as AdmissionDiagnostic }
      : { decision: match[1] }
  } catch {
    return { decision: match[1] }
  }
}

function admissionRecoveryKey(binding: ReviewBinding): string | undefined {
  if (!binding.revision || !binding.repository_context || !binding.subject_hash) return undefined
  return JSON.stringify([binding.lineage, binding.target, binding.revision, binding.repository_context, binding.lens, binding.order, binding.subject_hash])
}

const MAX_ADMISSION_RECOVERY_SESSIONS = 64
const MAX_ADMISSION_RECOVERIES_PER_SESSION = 8
const ADMISSION_RECOVERY_STATUS = { RELAUNCH: "relaunch", EXHAUSTED: "exhausted", UNAVAILABLE: "unavailable" } as const
type AdmissionRecoveryStatus = (typeof ADMISSION_RECOVERY_STATUS)[keyof typeof ADMISSION_RECOVERY_STATUS]
type AdmissionRecoveryStore = Map<string, Set<string>>
interface AdmissionRecoveryContext {
  sessionID: string
  store: AdmissionRecoveryStore
}

function clearAdmissionRecovery(store: AdmissionRecoveryStore, sessionID: string, binding: ReviewBinding): void {
  const key = admissionRecoveryKey(binding)
  const session = store.get(sessionID)
  if (!key || !session) return
  session.delete(key)
  if (session.size === 0) store.delete(sessionID)
}

function claimAdmissionRecovery(store: AdmissionRecoveryStore, sessionID: string, binding: ReviewBinding): AdmissionRecoveryStatus {
  const key = admissionRecoveryKey(binding)
  if (!key || sessionID === "") return ADMISSION_RECOVERY_STATUS.UNAVAILABLE
  const existing = store.get(sessionID)
  if (existing?.delete(key)) {
    if (existing.size === 0) store.delete(sessionID)
    return ADMISSION_RECOVERY_STATUS.EXHAUSTED
  }
  if (!existing && store.size >= MAX_ADMISSION_RECOVERY_SESSIONS) return ADMISSION_RECOVERY_STATUS.UNAVAILABLE
  const session = existing ?? new Set<string>()
  if (session.size >= MAX_ADMISSION_RECOVERIES_PER_SESSION) return ADMISSION_RECOVERY_STATUS.UNAVAILABLE
  session.add(key)
  if (!existing) store.set(sessionID, session)
  return ADMISSION_RECOVERY_STATUS.RELAUNCH
}

function sessionErrorMessage(binding: ReviewBinding, cause: unknown, code: string, recovery?: AdmissionRecoveryContext): string {
  if (!binding.repository_context) return errorMessage(cause)
  if (gitTrustRefusal(binding, cause)) return GIT_TRUST_REFUSAL_MESSAGE
  const admission = admissionRejection(cause)
  if (admission) {
    if (!admission.diagnostic) {
      if (recovery) clearAdmissionRecovery(recovery.store, recovery.sessionID, binding)
      return `reviewer_admission_recovery_unavailable: native admission rejected the reviewer result as ${admission.decision} without a safe actionable diagnostic; ` +
        "stop relaunching this lens and surface the terminal failure to the maintainer"
    }
    const status = recovery
      ? claimAdmissionRecovery(recovery.store, recovery.sessionID, binding)
      : ADMISSION_RECOVERY_STATUS.UNAVAILABLE
    if (status !== ADMISSION_RECOVERY_STATUS.RELAUNCH) {
      const terminalCode = status === ADMISSION_RECOVERY_STATUS.EXHAUSTED
        ? "reviewer_admission_recovery_exhausted"
        : "reviewer_admission_recovery_unavailable"
      return `${terminalCode}: corrected reviewer result was rejected as ${admission.decision}; ` +
        "stop relaunching this lens and surface the terminal failure to the maintainer"
    }
    const detail = `; finding ${admission.diagnostic.finding_id} at ${JSON.stringify(admission.diagnostic.location)}: ${admission.diagnostic.reason}`
    const correction = admission.diagnostic?.code === ADMISSION_DIAGNOSTIC_CODE.CANDIDATE_CAUSALITY_UNPROVEN
      ? "; anchor the finding to a candidate-changed line that proves the claimed causality"
      : "; use one repository-relative path followed by one positive integer line"
    return `${code}: native admission rejected the reviewer result as ${admission.decision}${detail}; ` +
      `retrying capture with the same result cannot succeed${correction}; relaunch this lens reviewer exactly once to produce a corrected result`
  }
  return `${code}: provider-owned review operation failed; refresh the exact native next_transition or retry the same opaque binding`
}

function preservedReference(manifest: string): string {
  try {
    const parsed = JSON.parse(manifest) as { reference?: unknown; path?: unknown; sha256?: unknown }
    if (parsed && typeof parsed.reference === "string" && parsed.reference !== "") return parsed.reference
    if (parsed && typeof parsed.path === "string" && parsed.path !== "") return parsed.path
    if (parsed && typeof parsed.sha256 === "string" && parsed.sha256 !== "") return parsed.sha256
  } catch {
    // fall through to the full manifest
  }
  return manifest
}

// Bound on the raw payload embedded in a double-failure error message. The
// native side already caps preserved payloads at 4 MiB; embedding is a last
// resort into the session transcript, so keep it far smaller.
const PRESERVE_EMBED_LIMIT = 64 * 1024

function embeddedRawPayload(raw: string): string {
  if (raw.length <= PRESERVE_EMBED_LIMIT) return raw
  return `${raw.slice(0, PRESERVE_EMBED_LIMIT)}\n[truncated: first ${PRESERVE_EMBED_LIMIT} of ${raw.length} characters embedded]`
}

async function preservedCaptureFailure(
  cwd: string, binding: ReviewBinding, raw: unknown, cause: unknown, recovery?: AdmissionRecoveryContext,
): Promise<Error> {
  const captureFailure = sessionErrorMessage(binding, cause, "repository_context_capture_failed", recovery)
  if (typeof raw !== "string" || raw.trim() === "") {
    return new Error(`${captureFailure}; no raw reviewer result was available to preserve`)
  }
  try {
    const reviewClass = extractionClass(cause)
    const manifest = await preserveResult(cwd, binding, raw, reviewClass)
    return new Error(`${captureFailure}; raw reviewer result preserved for recovery as ${preservedReference(manifest)}`)
  } catch (preserveCause) {
    const preserveFailure = sessionErrorMessage(binding, preserveCause, "repository_context_preserve_failed")
    // Double failure: durable preservation itself failed, so the transcript is
    // the only remaining copy — embed the bounded payload in the error. Both
    // bindings need this identically: captureResult and preserveResult resolve
    // the same repository through the same binding path, so one environmental
    // refusal can fail both, and an opaque binding that omitted the payload
    // had no equivalent transcript fallback left.
    return new Error(
      `${captureFailure}; raw reviewer result could not be preserved: ${preserveFailure}; ` +
      `raw reviewer result follows for manual recovery:\n${embeddedRawPayload(raw)}`,
    )
  }
}

const ReviewResultArtifactsPlugin: Plugin = async ({ client, directory, worktree }) => {
  const admissionRecoveries: AdmissionRecoveryStore = new Map()
  return {
  dispose: async () => { admissionRecoveries.clear() },
  event: async ({ event }) => {
    if (event.type === "session.deleted") admissionRecoveries.delete(event.properties.info.id)
  },
  "tool.execute.before": async (input, output) => {
    if (input.tool !== "task" || typeof output.args?.subagent_type !== "string" ||
        !REVIEW_AGENTS.has(output.args.subagent_type)) return
    if (typeof output.args.prompt !== "string") {
      throw new Error("review task is missing GENTLE_AI_REVIEW_BINDING")
    }
    if (output.args.background === true) {
      throw new Error("bound review tasks must run in the foreground for native result capture")
    }
    const missingIsolation = missingIsolationEnvironment()
    if (missingIsolation.length > 0) {
      throw new Error(
        `immutable OpenCode candidate inspection requires ${missingIsolation.join(" and ")} set to "1" for this OpenCode process; ` +
        "without them, OpenCode concatenates live project instructions (AGENTS.md/CLAUDE.md/CONTEXT.md, local " +
        "`instructions` glob entries) and the live skill catalog into every subagent's system prompt regardless " +
        "of its tools, which can leak post-freeze worktree content into the reviewer. " +
        "Set the missing variable(s) in the environment this OpenCode process runs under, then relaunch the lens. " +
        "The reviewer was not launched, so its exactly-once invocation is preserved.",
      )
    }
    let remoteInstructions: string[]
    try {
      const configResponse = (await client.config.get({ query: { directory } })) as { data?: { instructions?: unknown }; error?: unknown }
      if (configResponse?.error) {
        throw new Error(JSON.stringify(configResponse.error))
      }
      remoteInstructions = remoteInstructionsEntries(configResponse?.data?.instructions)
    } catch (cause) {
      throw new Error(
        `immutable OpenCode candidate inspection could not verify the effective configuration for remote ` +
        `\`instructions\` entries: ${errorMessage(cause)}. OpenCode fetches any http(s):// instructions entry ` +
        "unconditionally into every session's system prompt, so an unverifiable configuration cannot be ruled safe. " +
        "Resolve the config read failure, then relaunch the lens. " +
        "The reviewer was not launched, so its exactly-once invocation is preserved.",
      )
    }
    if (remoteInstructions.length > 0) {
      throw new Error(
        `immutable OpenCode candidate inspection refuses a remote \`instructions\` entry: ${remoteInstructions.join(", ")}; ` +
        "OpenCode fetches this unconditionally into every session's system prompt regardless of tools, which could " +
        "inject attacker-controlled prose into the reviewer. Remove it from the effective OpenCode configuration, " +
        "then relaunch the lens. The reviewer was not launched, so its exactly-once invocation is preserved.",
      )
    }
    output.args.prompt = await injectReviewerContext(
      output.args.prompt,
      output.args.subagent_type,
      captureCwd(worktree, directory),
    )
  },
  "tool.execute.after": async (input, output) => {
    if (input.tool !== "task" || typeof input.args?.subagent_type !== "string" || !REVIEW_AGENTS.has(input.args.subagent_type)) return
    if (typeof input.args.prompt !== "string" || !BINDING.test(input.args.prompt)) return
    const lens = input.args.subagent_type
    const binding = parseBinding(input.args.prompt, lens)
    const cwd = captureCwd(worktree, directory)
    const recovery = { sessionID: input.sessionID, store: admissionRecoveries }
    // Extract the replayable payload exactly once, BEFORE capture: recovery
    // re-runs `review capture-result --input <preserved file>`, whose strict
    // decoder rejects the task envelope, so a capture failure must preserve
    // the extracted strict JSON — never the enveloped output.output.
    let result: string
    try {
      result = reviewerResult(output.output)
    } catch (cause) {
      // Extraction itself failed (malformed envelope): there is no extracted
      // payload, so preserve the raw envelope under the distinct extraction
      // cause for manual inspection.
      clearAdmissionRecovery(admissionRecoveries, input.sessionID, binding)
      throw await preservedCaptureFailure(cwd, binding, output.output, cause)
    }
    try {
      output.output = await captureResult(cwd, binding, result)
      clearAdmissionRecovery(admissionRecoveries, input.sessionID, binding)
    } catch (cause) {
      throw await preservedCaptureFailure(cwd, binding, result, cause, recovery)
    }
  },
  }
}

export default ReviewResultArtifactsPlugin
