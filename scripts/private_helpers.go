// Package scripts exposes only the fixed private installation assets.
package scripts

import "embed"

//go:embed acquire-gentle-shell-private-bundle.sh bootstrap-gentle-shell-private-node.sh complete-generated-lock-sri.mjs install-gentle-shell-private.sh normalize-private-optional-platform-closure.mjs
var privateHelpers embed.FS

func PrivateHelperNames() []string {
	return []string{
		"acquire-gentle-shell-private-bundle.sh", "bootstrap-gentle-shell-private-node.sh",
		"complete-generated-lock-sri.mjs", "install-gentle-shell-private.sh",
		"normalize-private-optional-platform-closure.mjs",
	}
}

func ReadPrivateHelper(name string) ([]byte, error) {
	return privateHelpers.ReadFile(name)
}
