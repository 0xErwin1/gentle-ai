package hostruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const requestDomain = "gentle-ai.host-runtime-request/v1"

const (
	MaxRetainedStreamBytes = 4 << 20
	MaxRetainedOutputBytes = 8 << 20
)

type TerminalCause string

const (
	TerminalExited           TerminalCause = "exited"
	TerminalDeadlineExceeded TerminalCause = "deadline_exceeded"
	TerminalCancelled        TerminalCause = "cancelled"
	TerminalSpawnFailed      TerminalCause = "spawn_failed"
	TerminalControlFailed    TerminalCause = "control_failed"
	TerminalOutputTruncated  TerminalCause = "output_truncated"
	TerminalCleanupFailed    TerminalCause = "cleanup_failed"
)

type StreamLimits struct {
	StdoutBytes int `json:"stdoutBytes"`
	StderrBytes int `json:"stderrBytes"`
}

type RedactionPlan struct {
	Literals []string `json:"-"`
}

type ExecStep struct {
	ExecutionID string            `json:"executionId"`
	Program     string            `json:"-"`
	Args        []string          `json:"-"`
	CWD         string            `json:"-"`
	Env         map[string]string `json:"-"`
	Deadline    time.Duration     `json:"-"`
	Limits      StreamLimits      `json:"limits"`
	Redaction   RedactionPlan     `json:"-"`
}

type StreamEvidence struct {
	RawDigest      string `json:"rawDigest"`
	RawBytes       int64  `json:"rawBytes"`
	Retained       string `json:"retained"`
	RetainedDigest string `json:"retainedDigest"`
	Truncated      bool   `json:"truncated"`
}

type ProcessEvidence struct {
	SchemaName        string         `json:"schemaName"`
	SchemaVersion     int            `json:"schemaVersion"`
	ExecutionID       string         `json:"executionId"`
	RequestDigest     string         `json:"requestDigest"`
	ProgramDigest     string         `json:"programDigest"`
	ArgvDigest        string         `json:"argvDigest"`
	CWDDigest         string         `json:"cwdDigest"`
	EnvironmentDigest string         `json:"environmentDigest"`
	StartedAt         time.Time      `json:"startedAt"`
	FinishedAt        time.Time      `json:"finishedAt"`
	TerminalCause     TerminalCause  `json:"terminalCause"`
	ExitCode          int            `json:"exitCode"`
	Stdout            StreamEvidence `json:"stdout"`
	Stderr            StreamEvidence `json:"stderr"`
	CleanupScope      string         `json:"cleanupScope"`
	CleanupComplete   bool           `json:"cleanupComplete"`
	provenanceSeal    [sha256.Size]byte
}

type ValidationError struct {
	Field  string
	Reason string
}

func (err *ValidationError) Error() string {
	return fmt.Sprintf("invalid host runtime step field %s: %s", err.Field, err.Reason)
}

type ExecutionError struct {
	Kind TerminalCause
}

func (err *ExecutionError) Error() string {
	return fmt.Sprintf("host runtime %s", err.Kind)
}

type Executor struct {
	now         func() time.Time
	launchKey   [sha256.Size]byte
	launchReady bool
}

func NewExecutor() *Executor {
	executor := &Executor{now: func() time.Time { return time.Now().UTC() }}
	if _, err := rand.Read(executor.launchKey[:]); err != nil {
		panic(fmt.Sprintf("initialize host runtime launch authority: %v", err))
	}
	executor.launchReady = true
	return executor
}

// ExecuteAuthorized is the only production process-launch surface. It validates
// the exact HCR request, consumes one opaque WorkRun-issued launch capability,
// durably claims that reservation, and only then creates a process.
func (executor *Executor) ExecuteAuthorized(
	ctx context.Context,
	step ExecStep,
	capability *LaunchCapability,
) (ProcessEvidence, error) {
	canonical, err := validateStep(step)
	if err != nil {
		return ProcessEvidence{}, err
	}
	binding := requestBindingForCanonical(canonical)
	if executor == nil || !executor.launchReady {
		return ProcessEvidence{}, errors.New("host runtime executor launch authority is not initialized")
	}
	if err := capability.consume(ctx, binding.RequestDigest, executor.launchKey); err != nil {
		return ProcessEvidence{}, err
	}
	return executor.executeCanonical(ctx, canonical)
}

// execute is deliberately package-private. HCR's own tests exercise terminal
// process behavior through it; provider code must use ExecuteAuthorized.
func (executor *Executor) execute(ctx context.Context, step ExecStep) (ProcessEvidence, error) {
	canonical, err := validateStep(step)
	if err != nil {
		return ProcessEvidence{}, err
	}
	return executor.executeCanonical(ctx, canonical)
}

func (executor *Executor) executeCanonical(
	ctx context.Context,
	canonical ExecStep,
) (ProcessEvidence, error) {
	if executor == nil {
		executor = NewExecutor()
	}
	if executor.now == nil {
		executor.now = func() time.Time { return time.Now().UTC() }
	}
	evidence := newProcessEvidence(canonical)
	if ctx.Err() != nil {
		now := executor.now()
		evidence.StartedAt = now
		evidence.FinishedAt = now
		evidence.CleanupScope = "not_started"
		evidence.CleanupComplete = true
		if errors.Is(ctx.Err(), context.Canceled) {
			evidence.TerminalCause = TerminalCancelled
		} else {
			evidence.TerminalCause = TerminalDeadlineExceeded
		}
		evidence.Stdout = newStreamCollector(canonical.Limits.StdoutBytes, canonical.Redaction.Literals).Evidence()
		evidence.Stderr = newStreamCollector(canonical.Limits.StderrBytes, canonical.Redaction.Literals).Evidence()
		return sealProcessEvidence(evidence), nil
	}

	commandContext, cancel := context.WithTimeout(ctx, canonical.Deadline)
	defer cancel()

	command := exec.Command(canonical.Program, canonical.Args...)
	command.Dir = canonical.CWD
	command.Env = environmentEntries(canonical.Env)

	stdout := newStreamCollector(canonical.Limits.StdoutBytes, canonical.Redaction.Literals)
	stderr := newStreamCollector(canonical.Limits.StderrBytes, canonical.Redaction.Literals)
	command.Stdout = stdout
	command.Stderr = stderr

	evidence.StartedAt = executor.now()
	release, startErr := startProcessTree(command)
	if startErr != nil {
		evidence.FinishedAt = executor.now()
		evidence.TerminalCause = TerminalSpawnFailed
		if command.Process != nil {
			evidence.TerminalCause = TerminalControlFailed
			evidence.CleanupComplete = cleanupFailedStart(command, release)
		}
		evidence.Stdout = stdout.Evidence()
		evidence.Stderr = stderr.Evidence()
		if !evidence.CleanupComplete && command.Process != nil {
			evidence.TerminalCause = TerminalCleanupFailed
		}
		return sealProcessEvidence(evidence), &ExecutionError{Kind: evidence.TerminalCause}
	}
	evidence.CleanupScope = processTreeCleanupScope()

	var (
		releaseOnce sync.Once
		releaseErr  error
	)
	releaseTree := func() {
		releaseOnce.Do(func() {
			releaseErr = release()
		})
	}

	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()

	var (
		waitErr      error
		contextCause TerminalCause
	)
	select {
	case waitErr = <-waited:
		releaseTree()
	case <-commandContext.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			contextCause = TerminalCancelled
		} else {
			contextCause = TerminalDeadlineExceeded
		}
		releaseTree()
		waitErr = <-waited
	}

	evidence.FinishedAt = executor.now()
	evidence.Stdout = stdout.Evidence()
	evidence.Stderr = stderr.Evidence()
	evidence.CleanupComplete = releaseErr == nil
	evidence.ExitCode = exitCode(waitErr)
	evidence.TerminalCause = classifyTerminalCause(contextCause, waitErr, evidence)

	if releaseErr != nil {
		evidence.TerminalCause = TerminalCleanupFailed
		return sealProcessEvidence(evidence), &ExecutionError{Kind: TerminalCleanupFailed}
	}
	return sealProcessEvidence(evidence), nil
}

func validateStep(step ExecStep) (ExecStep, error) {
	step.ExecutionID = strings.TrimSpace(step.ExecutionID)
	if step.ExecutionID == "" {
		return ExecStep{}, &ValidationError{Field: "executionId", Reason: "must not be empty"}
	}
	if strings.ContainsRune(step.ExecutionID, '\x00') {
		return ExecStep{}, &ValidationError{Field: "executionId", Reason: "must not contain NUL"}
	}
	if !filepath.IsAbs(step.Program) {
		return ExecStep{}, &ValidationError{Field: "program", Reason: "must be absolute"}
	}
	if !filepath.IsAbs(step.CWD) {
		return ExecStep{}, &ValidationError{Field: "cwd", Reason: "must be absolute"}
	}
	if step.Deadline <= 0 {
		return ExecStep{}, &ValidationError{Field: "deadline", Reason: "must be positive"}
	}
	if step.Limits.StdoutBytes <= 0 {
		return ExecStep{}, &ValidationError{Field: "limits.stdoutBytes", Reason: "must be positive"}
	}
	if step.Limits.StderrBytes <= 0 {
		return ExecStep{}, &ValidationError{Field: "limits.stderrBytes", Reason: "must be positive"}
	}
	if step.Limits.StdoutBytes > MaxRetainedStreamBytes {
		return ExecStep{}, &ValidationError{Field: "limits.stdoutBytes", Reason: fmt.Sprintf("must not exceed %d", MaxRetainedStreamBytes)}
	}
	if step.Limits.StderrBytes > MaxRetainedStreamBytes {
		return ExecStep{}, &ValidationError{Field: "limits.stderrBytes", Reason: fmt.Sprintf("must not exceed %d", MaxRetainedStreamBytes)}
	}
	if step.Limits.StdoutBytes+step.Limits.StderrBytes > MaxRetainedOutputBytes {
		return ExecStep{}, &ValidationError{Field: "limits", Reason: fmt.Sprintf("combined retained bytes must not exceed %d", MaxRetainedOutputBytes)}
	}
	for index, arg := range step.Args {
		if strings.ContainsRune(arg, '\x00') {
			return ExecStep{}, &ValidationError{Field: fmt.Sprintf("args[%d]", index), Reason: "must not contain NUL"}
		}
	}
	for name, value := range step.Env {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "=\x00") {
			return ExecStep{}, &ValidationError{Field: "env", Reason: "names must be non-empty and contain neither '=' nor NUL"}
		}
		if strings.ContainsRune(value, '\x00') {
			return ExecStep{}, &ValidationError{Field: "env." + name, Reason: "must not contain NUL"}
		}
	}
	seenRedactions := make(map[string]struct{}, len(step.Redaction.Literals))
	redactions := make([]string, 0, len(step.Redaction.Literals))
	for index, literal := range step.Redaction.Literals {
		if literal == "" {
			return ExecStep{}, &ValidationError{Field: fmt.Sprintf("redaction.literals[%d]", index), Reason: "must not be empty"}
		}
		if _, exists := seenRedactions[literal]; exists {
			continue
		}
		seenRedactions[literal] = struct{}{}
		redactions = append(redactions, literal)
	}
	sort.Slice(redactions, func(i, j int) bool {
		if len(redactions[i]) == len(redactions[j]) {
			return redactions[i] < redactions[j]
		}
		return len(redactions[i]) > len(redactions[j])
	})
	step.Redaction.Literals = redactions
	step.Args = append([]string(nil), step.Args...)
	step.Env = cloneEnvironment(step.Env)
	return step, nil
}

func newProcessEvidence(step ExecStep) ProcessEvidence {
	return ProcessEvidence{
		SchemaName:        "gentle-ai.host-process-evidence",
		SchemaVersion:     1,
		ExecutionID:       step.ExecutionID,
		RequestDigest:     requestDigest(step),
		ProgramDigest:     digestValues("gentle-ai.host-program/v1", step.Program),
		ArgvDigest:        digestValues("gentle-ai.host-argv/v1", step.Args...),
		CWDDigest:         digestValues("gentle-ai.host-cwd/v1", step.CWD),
		EnvironmentDigest: digestEnvironment(step.Env),
		ExitCode:          -1,
	}
}

func classifyTerminalCause(contextCause TerminalCause, waitErr error, evidence ProcessEvidence) TerminalCause {
	if contextCause != "" {
		return contextCause
	}
	if evidence.Stdout.Truncated || evidence.Stderr.Truncated {
		return TerminalOutputTruncated
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return TerminalControlFailed
		}
	}
	return TerminalExited
}

func cleanupFailedStart(command *exec.Cmd, release func() error) bool {
	cleanupComplete := true
	if release != nil {
		if err := release(); err != nil {
			cleanupComplete = false
		}
	}
	if command.Process != nil && command.ProcessState == nil {
		if err := command.Process.Kill(); err != nil {
			cleanupComplete = false
		}
		if err := command.Wait(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				cleanupComplete = false
			}
		}
	}
	return cleanupComplete
}

func exitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type streamCollector struct {
	digest     hash.Hash
	limit      int
	rawBytes   int64
	retained   []byte
	truncated  bool
	redactions []string
}

func newStreamCollector(limit int, redactions []string) *streamCollector {
	return &streamCollector{
		digest:     sha256.New(),
		limit:      limit,
		retained:   make([]byte, 0, limit),
		redactions: append([]string(nil), redactions...),
	}
}

func (collector *streamCollector) Write(payload []byte) (int, error) {
	_, _ = collector.digest.Write(payload)
	collector.rawBytes += int64(len(payload))
	remaining := collector.limit - len(collector.retained)
	if remaining > 0 {
		if remaining > len(payload) {
			remaining = len(payload)
		}
		collector.retained = append(collector.retained, payload[:remaining]...)
	}
	if len(payload) > remaining {
		collector.truncated = true
	}
	return len(payload), nil
}

func (collector *streamCollector) Evidence() StreamEvidence {
	retained := "[TRUNCATED]"
	if !collector.truncated {
		retained = string(collector.retained)
		for _, literal := range collector.redactions {
			retained = strings.ReplaceAll(retained, literal, "[REDACTED]")
		}
	}
	return StreamEvidence{
		RawDigest:      "sha256:" + hex.EncodeToString(collector.digest.Sum(nil)),
		RawBytes:       collector.rawBytes,
		Retained:       retained,
		RetainedDigest: digestValues("gentle-ai.host-retained-output/v1", retained),
		Truncated:      collector.truncated,
	}
}

func environmentEntries(environment map[string]string) []string {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, name+"="+environment[name])
	}
	return entries
}

func cloneEnvironment(environment map[string]string) map[string]string {
	cloned := make(map[string]string, len(environment))
	for name, value := range environment {
		cloned[name] = value
	}
	return cloned
}

func digestEnvironment(environment map[string]string) string {
	values := environmentEntries(environment)
	return digestValues("gentle-ai.host-environment/v1", values...)
}

func requestDigest(step ExecStep) string {
	hash := sha256.New()
	writeValue(hash, requestDomain)
	writeValue(hash, step.ExecutionID)
	writeValue(hash, step.Program)
	for _, arg := range step.Args {
		writeValue(hash, arg)
	}
	writeValue(hash, step.CWD)
	for _, entry := range environmentEntries(step.Env) {
		writeValue(hash, entry)
	}
	writeValue(hash, step.Deadline.String())
	writeValue(hash, strconv.Itoa(step.Limits.StdoutBytes))
	writeValue(hash, strconv.Itoa(step.Limits.StderrBytes))
	for _, literal := range step.Redaction.Literals {
		writeValue(hash, digestValues("gentle-ai.host-redaction-literal/v1", literal))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func digestValues(domain string, values ...string) string {
	hash := sha256.New()
	writeValue(hash, domain)
	for _, value := range values {
		writeValue(hash, value)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func writeValue(writer hash.Hash, value string) {
	_, _ = writer.Write([]byte(strconv.Itoa(len(value))))
	_, _ = writer.Write([]byte{0})
	_, _ = writer.Write([]byte(value))
	_, _ = writer.Write([]byte{0})
}
