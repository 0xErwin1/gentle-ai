//go:build windows

package reviewtransaction

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func openReviewRepositoryContext(path string, openFile func(string) (*os.File, error)) (*os.File, error) {
	for _, delay := range [...]time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond} {
		file, err := openFile(path)
		if err == nil || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return file, err
		}
		time.Sleep(delay)
	}
	return openFile(path)
}
