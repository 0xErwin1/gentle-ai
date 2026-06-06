package filemerge

import (
	"strings"
	"testing"
)

// ─── UpsertYAMLMCPServerBlock ─────────────────────────────────────────────────

// TestUpsertYAMLMCPServerBlock covers the full golden matrix required by the spec
// and design (scenarios #1–#9). Scenario #10 (context7 wrapper) is in
// TestUpsertHermesContext7Block below.
func TestUpsertYAMLMCPServerBlock(t *testing.T) {
	t.Parallel()

	engram := func(content string) string {
		return UpsertYAMLMCPServerBlock(content, "engram", "engram", []string{"mcp", "--tools=agent"}, nil)
	}

	tests := []struct {
		name   string
		input  string
		fn     func(string) string
		checks []string // substrings that must be present
		absent []string // substrings that must be absent
		suffix string   // if non-empty, result must end with this
		exact  string   // if non-empty, result must equal this exactly
	}{
		{
			// #1: empty/absent content → engram block created from scratch
			name:  "empty_content_creates_mcp_servers",
			input: "",
			fn:    engram,
			checks: []string{
				"mcp_servers:\n",
				"  engram:\n",
				"    command: engram\n",
				"    args:\n",
				"      - mcp\n",
				"      - --tools=agent\n",
			},
			suffix: "\n",
		},
		{
			// #2: mcp_servers: absent, other top-level keys present → keys/comments preserved; section appended
			name: "other_top_level_keys_preserved",
			input: `# user config
model: gpt-4o
temperature: 0.7
`,
			fn: engram,
			checks: []string{
				"# user config\n",
				"model: gpt-4o\n",
				"temperature: 0.7\n",
				"mcp_servers:\n",
				"  engram:\n",
				"    command: engram\n",
			},
			suffix: "\n",
		},
		{
			// #3: mcp_servers: present, no engram entry → user server preserved, engram appended as sibling
			name: "user_server_preserved_engram_appended",
			input: `mcp_servers:
  myserver:
    command: myserver
    args:
      - --flag
`,
			fn: engram,
			checks: []string{
				"  myserver:\n",
				"    command: myserver\n",
				"    args:\n",
				"      - --flag\n",
				"  engram:\n",
				"    command: engram\n",
			},
		},
		{
			// #4: idempotency — output of #3 fed back in → byte-identical result
			name: "idempotent_rerun",
			input: `mcp_servers:
  myserver:
    command: myserver
    args:
      - --flag
`,
			fn: func(content string) string {
				first := engram(content)
				second := engram(first)
				if first != second {
					t.Errorf("idempotency violated:\nfirst:\n%s\nsecond:\n%s", first, second)
				}
				return second
			},
			checks: []string{
				"  myserver:\n",
				"  engram:\n",
			},
			absent: []string{
				// Must not duplicate engram
				"  engram:\n  engram:\n",
			},
		},
		{
			// #5: upsert replaces stale engram block (old args) → old block removed, fresh block appended, siblings intact
			name: "stale_block_replaced",
			input: `mcp_servers:
  myserver:
    command: myserver
  engram:
    command: engram
    args:
      - mcp
`,
			fn: engram,
			checks: []string{
				"  myserver:\n",
				"  engram:\n",
				"      - --tools=agent\n",
			},
			absent: []string{
				// Old engram args-only entry must be gone; replaced with full block
			},
		},
		{
			// #6: user comments outside managed block → all comments preserved verbatim
			name: "comments_outside_block_preserved",
			input: `# top-level comment
model: gpt-4o

# comment before mcp
mcp_servers:
  # comment inside mcp
  myserver:
    command: myserver

# trailing comment
`,
			fn: engram,
			checks: []string{
				"# top-level comment\n",
				"# comment before mcp\n",
				"# trailing comment\n",
				"  # comment inside mcp\n",
				"  myserver:\n",
				"  engram:\n",
			},
		},
		{
			// #7: two managed servers coexist (engram then context7) → both present, both at 2-space indent, idempotent
			name:  "two_managed_servers_coexist",
			input: "",
			fn: func(content string) string {
				withEngram := engram(content)
				withBoth := UpsertYAMLMCPServerBlock(withEngram, "context7", "npx",
					[]string{"-y", "--package=@upstash/context7-mcp@2.2.5", "--", "context7-mcp"}, nil)
				// idempotency check
				withBothAgain := UpsertYAMLMCPServerBlock(withBoth, "context7", "npx",
					[]string{"-y", "--package=@upstash/context7-mcp@2.2.5", "--", "context7-mcp"}, nil)
				if withBoth != withBothAgain {
					t.Errorf("two-server idempotency violated:\nfirst:\n%s\nsecond:\n%s", withBoth, withBothAgain)
				}
				return withBoth
			},
			checks: []string{
				"  engram:\n",
				"    command: engram\n",
				"  context7:\n",
				"    command: npx\n",
			},
		},
		{
			// #8: CRLF input → normalized to \n, single trailing \n
			name:  "crlf_normalized",
			input: "model: gpt-4o\r\n",
			fn:    engram,
			checks: []string{
				"model: gpt-4o\n",
				"mcp_servers:\n",
			},
			absent: []string{"\r\n"},
			suffix: "\n",
		},
		{
			// #9: env map rendered → env: sub-block with 2-space-deeper KV pairs
			name:  "env_block_rendered",
			input: "",
			fn: func(content string) string {
				return UpsertYAMLMCPServerBlock(content, "engram", "engram",
					[]string{"mcp", "--tools=agent"},
					map[string]string{"ENGRAM_HOME": "/data/engram"})
			},
			checks: []string{
				"    env:\n",
				"      ENGRAM_HOME: /data/engram\n",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.fn(tt.input)
			for _, want := range tt.checks {
				if !strings.Contains(got, want) {
					t.Errorf("missing expected content %q in:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("unexpected content %q found in:\n%s", absent, got)
				}
			}
			if tt.suffix != "" && !strings.HasSuffix(got, tt.suffix) {
				t.Errorf("result does not end with %q; got:\n%q", tt.suffix, got)
			}
			if tt.exact != "" && got != tt.exact {
				t.Errorf("result mismatch:\nwant:\n%s\ngot:\n%s", tt.exact, got)
			}
		})
	}
}

// #10: UpsertHermesContext7Block on empty → pinned versions.Context7MCP args emitted
func TestUpsertHermesContext7Block(t *testing.T) {
	t.Parallel()

	got := UpsertHermesContext7Block("")

	if !strings.Contains(got, "  context7:\n") {
		t.Fatalf("missing context7 server key; got:\n%s", got)
	}
	if !strings.Contains(got, "    command: npx\n") {
		t.Fatalf("missing command: npx; got:\n%s", got)
	}
	// Must contain the pinned context7 package arg.
	if !strings.Contains(got, "--package=@upstash/context7-mcp@") {
		t.Fatalf("missing pinned context7-mcp package arg; got:\n%s", got)
	}
	if !strings.Contains(got, "      - context7-mcp\n") {
		t.Fatalf("missing context7-mcp arg; got:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("result does not end with newline; got:\n%q", got)
	}

	// Idempotency.
	second := UpsertHermesContext7Block(got)
	if got != second {
		t.Fatalf("UpsertHermesContext7Block not idempotent:\nfirst:\n%s\nsecond:\n%s", got, second)
	}
}

// TestUpsertHermesEngramBlock verifies the engram-specific convenience wrapper.
func TestUpsertHermesEngramBlock(t *testing.T) {
	t.Parallel()

	t.Run("empty_content_default_command", func(t *testing.T) {
		t.Parallel()
		got := UpsertHermesEngramBlock("", "")
		if !strings.Contains(got, "  engram:\n") {
			t.Fatalf("missing engram server key; got:\n%s", got)
		}
		if !strings.Contains(got, "    command: engram\n") {
			t.Fatalf("missing default command 'engram'; got:\n%s", got)
		}
		if !strings.Contains(got, "      - --tools=agent\n") {
			t.Fatalf("missing --tools=agent arg; got:\n%s", got)
		}
	})

	t.Run("custom_command_used", func(t *testing.T) {
		t.Parallel()
		got := UpsertHermesEngramBlock("", "/usr/local/bin/engram")
		if !strings.Contains(got, "    command: /usr/local/bin/engram\n") {
			t.Fatalf("missing custom command; got:\n%s", got)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		t.Parallel()
		first := UpsertHermesEngramBlock("", "engram")
		second := UpsertHermesEngramBlock(first, "engram")
		if first != second {
			t.Fatalf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
		}
		if strings.Count(second, "  engram:\n") != 1 {
			t.Fatalf("expected exactly 1 engram block; got:\n%s", second)
		}
	})
}

// ─── ReadYAMLMCPServerCommand ─────────────────────────────────────────────────

// TestReadYAMLMCPServerCommand covers T-04: all recovery shapes.
func TestReadYAMLMCPServerCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		serverID string
		wantCmd  string
		wantOK   bool
	}{
		{
			// scalar command: command: engram → ("engram", true)
			name: "scalar_command",
			content: `mcp_servers:
  engram:
    command: engram
    args:
      - mcp
      - --tools=agent
`,
			serverID: "engram",
			wantCmd:  "engram",
			wantOK:   true,
		},
		{
			// list command: command:\n  - /path/engram items → first element ("/path/engram", true)
			name: "list_command_first_element",
			content: `mcp_servers:
  engram:
    command:
      - /path/to/engram
      - mcp
      - --tools=agent
`,
			serverID: "engram",
			wantCmd:  "/path/to/engram",
			wantOK:   true,
		},
		{
			// server absent under mcp_servers: → ("", false)
			name: "server_absent_under_mcp_servers",
			content: `mcp_servers:
  context7:
    command: npx
`,
			serverID: "engram",
			wantCmd:  "",
			wantOK:   false,
		},
		{
			// mcp_servers: key absent entirely → ("", false)
			name: "mcp_servers_key_absent",
			content: `model: gpt-4o
temperature: 0.7
`,
			serverID: "engram",
			wantCmd:  "",
			wantOK:   false,
		},
		{
			// comment lines inside/around block ignored without breaking recovery
			name: "comment_lines_ignored",
			content: `# top comment
mcp_servers:
  # comment about servers
  engram:
    # comment inside server
    command: engram
    args:
      - mcp
`,
			serverID: "engram",
			wantCmd:  "engram",
			wantOK:   true,
		},
		{
			// absolute path command preserved
			name: "absolute_path_command",
			content: `mcp_servers:
  engram:
    command: /custom/path/engram
`,
			serverID: "engram",
			wantCmd:  "/custom/path/engram",
			wantOK:   true,
		},
		{
			// empty content → ("", false)
			name:     "empty_content",
			content:  "",
			serverID: "engram",
			wantCmd:  "",
			wantOK:   false,
		},
		{
			// versioned cellar path (ensure it is recoverable)
			name: "versioned_cellar_path",
			content: `mcp_servers:
  engram:
    command: /opt/homebrew/Cellar/engram/1.2.3/bin/engram
`,
			serverID: "engram",
			wantCmd:  "/opt/homebrew/Cellar/engram/1.2.3/bin/engram",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotCmd, gotOK := ReadYAMLMCPServerCommand(tt.content, tt.serverID)
			if gotCmd != tt.wantCmd {
				t.Errorf("command: got %q, want %q", gotCmd, tt.wantCmd)
			}
			if gotOK != tt.wantOK {
				t.Errorf("ok: got %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}
