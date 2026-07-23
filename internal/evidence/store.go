package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

const (
	storeRelativeRoot        = "gentle-ai/execution-evidence/v1"
	maxTicketRecordBytes     = 4 << 20
	maxExecutionRecordBytes  = hostruntime.MaxRetainedOutputBytes*6 + 2<<20
	maxDiagnosticRecordBytes = 64 << 10
	maxGitPathOutputBytes    = 64 << 10
)

var ErrEvidenceNotFound = errors.New("immutable evidence artifact not found")

type ImmutableConflictError struct {
	Binding     string
	ExistingRef string
	ProposedRef string
}

func (err *ImmutableConflictError) Error() string {
	return fmt.Sprintf("immutable evidence conflict for %s: existing %s, proposed %s",
		err.Binding, err.ExistingRef, err.ProposedRef)
}

type CorruptionError struct {
	Artifact string
}

func (err *CorruptionError) Error() string {
	return "immutable evidence artifact is corrupt: " + err.Artifact
}

// Store is rooted only in the selected repository's validated Git common
// directory. Its objects and bindings are immutable and content addressed.
type Store struct {
	root      string
	commonDir string
}

func OpenStore(ctx context.Context, repositoryPath string) (Store, error) {
	commonDir, err := resolveGitCommonDirectory(ctx, repositoryPath)
	if err != nil {
		return Store{}, err
	}
	parent := filepath.Join(commonDir, "gentle-ai")
	if err := ensureDirectoryBelow(commonDir, parent, 0o755, false); err != nil {
		return Store{}, err
	}
	root := filepath.Join(commonDir, filepath.FromSlash(storeRelativeRoot))
	if err := ensureDirectoryBelow(parent, root, 0o700, true); err != nil {
		return Store{}, err
	}
	return Store{root: root, commonDir: commonDir}, nil
}

// Root returns the provider-private storage location for diagnostics and
// maintenance tooling. It is not an authority or a portable artifact ref.
func (store Store) Root() string {
	return store.root
}

func (store Store) PutTicket(ticket ActionTicket) (string, error) {
	if err := store.validate(); err != nil {
		return "", err
	}
	if err := ticket.Validate(); err != nil {
		return "", err
	}
	slotRef, err := ticket.SlotBindingRef()
	if err != nil {
		return "", err
	}
	payload, err := canonicalPayload(ticket)
	if err != nil {
		return "", err
	}
	if len(payload) > maxTicketRecordBytes {
		return "", errors.New("action ticket exceeds the durable record limit")
	}
	if err := store.publishObject("tickets", ticket.TicketRef, payload); err != nil {
		return "", err
	}
	if err := store.publishBinding("ticket-slots", slotRef, ticket.TicketRef); err != nil {
		return "", err
	}
	return ticket.TicketRef, nil
}

func (store Store) ReadTicket(ticketRef string) (ActionTicket, error) {
	var ticket ActionTicket
	payload, err := store.readObject("tickets", ticketRef, maxTicketRecordBytes)
	if err != nil {
		return ActionTicket{}, err
	}
	if err := decodeCanonical(payload, &ticket); err != nil || ticket.Validate() != nil ||
		ticket.TicketRef != ticketRef {
		return ActionTicket{}, &CorruptionError{Artifact: "action_ticket"}
	}
	slotRef, err := ticket.SlotBindingRef()
	if err != nil {
		return ActionTicket{}, &CorruptionError{Artifact: "action_ticket_slot"}
	}
	if err := store.requireBindingWinner("ticket-slots", slotRef, ticket.TicketRef); err != nil {
		return ActionTicket{}, err
	}
	return ticket, nil
}

func (store Store) PutExecution(evidence ExecutionEvidence) (string, error) {
	if err := store.validate(); err != nil {
		return "", err
	}
	ticket, err := store.ReadTicket(evidence.TicketRef)
	if err != nil {
		return "", err
	}
	if err := evidence.ValidateFor(ticket); err != nil {
		return "", err
	}
	payload, err := canonicalPayload(evidence)
	if err != nil {
		return "", err
	}
	if len(payload) > maxExecutionRecordBytes {
		return "", errors.New("execution evidence exceeds the durable record limit")
	}
	if err := store.publishObject("executions", evidence.EvidenceRef, payload); err != nil {
		return "", err
	}
	if err := store.publishBinding("ticket-executions", evidence.TicketRef, evidence.EvidenceRef); err != nil {
		return "", err
	}
	return evidence.EvidenceRef, nil
}

func (store Store) ReadExecution(evidenceRef string) (ExecutionEvidence, error) {
	var evidence ExecutionEvidence
	payload, err := store.readObject("executions", evidenceRef, maxExecutionRecordBytes)
	if err != nil {
		return ExecutionEvidence{}, err
	}
	if err := decodeCanonical(payload, &evidence); err != nil ||
		evidence.validateStored() != nil || evidence.EvidenceRef != evidenceRef {
		return ExecutionEvidence{}, &CorruptionError{Artifact: "execution_evidence"}
	}
	ticket, err := store.ReadTicket(evidence.TicketRef)
	if err != nil {
		return ExecutionEvidence{}, err
	}
	if evidence.validateStoredFor(ticket) != nil {
		return ExecutionEvidence{}, &CorruptionError{Artifact: "execution_ticket_binding"}
	}
	if err := store.requireBindingWinner(
		"ticket-executions", evidence.TicketRef, evidence.EvidenceRef,
	); err != nil {
		return ExecutionEvidence{}, err
	}
	return sealExecutionEvidence(evidence), nil
}

func (store Store) ReadExecutionForTicket(ticketRef string) (ExecutionEvidence, error) {
	evidenceRef, err := store.readBinding("ticket-executions", ticketRef)
	if err != nil {
		return ExecutionEvidence{}, err
	}
	evidence, err := store.ReadExecution(evidenceRef)
	if err != nil {
		return ExecutionEvidence{}, err
	}
	if evidence.TicketRef != ticketRef {
		return ExecutionEvidence{}, &CorruptionError{Artifact: "ticket_execution_index"}
	}
	return evidence, nil
}

func (store Store) PutDiagnostic(diagnostic Diagnostic) (string, error) {
	if err := store.validate(); err != nil {
		return "", err
	}
	if err := diagnostic.Validate(); err != nil {
		return "", err
	}
	payload, err := canonicalPayload(diagnostic)
	if err != nil {
		return "", err
	}
	if len(payload) > maxDiagnosticRecordBytes {
		return "", errors.New("evidence diagnostic exceeds the durable record limit")
	}
	if err := store.publishObject("diagnostics", diagnostic.DiagnosticRef, payload); err != nil {
		return "", err
	}
	return diagnostic.DiagnosticRef, nil
}

func (store Store) ReadDiagnostic(diagnosticRef string) (Diagnostic, error) {
	var diagnostic Diagnostic
	payload, err := store.readObject("diagnostics", diagnosticRef, maxDiagnosticRecordBytes)
	if err != nil {
		return Diagnostic{}, err
	}
	if err := decodeCanonical(payload, &diagnostic); err != nil ||
		diagnostic.Validate() != nil || diagnostic.DiagnosticRef != diagnosticRef {
		return Diagnostic{}, &CorruptionError{Artifact: "diagnostic"}
	}
	return diagnostic, nil
}

// AdmitAndStore validates one HCR result and publishes its exact immutable
// envelope. Production callers should PutTicket before launching HCR; this
// helper still requires that ticket to exist and never starts a process.
func (store Store) AdmitAndStore(ticket ActionTicket, process hostruntime.ProcessEvidence) (ExecutionEvidence, error) {
	stored, err := store.ReadTicket(ticket.TicketRef)
	if err != nil {
		return ExecutionEvidence{}, err
	}
	if stored.TicketRef != ticket.TicketRef {
		return ExecutionEvidence{}, &CorruptionError{Artifact: "action_ticket"}
	}
	evidence, err := AdmitExecution(ticket, process)
	if err != nil {
		return ExecutionEvidence{}, err
	}
	if _, err := store.PutExecution(evidence); err != nil {
		return ExecutionEvidence{}, err
	}
	return evidence, nil
}

func (store Store) validate() error {
	if store.root == "" || store.commonDir == "" ||
		!filepath.IsAbs(store.root) || !filepath.IsAbs(store.commonDir) {
		return errors.New("evidence store is not initialized")
	}
	commonInfo, err := os.Lstat(store.commonDir)
	if err != nil || !commonInfo.IsDir() || evidencePathUnsafe(store.commonDir, commonInfo) {
		return errors.New("evidence store Git common directory is unavailable or unsafe")
	}
	if err := validateDirectoryBelow(store.commonDir, store.root, false); err != nil {
		return errors.New("evidence store path is unavailable or unsafe")
	}
	privateBase := filepath.Join(store.commonDir, "gentle-ai")
	if err := validateDirectoryBelow(privateBase, store.root, true); err != nil {
		return errors.New("evidence store private path is unavailable or unsafe")
	}
	info, err := os.Lstat(store.root)
	if err != nil || !info.IsDir() || evidencePathUnsafe(store.root, info) ||
		!privateEvidencePathSafe(store.root, info) {
		return errors.New("evidence store root is unavailable or unsafe")
	}
	return nil
}

func (store Store) publishObject(kind, ref string, payload []byte) error {
	path, err := store.objectPath(kind, ref, ".json", true)
	if err != nil {
		return err
	}
	return publishImmutable(path, payload)
}

func (store Store) publishBinding(kind, bindingRef, artifactRef string) error {
	path, err := store.objectPath(kind, bindingRef, ".ref", true)
	if err != nil {
		return err
	}
	payload := []byte(artifactRef + "\n")
	err = publishImmutable(path, payload)
	if err == nil {
		return nil
	}
	var conflict *immutableBytesConflict
	if !errors.As(err, &conflict) {
		return err
	}
	existing := strings.TrimSpace(string(conflict.existing))
	if !validSHA256Ref(existing) {
		return &CorruptionError{Artifact: kind}
	}
	return &ImmutableConflictError{
		Binding: kind, ExistingRef: existing, ProposedRef: artifactRef,
	}
}

func (store Store) readObject(kind, ref string, limit int) ([]byte, error) {
	if err := store.validate(); err != nil {
		return nil, err
	}
	path, err := store.objectPath(kind, ref, ".json", false)
	if err != nil {
		return nil, err
	}
	return readImmutable(path, limit)
}

func (store Store) readBinding(kind, bindingRef string) (string, error) {
	if err := store.validate(); err != nil {
		return "", err
	}
	path, err := store.objectPath(kind, bindingRef, ".ref", false)
	if err != nil {
		return "", err
	}
	payload, err := readImmutable(path, 256)
	if err != nil {
		return "", err
	}
	if len(payload) != len("sha256:")+64+1 || payload[len(payload)-1] != '\n' {
		return "", &CorruptionError{Artifact: kind}
	}
	ref := strings.TrimSuffix(string(payload), "\n")
	if !validSHA256Ref(ref) {
		return "", &CorruptionError{Artifact: kind}
	}
	return ref, nil
}

// requireBindingWinner treats content-addressed object presence as
// non-authoritative. Only the artifact named by the immutable binding is an
// admitted ticket or execution; objects published by a losing contender stay
// untrusted and can never regain provenance through durable replay.
func (store Store) requireBindingWinner(kind, bindingRef, artifactRef string) error {
	winner, err := store.readBinding(kind, bindingRef)
	if err != nil {
		return err
	}
	if winner == artifactRef {
		return nil
	}
	return &ImmutableConflictError{
		Binding: kind, ExistingRef: winner, ProposedRef: artifactRef,
	}
}

func (store Store) objectPath(kind, ref, suffix string, create bool) (string, error) {
	if !validSHA256Ref(ref) {
		return "", errors.New("immutable evidence ref must be lowercase SHA-256")
	}
	switch kind {
	case "tickets", "executions", "diagnostics", "ticket-slots", "ticket-executions":
	default:
		return "", errors.New("unsupported immutable evidence object kind")
	}
	digest := strings.TrimPrefix(ref, "sha256:")
	dir := filepath.Join(store.root, kind, digest[:2])
	if create {
		if err := ensureDirectoryBelow(store.root, dir, 0o700, true); err != nil {
			return "", err
		}
	} else {
		if err := validateDirectoryBelow(store.root, dir, true); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", ErrEvidenceNotFound
			}
			return "", err
		}
	}
	path := filepath.Join(dir, digest[2:]+suffix)
	relative, err := filepath.Rel(store.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", errors.New("immutable evidence path escapes its store")
	}
	return path, nil
}

func validateDirectoryBelow(base, target string, private bool) error {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return errors.New("evidence directory escapes its trusted base")
	}
	current := base
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || evidencePathUnsafe(current, info) {
			return errors.New("evidence directory is unavailable or unsafe")
		}
		if private && !privateEvidencePathSafe(current, info) {
			return errors.New("evidence directory permissions are not owner-only")
		}
	}
	return nil
}

type immutableBytesConflict struct {
	existing []byte
}

func (err *immutableBytesConflict) Error() string {
	return "immutable artifact already exists with different bytes"
}

func publishImmutable(path string, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("immutable artifact payload is empty")
	}
	if existing, err := readImmutable(path, len(payload)+1); err == nil {
		if bytes.Equal(existing, payload) {
			return syncEvidenceDirectory(filepath.Dir(path))
		}
		return &immutableBytesConflict{existing: existing}
	} else if !errors.Is(err, ErrEvidenceNotFound) {
		return err
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".publish-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if err := securePrivateEvidencePath(tempPath); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempPath, path); err != nil {
		existing, readErr := readImmutable(path, len(payload)+1)
		if readErr == nil {
			if bytes.Equal(existing, payload) {
				return syncEvidenceDirectory(dir)
			}
			return &immutableBytesConflict{existing: existing}
		}
		return err
	}
	return syncEvidenceDirectory(dir)
}

func readImmutable(path string, limit int) ([]byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, ErrEvidenceNotFound
	}
	if err != nil || !info.Mode().IsRegular() || evidencePathUnsafe(path, info) ||
		!privateEvidencePathSafe(path, info) || info.Size() <= 0 || info.Size() > int64(limit) {
		return nil, &CorruptionError{Artifact: "file"}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, &CorruptionError{Artifact: "file_identity"}
	}
	payload, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(payload) == 0 || len(payload) > limit {
		return nil, &CorruptionError{Artifact: "file_payload"}
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) ||
		opened.Size() != after.Size() || opened.ModTime() != after.ModTime() ||
		int64(len(payload)) != after.Size() {
		return nil, &CorruptionError{Artifact: "file_changed_during_read"}
	}
	return payload, nil
}

func canonicalPayload(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func decodeCanonical(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("immutable evidence record contains trailing JSON")
	}
	canonical, err := canonicalPayload(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(payload, canonical) {
		return errors.New("immutable evidence record is not canonical JSON")
	}
	return nil
}

func ensureDirectoryBelow(base, target string, mode os.FileMode, private bool) error {
	base, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return err
	}
	target = filepath.Clean(target)
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return errors.New("evidence directory escapes its trusted base")
	}
	current := base
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			_, err = createEvidenceDirectory(current, mode, private)
			if err != nil {
				return err
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil || !info.IsDir() || evidencePathUnsafe(current, info) {
			return errors.New("evidence directory is unavailable or unsafe")
		}
		if private && !privateEvidencePathSafe(current, info) {
			return errors.New("evidence directory permissions are not owner-only")
		}
		// Sync even when this caller observed an existing directory. Another
		// contender may have just created it and not yet synced its parent; no
		// successful caller may publish beneath it before that entry is durable.
		if err := syncEvidenceDirectory(current); err != nil {
			return err
		}
		if err := syncEvidenceDirectory(filepath.Dir(current)); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectoryCompatible(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		unsupported := errors.Is(err, syscall.EINVAL) || errors.Is(err, errors.ErrUnsupported) ||
			evidenceRuntimeGOOS() == "windows" && errors.Is(err, os.ErrPermission)
		if !unsupported {
			return err
		}
	}
	return closeErr
}

var evidenceRuntimeGOOS = func() string { return runtime.GOOS }
var syncEvidenceDirectory = syncDirectoryCompatible

var evidenceGitCommandContext = exec.CommandContext

func resolveGitCommonDirectory(ctx context.Context, repositoryPath string) (string, error) {
	repositoryPath = strings.TrimSpace(repositoryPath)
	if repositoryPath == "" {
		return "", errors.New("repository path is required")
	}
	absolute, err := filepath.Abs(repositoryPath)
	if err != nil {
		return "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", errors.New("repository path is not a directory")
	}
	rootOutput, err := runGitPathQuery(ctx, absolute, "--show-toplevel")
	if err != nil {
		return "", err
	}
	root, err := canonicalGitDirectory(absolute, rootOutput, true)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", errors.New("resolved repository does not contain the requested path")
	}
	output, err := runGitPathQuery(ctx, root, "--git-common-dir", "--git-dir")
	if err != nil {
		return "", err
	}
	records := bytes.Split(bytes.TrimSuffix(output, []byte{'\n'}), []byte{'\n'})
	if len(records) != 2 {
		return "", errors.New("Git repository selection must return exactly two directory records")
	}
	commonDir, err := canonicalGitDirectory(root, bytes.TrimSuffix(records[0], []byte{'\r'}), false)
	if err != nil {
		return "", err
	}
	gitDir, err := canonicalGitDirectory(root, bytes.TrimSuffix(records[1], []byte{'\r'}), false)
	if err != nil {
		return "", err
	}
	if err := validateGitCommonRelationship(gitDir, commonDir); err != nil {
		return "", err
	}
	return commonDir, nil
}

func runGitPathQuery(ctx context.Context, repo string, selectors ...string) ([]byte, error) {
	queryContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"--no-replace-objects", "--no-optional-locks", "-C", repo, "rev-parse"}
	args = append(args, selectors...)
	command := evidenceGitCommandContext(queryContext, "git", args...)
	command.Env = sanitizedGitEnvironment(os.Environ())
	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = maxGitPathOutputBytes, maxGitPathOutputBytes
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		if queryContext.Err() != nil {
			return nil, errors.New("resolve Git evidence store timed out")
		}
		return nil, errors.New("resolve Git evidence store failed")
	}
	if stdout.exceeded || stderr.exceeded || stderr.buffer.Len() != 0 {
		return nil, errors.New("Git evidence-store path query produced invalid output")
	}
	return append([]byte(nil), stdout.buffer.Bytes()...), nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *boundedBuffer) Write(payload []byte) (int, error) {
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		count := len(payload)
		if count > remaining {
			count = remaining
		}
		_, _ = buffer.buffer.Write(payload[:count])
	}
	if len(payload) > remaining {
		buffer.exceeded = true
	}
	return len(payload), nil
}

func sanitizedGitEnvironment(environment []string) []string {
	sanitized := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		name := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			name = entry[:index]
		}
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		sanitized = append(sanitized, entry)
	}
	return append(sanitized, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=0")
}

func canonicalGitDirectory(base string, record []byte, requireAbsolute bool) (string, error) {
	if len(record) > 0 && record[len(record)-1] == '\n' {
		record = record[:len(record)-1]
		if len(record) > 0 && record[len(record)-1] == '\r' {
			record = record[:len(record)-1]
		}
	}
	if len(record) == 0 || bytes.IndexByte(record, 0) >= 0 ||
		bytes.ContainsAny(record, "\r\n") || bytes.HasPrefix(record, []byte("--")) ||
		strings.TrimSpace(string(record)) == "" {
		return "", errors.New("Git directory output is not exactly one valid path record")
	}
	path := string(record)
	if requireAbsolute && !filepath.IsAbs(path) {
		return "", errors.New("Git repository root is not absolute")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
		relative, err := filepath.Rel(base, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
			filepath.IsAbs(relative) {
			return "", errors.New("relative Git directory escapes the repository root")
		}
	}
	path, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", errors.New("Git directory output is not a directory")
	}
	return filepath.Clean(path), nil
}

func validateGitCommonRelationship(gitDir, commonDir string) error {
	gitInfo, err := os.Stat(gitDir)
	if err != nil {
		return err
	}
	commonInfo, err := os.Stat(commonDir)
	if err != nil {
		return err
	}
	if os.SameFile(gitInfo, commonInfo) {
		return nil
	}
	record, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	record = bytes.TrimSuffix(bytes.TrimSuffix(record, []byte{'\n'}), []byte{'\r'})
	if err != nil || len(record) == 0 || bytes.IndexByte(record, 0) >= 0 ||
		bytes.ContainsAny(record, "\r\n") || bytes.HasPrefix(record, []byte("--")) ||
		strings.TrimSpace(string(record)) == "" {
		return errors.New("Git common directory relationship is invalid")
	}
	path := string(record)
	if !filepath.IsAbs(path) {
		path = filepath.Join(gitDir, path)
	}
	path, err = filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return errors.New("Git common directory relationship is invalid")
	}
	resolved, err := os.Stat(path)
	if err != nil || !os.SameFile(resolved, commonInfo) {
		return errors.New("Git common directory relationship is invalid")
	}
	return nil
}
