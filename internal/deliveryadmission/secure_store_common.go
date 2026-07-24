package deliveryadmission

import (
	"fmt"
	"path/filepath"
	"strings"
)

const maximumSecureRecordBytes = 4 << 20

// secureUseDirectoryComponents proves that an internal provider store starts
// below the shared Git common-dir/gentle-ai namespace. The first component is
// intentionally shared; every component after it is owner-private.
func secureUseDirectoryComponents(commonPath, directoryPath string) ([]string, error) {
	if !filepath.IsAbs(commonPath) || filepath.Clean(commonPath) != commonPath ||
		!filepath.IsAbs(directoryPath) || filepath.Clean(directoryPath) != directoryPath {
		return nil, fmt.Errorf("%w: non-canonical secure store path", ErrInvalid)
	}
	relative, err := filepath.Rel(commonPath, directoryPath)
	if err != nil {
		return nil, fmt.Errorf("relativize secure store path: %w", err)
	}
	components := strings.Split(relative, string(filepath.Separator))
	if len(components) < 3 || components[0] != "gentle-ai" {
		return nil, fmt.Errorf("%w: secure store must be below Git common-dir/gentle-ai", ErrInvalid)
	}
	for _, component := range components {
		if !validUseStoreComponent(component) {
			return nil, fmt.Errorf("%w: invalid secure store component", ErrInvalid)
		}
	}
	return components, nil
}

func validUseStoreComponent(value string) bool {
	return value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, `/\`) && filepath.Base(value) == value
}

func validUseRecordName(value string) bool {
	if len(value) != 64+len(".json") ||
		!strings.HasSuffix(value, ".json") || !validUseStoreComponent(value) {
		return false
	}
	digest := strings.TrimSuffix(value, ".json")
	for _, character := range digest {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return len(digest) == 64
}

// validSecureRecordName admits only fixed, package-generated flat records.
// Pending names are never accepted as publication or read targets.
func validSecureRecordName(value string) bool {
	if len(value) < 1 || len(value) > 192 || !validUseStoreComponent(value) ||
		strings.HasPrefix(value, ".") || strings.Contains(value, "..") ||
		(!strings.HasSuffix(value, ".json") && !strings.HasSuffix(value, ".lock")) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}
