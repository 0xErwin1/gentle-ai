package reviewtransaction

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const storeLockSchema = "gentle-ai.review-store-lock/v1"

type storeLockOwner struct {
	Schema     string    `json:"schema"`
	OwnerID    string    `json:"owner_id"`
	PID        int       `json:"pid"`
	Host       string    `json:"host"`
	AcquiredAt time.Time `json:"acquired_at"`
}

type storeLock struct {
	file  *os.File
	owner storeLockOwner
}

type storeLockBusyError struct {
	Owner storeLockOwner
}

func (err storeLockBusyError) Error() string {
	pid := "unknown"
	if err.Owner.PID > 0 {
		pid = fmt.Sprintf("%d", err.Owner.PID)
	}
	host := strings.TrimSpace(err.Owner.Host)
	if host == "" {
		host = "unknown"
	}
	acquired := "unknown"
	if !err.Owner.AcquiredAt.IsZero() {
		acquired = err.Owner.AcquiredAt.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%v: authoritative review store is locked by pid=%s host=%s since=%s; retry after that owner exits", ErrConcurrentUpdate, pid, host, acquired)
}

func (err storeLockBusyError) Unwrap() error { return ErrConcurrentUpdate }

func acquireStoreLock(path string) (*storeLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	locked, err := tryLockFile(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire bounded review store lock: %w", err)
	}
	if !locked {
		owner := readStoreLockOwner(file)
		_ = file.Close()
		return nil, storeLockBusyError{Owner: owner}
	}

	owner, err := newStoreLockOwner()
	if err != nil {
		_ = unlockFile(file)
		_ = file.Close()
		return nil, err
	}
	payload, err := json.Marshal(owner)
	if err != nil {
		_ = unlockFile(file)
		_ = file.Close()
		return nil, err
	}
	payload = append(payload, '\n')
	if err := file.Truncate(0); err == nil {
		_, err = file.Seek(0, 0)
	}
	if err == nil {
		_, err = file.Write(payload)
	}
	if err == nil {
		err = file.Sync()
	}
	if err != nil {
		_ = unlockFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("write review store lock owner: %w", err)
	}
	return &storeLock{file: file, owner: owner}, nil
}

func (lock *storeLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unlockFile(lock.file)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func newStoreLockOwner() (storeLockOwner, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return storeLockOwner{}, fmt.Errorf("generate review store lock owner: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return storeLockOwner{
		Schema: storeLockSchema, OwnerID: hex.EncodeToString(token[:]), PID: os.Getpid(),
		Host: host, AcquiredAt: time.Now().UTC(),
	}, nil
}

func readStoreLockOwner(file *os.File) storeLockOwner {
	if _, err := file.Seek(0, 0); err != nil {
		return storeLockOwner{}
	}
	var owner storeLockOwner
	if err := json.NewDecoder(file).Decode(&owner); err != nil || owner.Schema != storeLockSchema {
		return storeLockOwner{}
	}
	return owner
}
