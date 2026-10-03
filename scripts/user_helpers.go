package scripts

import "embed"

// Kept separate from the five frozen private helpers: their bytes and inventory
// remain unchanged, including every existing private component test.
//go:embed provision-gentle-shell-private-global.mjs
var userHelpers embed.FS

func ReadUserHelper() ([]byte, error) {
	return userHelpers.ReadFile("provision-gentle-shell-private-global.mjs")
}
