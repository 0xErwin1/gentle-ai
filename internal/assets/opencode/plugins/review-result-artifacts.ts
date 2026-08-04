import type { Plugin } from "@opencode-ai/plugin"
import { spawn } from "node:child_process"

const REVIEW_AGENTS = new Set(["review-risk", "review-resilience", "review-readability", "review-reliability"])
const BINDING = /^GENTLE_AI_REVIEW_BINDING (\{[^\n]+\})(?:\n|$)/
const TASK_RESULT = /^<task id="[^"\r\n]+" state="completed">\n<task_result>\n([\s\S]*?)\n<\/task_result>\n<\/task>$/
const TASK_TAG = /<\/?task(?:\s|>)|<\/?task_result>/
const REVIEW_OUTCOME = { UNSUPPORTED_CAPABILITY: "unsupported-capability" } as const

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

const LINEAGE_ID = /^[a-z0-9]+(?:-[a-z0-9]+)*$/
const SHA256_TOKEN = /^sha256:[a-f0-9]{64}$/
const REPOSITORY_CONTEXT_HANDLE = /^rctx1_[a-f0-9]{64}$/
const LENS_TOKEN = /^[a-z][a-z0-9-]{0,63}$/

// BINDING_SHAPES are the four field sets the native transitions emit, mapped to
// which optional fields that shape then requires. Keeping the shape explicit is
// what lets a rejection say WHICH set arrived and which ones exist, instead of
// "does not match the selected lens".
const BINDING_SHAPES = new Map<string, { revision: boolean, subjectHash: boolean }>([
  ["lens,lineage,order,target", { revision: false, subjectHash: false }],
  ["lens,lineage,order,subject_hash,target", { revision: false, subjectHash: true }],
  ["lens,lineage,order,repository_context,revision,target", { revision: true, subjectHash: false }],
  ["lens,lineage,order,repository_context,revision,subject_hash,target", { revision: true, subjectHash: true }],
])

const BINDING_REBUILD =
  "rebuild the prompt prefix from the exact fields of the provider-returned transition " +
  "(gentle-ai review status --cwd <repo> --contract gentle-ai.review-integration/v2 --next-transition)"

// observedValue describes what arrived without echoing it. The binding is
// authored by a model, so its values are untrusted text that must not be
// pasted into the transcript; the type, and for strings the length, is what
// distinguishes the reported causes (a quoted number, a truncated digest, an
// absent field) from each other.
function observedValue(value: unknown): string {
  if (value === undefined) return "no value"
  if (value === null) return "null"
  if (Array.isArray(value)) return `an array of length ${value.length}`
  if (typeof value === "string") return `a string of length ${value.length}`
  if (typeof value === "number") return Number.isSafeInteger(value) ? `the integer ${value}` : `the number ${value}`
  return `a ${typeof value}`
}

function bindingRefusal(field: string, requirement: string, value: unknown): Error {
  return new Error(
    `review task binding field "${field}" ${requirement}; received ${observedValue(value)}; ${BINDING_REBUILD}`,
  )
}

// parseBinding rejects one field at a time and names it.
//
// It used to evaluate thirteen conditions in a single boolean OR and throw one
// sentence, "review task binding does not match the selected lens", for every
// one of them (#2442). #2461 reported all four lenses rejected with exactly
// that sentence while the real cause was an unexpected field set. A refusal
// that forces the reader to open the validator's source is a dead end with
// extra steps, so each check below states the field, the expected shape, a
// redacted observation of what arrived, and the executable next step.
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
  const shape = BINDING_SHAPES.get(fields)
  if (!shape) {
    throw new Error(
      `review task binding has an unexpected field set; received {${scrubText(fields)}}; ` +
      `expected exactly one of ${[...BINDING_SHAPES.keys()].map((set) => `{${set}}`).join(", ")}; ` +
      `${BINDING_REBUILD}`,
    )
  }
  if (typeof value.lineage !== "string" || !LINEAGE_ID.test(value.lineage)) {
    throw bindingRefusal("lineage", "must be a lowercase hyphen-separated identifier", value.lineage)
  }
  if (typeof value.target !== "string" || !SHA256_TOKEN.test(value.target)) {
    throw bindingRefusal("target", "must be sha256: followed by 64 lowercase hex characters", value.target)
  }
  if (shape.revision) {
    if (typeof value.revision !== "string" || !SHA256_TOKEN.test(value.revision)) {
      throw bindingRefusal("revision", "must be sha256: followed by 64 lowercase hex characters", value.revision)
    }
    if (typeof value.repository_context !== "string" || !REPOSITORY_CONTEXT_HANDLE.test(value.repository_context)) {
      throw bindingRefusal("repository_context", "must be rctx1_ followed by 64 lowercase hex characters", value.repository_context)
    }
  }
  if (shape.subjectHash && (typeof value.subject_hash !== "string" || !SHA256_TOKEN.test(value.subject_hash))) {
    throw bindingRefusal("subject_hash", "must be sha256: followed by 64 lowercase hex characters", value.subject_hash)
  }
  if (value.lens !== lens) {
    const received = typeof value.lens === "string" && LENS_TOKEN.test(value.lens)
      ? `"${value.lens}"`
      : observedValue(value.lens)
    throw new Error(
      `review task binding field "lens" must equal the launched lens "${lens}"; received ${received}; ${BINDING_REBUILD}`,
    )
  }
  if (!Number.isSafeInteger(value.order)) {
    throw bindingRefusal("order", "must be a JSON number, not a quoted string, and a safe integer", value.order)
  }
  if ((value.order as number) < 0) {
    throw bindingRefusal("order", "must be zero or greater", value.order)
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
      : `If lineage ${binding.lineage} was started in a different repository (for example a nested one), run ` +
        `gentle-ai review status --cwd <that repository> --contract gentle-ai.review-integration/v2 --next-transition ` +
        `and relaunch the lens from the binding it returns: that binding carries a provider-issued repository ` +
        `context, which resolves independently of this session's working directory.`
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

async function injectReviewerContext(prompt: string, lens: string, cwd: string): Promise<string> {
  const binding = parseBinding(prompt, lens)
  const preflight = await preflightCapture(cwd, binding)
  const injectedBinding = { ...binding, subject_hash: preflight.artifact_subject.subject_hash }
  return `GENTLE_AI_REVIEW_BINDING ${JSON.stringify(injectedBinding)}\n` +
    `GENTLE_AI_REVIEW_CONTEXT ${JSON.stringify(preflight)}\n`
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

// The privacy gate a forwarded cause passes through. It mirrors
// reviewScrubDefectReportField in internal/cli/review_defect_report.go field
// for field -- same three patterns, same marker, same first-line-only rule --
// because the plugin cannot call into Go and the two surfaces must redact the
// same things. The native side scrubs what it forwards; this scrubs what the
// native side did not author, such as an OS-level spawn failure.
const REDACTION_MARKER = "<redacted>"
const ENV_ASSIGNMENT = /\b[A-Z][A-Z0-9_]{2,}=\S+/g
const EMAIL_ADDRESS = /[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}/g
const ABSOLUTE_PATH = /(?:[A-Za-z]:)?[\\/][^\s"'`]+/g
// Bounds a cause bound for a session transcript. Native failures can quote
// reviewer payload fragments, so this is a limit, not a formatting preference.
const CAUSE_LIMIT = 512

function scrubText(value: string): string {
  const scrubbed = value.split("\n", 1)[0]
    .replace(ENV_ASSIGNMENT, REDACTION_MARKER)
    .replace(EMAIL_ADDRESS, REDACTION_MARKER)
    .replace(ABSOLUTE_PATH, REDACTION_MARKER)
    .trim()
  return scrubbed.length > CAUSE_LIMIT ? `${scrubbed.slice(0, CAUSE_LIMIT)} (truncated)` : scrubbed
}

function scrubbedCause(cause: unknown): string {
  return scrubText(errorMessage(cause))
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

// sessionErrorMessage renders one native failure for the session transcript.
//
// It used to answer every opaque-binding failure that was neither a Git trust
// refusal nor a structured admission rejection with one constant sentence, so a
// strict-decode rejection (#2227), an unreachable authority (#2461) and a
// failed durable publish (#2411) all arrived identically. The rule that no
// unscrubbed native text reaches a transcript stays; what changes is that
// dropping the cause is no longer how that rule is kept. Everything now takes
// the same path -- scrub, then report -- so there is no branch left in which a
// cause can be silently discarded.
function sessionErrorMessage(binding: ReviewBinding, cause: unknown, code: string, recovery?: AdmissionRecoveryContext): string {
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
  const scrubbed = scrubbedCause(cause)
  return `${code}: provider-owned review operation failed${scrubbed === "" ? "" : `; cause: ${scrubbed}`}; ` +
    "refresh the exact native next_transition or retry the same binding"
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

const ReviewResultArtifactsPlugin: Plugin = async ({ directory, worktree }) => {
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
    parseBinding(output.args.prompt, output.args.subagent_type)
    throw new Error(REVIEW_OUTCOME.UNSUPPORTED_CAPABILITY)
  },
  "tool.execute.after": async (input, output) => {
    if (input.tool !== "task" || typeof input.args?.subagent_type !== "string" || !REVIEW_AGENTS.has(input.args.subagent_type)) return
    if (typeof input.args.prompt !== "string" || !BINDING.test(input.args.prompt)) return
    const lens = input.args.subagent_type
    const binding = parseBinding(input.args.prompt, lens)
    // The OpenCode anchor is the only source of the capture working directory.
    // A GENTLE_AI_REVIEW_CWD override used to win over it with no check that it
    // named the same repository, so a value left behind by an earlier session
    // silently redirected every capture into an unrelated checkout and stranded
    // every lens result as a preserved incident there (#2446). It is deleted
    // rather than validated: an opaque binding resolves its repository from the
    // provider-issued handle and never needed it, and a legacy binding is
    // recovered by refreshing the transition, which the preflight refusal now
    // names.
    const cwd = worktree || directory
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
