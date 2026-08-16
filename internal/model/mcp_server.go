package model

// MCPServer is one MCP server in the semantic selection. Enabled is resolved
// here rather than left optional, because every consumer needs a decision.
type MCPServer struct {
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Enabled bool
}
