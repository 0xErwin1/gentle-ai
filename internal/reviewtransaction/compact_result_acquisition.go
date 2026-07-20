package reviewtransaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const CompactResultAcquisitionSchema = "gentle-ai.review-result-acquisition/v1"

type CompactResultAcquisitionState string

const CompactResultAcquisitionUnknownOutcome CompactResultAcquisitionState = "unknown_outcome"

// CompactResultAcquisition is the immutable authority created before a reviewer
// task launches. Its existence consumes the binding's launch slot.
type CompactResultAcquisition struct {
	Schema         string                        `json:"schema"`
	ID             string                        `json:"id"`
	LineageID      string                        `json:"lineage_id"`
	TargetIdentity string                        `json:"target_identity"`
	Lens           string                        `json:"lens"`
	SelectedOrder  int                           `json:"selected_order"`
	State          CompactResultAcquisitionState `json:"state"`
	AcquiredAt     time.Time                     `json:"acquired_at"`
}

// AcquireReviewerResult validates a frozen reviewer binding and atomically
// records its indeterminate launch outcome while holding the store lock.
func (store CompactStore) AcquireReviewerResult(ctx context.Context, target, lens string, order int) (CompactResultAcquisition, error) {
	if err := ctx.Err(); err != nil {
		return CompactResultAcquisition{}, err
	}
	lock, err := acquireStoreLock(store.lockPath)
	if err != nil {
		return CompactResultAcquisition{}, err
	}
	defer lock.release()
	record, err := store.loadCompactRecordLocked()
	if err != nil {
		return CompactResultAcquisition{}, err
	}
	state := record.State
	if state.State != StateReviewing || state.InitialSnapshot.Identity != target || order < 0 || order >= len(state.SelectedLenses) || state.SelectedLenses[order] != lens {
		return CompactResultAcquisition{}, errors.New("acquisition binding does not match the current reviewing authority")
	}
	if _, err := os.Stat(filepath.Join(store.Dir, CompactReviewerResultsDir, fmt.Sprintf("%02d-%s.json", order, lens))); err == nil {
		return CompactResultAcquisition{}, errors.New("reviewer result is already captured for this binding")
	} else if !os.IsNotExist(err) {
		return CompactResultAcquisition{}, err
	}
	path := compactResultAcquisitionPath(store.Dir, order, lens)
	if _, err := os.Stat(path); err == nil {
		return CompactResultAcquisition{}, errors.New("reviewer result acquisition already exists for this binding")
	} else if !os.IsNotExist(err) {
		return CompactResultAcquisition{}, err
	}
	acquisition := CompactResultAcquisition{
		Schema: CompactResultAcquisitionSchema, LineageID: state.LineageID,
		TargetIdentity: target, Lens: lens, SelectedOrder: order,
		State: CompactResultAcquisitionUnknownOutcome, AcquiredAt: time.Now().UTC(),
	}
	acquisition.ID = compactResultAcquisitionID(acquisition)
	if err := writeCompactResultAcquisition(path, acquisition); err != nil {
		return CompactResultAcquisition{}, err
	}
	return acquisition, nil
}

// ReadResultAcquisition loads the acquisition record published for the exact
// (order, lens) slot and validates it against the exact bound lineage,
// target, lens, order, and ID. Acquisition is immutable once published, so
// this is a lock-free read: any field mismatch, a stale ID, or a missing
// record is refused identically, and terminal callers (capture-result and
// dispose-result) never trust a caller-supplied acquisition on its own.
func (store CompactStore) ReadResultAcquisition(lineage, target, lens string, order int, id string) (CompactResultAcquisition, error) {
	if strings.TrimSpace(id) == "" {
		return CompactResultAcquisition{}, errors.New("reviewer result acquisition ID is required")
	}
	payload, err := os.ReadFile(compactResultAcquisitionPath(store.Dir, order, lens))
	if err != nil {
		return CompactResultAcquisition{}, fmt.Errorf("read reviewer result acquisition: %w", err)
	}
	var acquisition CompactResultAcquisition
	if err := json.Unmarshal(payload, &acquisition); err != nil {
		return CompactResultAcquisition{}, fmt.Errorf("decode reviewer result acquisition: %w", err)
	}
	if acquisition.Schema != CompactResultAcquisitionSchema || acquisition.ID != id || acquisition.LineageID != lineage ||
		acquisition.TargetIdentity != target || acquisition.Lens != lens || acquisition.SelectedOrder != order {
		return CompactResultAcquisition{}, errors.New("reviewer result acquisition does not match the exact bound lineage, target, lens, and order")
	}
	return acquisition, nil
}

func compactResultAcquisitionPath(storeDir string, order int, lens string) string {
	return filepath.Join(storeDir, CompactReviewerAcquisitionsDir, fmt.Sprintf("%02d-%s.json", order, lens))
}

func compactResultAcquisitionID(acquisition CompactResultAcquisition) string {
	payload := CompactResultAcquisitionSchema + "\x00" + acquisition.LineageID + "\x00" + acquisition.TargetIdentity + "\x00" +
		acquisition.Lens + "\x00" + strconv.Itoa(acquisition.SelectedOrder) + "\x00" + acquisition.AcquiredAt.Format(time.RFC3339Nano)
	sum := sha256.Sum256([]byte(payload))
	return "acq-" + hex.EncodeToString(sum[:])
}

func writeCompactResultAcquisition(path string, acquisition CompactResultAcquisition) error {
	payload, err := json.MarshalIndent(acquisition, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".acquisition-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
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
	if err := PublishFileNoReplace(temp.Name(), path); err != nil {
		return fmt.Errorf("publish reviewer result acquisition: %w", err)
	}
	if err := SyncReviewDirectory(dir); err != nil {
		return &directorySyncError{path: path, cause: err}
	}
	return nil
}
