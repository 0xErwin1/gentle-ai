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
	padDeliveryOwnerLockTimeout = 2 * time.Second
	padDeliveryOwnerLockPoll    = 10 * time.Millisecond
)

// acquirePADDeliveryOwnerLock serializes every terminal delivery authority
// mutation for one WorkRun before its first PAD effect. Route reevaluation and
// authorization issuance deliberately share this lock.
func acquirePADDeliveryOwnerLock(
	ctx context.Context,
	workRunDirectory string,
) (*reviewtransaction.AuthorityFileLock, error) {
	path := filepath.Join(
		workRunDirectory,
		"delivery-owner",
		"LOCK",
	)
	timer := time.NewTimer(padDeliveryOwnerLockTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(padDeliveryOwnerLockPoll)
	defer ticker.Stop()
	for {
		lock, err := reviewtransaction.AcquireAuthorityFileLock(path)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, reviewtransaction.ErrConcurrentUpdate) {
			return nil, fmt.Errorf(
				"acquire PAD delivery owner lock: %w",
				err,
			)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf(
				"%w: PAD delivery owner lock",
				workrun.ErrWorkRunConcurrentUpdate,
			)
		case <-ticker.C:
		}
	}
}
