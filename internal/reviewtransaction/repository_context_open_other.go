//go:build !windows

package reviewtransaction

import "os"

func openReviewRepositoryContext(path string, openFile func(string) (*os.File, error)) (*os.File, error) {
	return openFile(path)
}
