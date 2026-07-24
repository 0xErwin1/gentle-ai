//go:build windows

package mutationintegrity

// Windows does not expose portable directory fsync through os.File.Sync.
// The immutable object itself is flushed before its hard-link publication.
func syncDirectory(string) error {
	return nil
}
