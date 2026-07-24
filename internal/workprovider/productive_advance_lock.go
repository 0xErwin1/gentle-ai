package workprovider

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	productiveAdvanceOwnerLockTimeout = 30 * time.Second
	productiveAdvanceOwnerLockPoll    = 10 * time.Millisecond
)

// acquireProductiveAdvanceOwnerLock serializes the complete owner driver for
// one WorkRun. The immutable attempt marker makes retries resumable, while this
// kernel advisory lock prevents same-CAS contenders from racing through RAR
// state transitions before any one of them can publish the terminal cache.
func acquireProductiveAdvanceOwnerLock(
	ctx context.Context,
	store productiveAdvanceStore,
) (*reviewtransaction.AuthorityFileLock, error) {
	if err := store.validate(ctx); err != nil {
		return nil, err
	}
	path := filepath.Join(store.root, "ADVANCE-LOCK")
	timer := time.NewTimer(productiveAdvanceOwnerLockTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(productiveAdvanceOwnerLockPoll)
	defer ticker.Stop()
	for {
		lock, err := reviewtransaction.AcquireAuthorityFileLock(path)
		if err == nil {
			if validateErr := store.validate(ctx); validateErr != nil {
				_ = lock.Release()
				return nil, validateErr
			}
			return lock, nil
		}
		if !errors.Is(err, reviewtransaction.ErrConcurrentUpdate) {
			return nil, fmt.Errorf(
				"acquire productive advance owner lock: %w",
				err,
			)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf(
				"%w: productive advance owner lock",
				workrun.ErrWorkRunConcurrentUpdate,
			)
		case <-ticker.C:
		}
	}
}
