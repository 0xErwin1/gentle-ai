package mutationintegrity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const maximumCompletionRecordBytes = 8 << 20

var ErrCompletionNotFound = errors.New("mutation completion not found")

// Store persists immutable MMI completion evidence under the repository's
// validated Git common directory and isolates linked worktrees by identity.
type Store struct {
	root  string
	lease *reviewtransaction.RepositoryIdentityLease
}

func OpenStore(
	ctx context.Context,
	lease *reviewtransaction.RepositoryIdentityLease,
) (Store, error) {
	if lease == nil {
		return Store{}, errors.New("mutation completion store requires a repository identity lease")
	}
	if err := lease.Validate(ctx); err != nil {
		return Store{}, err
	}
	identity := lease.Identity()
	root := filepath.Join(
		identity.GitCommonDir,
		"gentle-ai",
		"mutation-integrity",
		"v1",
		"repositories",
		lease.StorageKey(),
		"completions",
	)
	if err := ensureSafeDirectory(identity.GitCommonDir, root); err != nil {
		return Store{}, err
	}
	if err := lease.Validate(ctx); err != nil {
		return Store{}, err
	}
	return Store{root: root, lease: lease}, nil
}

func (store Store) Put(
	ctx context.Context,
	completion Completion,
) (string, error) {
	if err := store.validate(ctx); err != nil {
		return "", err
	}
	if err := completion.Validate(); err != nil {
		return "", err
	}
	if completion.RepositoryRef != store.lease.Identity().RepositoryRef {
		return "", errors.New("mutation completion belongs to another repository")
	}
	payload, err := json.Marshal(completion)
	if err != nil {
		return "", err
	}
	payload = append(payload, '\n')
	if len(payload) > maximumCompletionRecordBytes {
		return "", errors.New("mutation completion exceeds durable record limit")
	}
	path, err := store.objectPath(completion.CompletionRef)
	if err != nil {
		return "", err
	}
	if err := publishImmutable(path, payload); err != nil {
		return "", err
	}
	if err := store.validate(ctx); err != nil {
		return "", err
	}
	return completion.CompletionRef, nil
}

func (store Store) Read(
	ctx context.Context,
	ref string,
) (Completion, error) {
	if err := store.validate(ctx); err != nil {
		return Completion{}, err
	}
	path, err := store.objectPath(ref)
	if err != nil {
		return Completion{}, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return Completion{}, ErrCompletionNotFound
	}
	if err != nil {
		return Completion{}, err
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maximumCompletionRecordBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return Completion{}, readErr
	}
	if closeErr != nil {
		return Completion{}, closeErr
	}
	if len(payload) > maximumCompletionRecordBytes {
		return Completion{}, errors.New("mutation completion record exceeds limit")
	}
	var completion Completion
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&completion); err != nil {
		return Completion{}, errors.New("mutation completion record is corrupt")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Completion{}, errors.New("mutation completion record has trailing data")
	}
	if err := completion.Validate(); err != nil || completion.CompletionRef != ref ||
		completion.RepositoryRef != store.lease.Identity().RepositoryRef {
		return Completion{}, errors.New("mutation completion record is corrupt")
	}
	return completion, nil
}

func (store Store) Root() string { return store.root }

func (store Store) validate(ctx context.Context) error {
	if store.root == "" || !filepath.IsAbs(store.root) || store.lease == nil {
		return errors.New("mutation completion store is not initialized")
	}
	if err := store.lease.Validate(ctx); err != nil {
		return err
	}
	info, err := os.Lstat(store.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("mutation completion store root is unsafe")
	}
	return nil
}

func (store Store) objectPath(ref string) (string, error) {
	if !sha256RefPattern.MatchString(ref) {
		return "", errors.New("mutation completion reference must be sha256")
	}
	name := strings.TrimPrefix(ref, "sha256:") + ".json"
	return filepath.Join(store.root, name), nil
}

func ensureSafeDirectory(base, target string) error {
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("mutation completion store escaped Git common directory")
	}
	current := base
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		switch {
		case os.IsNotExist(err):
			if err := os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(current)
			if err != nil {
				return err
			}
		case err != nil:
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe mutation completion directory %q", current)
		}
	}
	return nil
}

func publishImmutable(path string, payload []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, payload) {
			return nil
		}
		return errors.New("immutable mutation completion conflict")
	}
	if !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".completion-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
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
		existing, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Equal(existing, payload) {
			return nil
		}
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
