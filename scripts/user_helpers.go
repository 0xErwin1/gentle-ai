package scripts

import "embed"

// Kept separate from the five frozen private helpers: their bytes and inventory
// remain unchanged, including every existing private component test.
//
//go:embed provision-gentle-shell-private-global.mjs user-locks/modern/package-lock.json user-locks/prior/package-lock.json
var userHelpers embed.FS

func ReadUserHelper() ([]byte, error) {
	return userHelpers.ReadFile("provision-gentle-shell-private-global.mjs")
}

// ReadUserAssets returns the provisioner and the two complete acquisition locks.
// Runtime paths stay distinct from the frozen private-helper inventory.
func ReadUserAssets() (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range []string{"provision-gentle-shell-private-global.mjs", "user-locks/modern/package-lock.json", "user-locks/prior/package-lock.json"} {
		data, err := userHelpers.ReadFile(name)
		if err != nil {
			return nil, err
		}
		if name == "provision-gentle-shell-private-global.mjs" {
			name = "provision.mjs"
		}
		files[name] = data
	}
	return files, nil
}
