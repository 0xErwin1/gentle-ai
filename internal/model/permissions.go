package model

// Permissions are declared permission rules layered over the shipped defaults.
type Permissions struct {
	Allow []string
	Deny  []string
	Ask   []string
}
