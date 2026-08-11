//go:build windows

package reviewtransaction

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestReadReviewRepositoryContextRetriesTransientWindowsSharingViolation(t *testing.T) {
	repo, binding := reviewRepositoryContextFixture(t, "repository-context-sharing-violation")
	handle, err := PublishReviewRepositoryContext(context.Background(), repo, binding)
	if err != nil {
		t.Fatal(err)
	}
	path, err := reviewRepositoryContextPath(handle)
	if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = windows.CloseHandle(lock)
		}
	}()

	started := make(chan struct{})
	results := make(chan error, 1)
	go func() {
		close(started)
		_, err := readReviewRepositoryContext(path)
		results <- err
	}()
	<-started
	select {
	case err := <-results:
		t.Fatalf("read returned while zero-share handle was held: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := windows.CloseHandle(lock); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err := <-results; err != nil {
		t.Fatalf("read after sharing violation: %v", err)
	}
}

func TestReadReviewRepositoryContextDoesNotRetryNonSharingOpenFailure(t *testing.T) {
	want := &os.PathError{Op: "open", Path: "repository-context.json", Err: windows.ERROR_ACCESS_DENIED}
	attempts := 0
	_, err := openReviewRepositoryContext(want.Path, func(string) (*os.File, error) {
		attempts++
		return nil, want
	})
	if err != want || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("open error = %v, want unchanged ERROR_ACCESS_DENIED", err)
	}
	if attempts != 1 {
		t.Fatalf("open attempts = %d, want 1", attempts)
	}
}
