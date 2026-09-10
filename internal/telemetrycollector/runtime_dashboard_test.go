package telemetrycollector

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRuntimeDashboard(t *testing.T) {
	data, err := os.ReadFile("../../deploy/telemetry/grafana/dashboards/gentle-ai-usage.json")
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		UID      string
		Title    string
		Timezone string
		Refresh  string
		Time     struct{ From, To string }
		Panels   []struct {
			ID          int
			Title       string
			Type        string
			TimeFrom    string
			Description string
			GridPos     struct{ X, Y, W, H int }
			Datasource  struct{ UID string }
			Targets     []struct {
				QueryType, QueryText, RawQueryText string
				TimeColumns                        []string
			}
			FieldConfig map[string]any
			Options     map[string]any
		}
	}
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatal(err)
	}
	if dashboard.UID != "gentle-ai-usage" || dashboard.Title != "Gentle AI — Usage" {
		t.Fatalf("dashboard identity changed: %q %q", dashboard.UID, dashboard.Title)
	}
	if dashboard.Timezone != "browser" || dashboard.Refresh != "1m" || dashboard.Time.From != "now-7d" || dashboard.Time.To != "now" {
		t.Fatalf("dashboard defaults = timezone %q, refresh %q, range %q to %q", dashboard.Timezone, dashboard.Refresh, dashboard.Time.From, dashboard.Time.To)
	}

	wantPanels := []struct{ section, title, kind string }{
		{"Adoption", "Unique installs all-time", "stat"},
		{"Adoption", "Active installs yesterday", "stat"},
		{"Adoption", "Active installs in range", "stat"},
		{"Adoption", "New installs in range", "stat"},
		{"Adoption", "Heartbeats in range", "stat"},
		{"Adoption", "RDD adoption %", "stat"},
		{"Adoption", "npm downloads latest day", "stat"},
		{"Adoption", "GitHub release downloads", "stat"},
		{"Growth", "Daily active installs", "timeseries"},
		{"Growth", "Daily new installs", "timeseries"},
		{"Growth", "Cumulative unique installs", "timeseries"},
		{"Where Gentle AI runs", "Agent adoption", "barchart"},
		{"Where Gentle AI runs", "Component adoption", "barchart"},
		{"Where Gentle AI runs", "OS and architecture", "barchart"},
		{"Where Gentle AI runs", "Version adoption", "barchart"},
		{"Runtime usage", "Tokens processed", "stat"},
		{"Runtime usage", "Responses", "stat"},
		{"Runtime usage", "Deliveries", "stat"},
		{"Runtime usage", "Hosts reporting", "stat"},
		{"Runtime usage", "Usage by host, subagent, model and effort", "table"},
		{"Runtime usage", "Tokens processed by host", "barchart"},
		{"Runtime usage", "Responses by host", "barchart"},
		{"Runtime usage", "Tokens by model", "table"},
		{"Runtime usage", "Responses and tokens by selected effort", "table"},
		{"Runtime usage", "Tokens processed per hour by host (last 24h)", "timeseries"},
		{"Runtime usage", "Token coverage per field", "table"},
		{"Runtime usage", "Error observations by category", "barchart"},
		{"Runtime usage", "Measured duration by kind", "table"},
	}
	var gotPanels []struct{ section, title, kind string }
	section := ""
	seenIDs := map[int]bool{}
	for i, panel := range dashboard.Panels {
		if seenIDs[panel.ID] {
			t.Fatalf("duplicate panel id %d", panel.ID)
		}
		seenIDs[panel.ID] = true
		g := panel.GridPos
		for _, other := range dashboard.Panels[:i] {
			o := other.GridPos
			if g.X < o.X+o.W && o.X < g.X+g.W && g.Y < o.Y+o.H && o.Y < g.Y+g.H {
				t.Errorf("panels %d and %d overlap", panel.ID, other.ID)
			}
		}
		if panel.Type == "row" {
			section = panel.Title
			continue
		}
		gotPanels = append(gotPanels, struct{ section, title, kind string }{section, panel.Title, panel.Type})
		if panel.Description == "" {
			t.Errorf("panel %q has no description", panel.Title)
		}
		if panel.Datasource.UID != "gentle-telemetry-sqlite" || len(panel.Targets) != 1 {
			t.Errorf("panel %q has wrong datasource or target count", panel.Title)
			continue
		}
		target := panel.Targets[0]
		if target.QueryType != "table" || target.RawQueryText == "" || target.QueryText != target.RawQueryText {
			t.Errorf("panel %q must use one identical table queryText/rawQueryText pair", panel.Title)
		}
		if strings.Contains(target.RawQueryText, "received_at >= ${__from}") && !strings.Contains(target.RawQueryText, "received_at >= ${__from} * 1000000") {
			t.Errorf("panel %q does not convert the millisecond lower bound to nanoseconds", panel.Title)
		}
		if strings.Contains(target.RawQueryText, "received_at <= ${__to}") && !strings.Contains(target.RawQueryText, "received_at <= ${__to} * 1000000") {
			t.Errorf("panel %q does not convert the millisecond upper bound to nanoseconds", panel.Title)
		}
		if panel.Type == "timeseries" && !reflect.DeepEqual(target.TimeColumns, []string{"time"}) {
			t.Errorf("time-series panel %q does not identify its numeric time column", panel.Title)
		}
		defaults, ok := panel.FieldConfig["defaults"].(map[string]any)
		if !ok {
			t.Errorf("panel %q has no field defaults", panel.Title)
		} else {
			wantUnit := "short"
			if panel.Type == "stat" && panel.Title != "RDD adoption %" {
				wantUnit = "locale"
			} else if panel.Title == "RDD adoption %" {
				wantUnit = "percent"
			}
			if defaults["unit"] != wantUnit {
				t.Errorf("panel %q unit = %v; want %q", panel.Title, defaults["unit"], wantUnit)
			}
		}
		configJSON, err := json.Marshal(panel.FieldConfig)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(configJSON), `"axisPlacement":"right"`) {
			t.Errorf("panel %q introduces a second y-axis", panel.Title)
		}
		if panel.Type == "stat" {
			if panel.Options["textMode"] != "value" || panel.Options["colorMode"] != "value" || panel.Options["graphMode"] != "none" {
				t.Errorf("stat panel %q does not use exact-value neutral presentation", panel.Title)
			}
			if !strings.Contains(string(configJSON), `"color":{"fixedColor":"#73BF69","mode":"fixed"}`) || strings.Contains(string(configJSON), `"thresholds"`) {
				t.Errorf("stat panel %q does not use one fixed color without thresholds", panel.Title)
			}
		}
		if panel.Title == "Tokens processed by host" || panel.Title == "Responses by host" {
			if panel.Options["xField"] != "Host" || panel.Options["colorByField"] != "Host" {
				t.Errorf("host bar panel %q must use Host as its category and color field", panel.Title)
			}
			if !strings.Contains(target.RawQueryText, "CAST(") || !strings.Contains(target.RawQueryText, " AS TEXT) AS Host") || !strings.Contains(target.RawQueryText, "GROUP BY Host") {
				t.Errorf("host bar panel %q must return long-form text host rows", panel.Title)
			}
			for host, color := range map[string]string{"pi": "#73BF69", "opencode": "#5794F2", "claude-code": "#FF9830", "codex": "#B877D9"} {
				if !strings.Contains(string(configJSON), `"`+host+`":{"color":"`+color+`"`) {
					t.Errorf("host bar panel %q does not map %s to its fixed color", panel.Title, host)
				}
			}
		}
		if panel.Title == "Tokens processed per hour by host (last 24h)" {
			if panel.TimeFrom != "24h" {
				t.Error("runtime token trend must override its range to the last 24 hours")
			}
			for _, host := range []string{"pi", "opencode", "claude-code", "codex"} {
				if !strings.Contains(string(configJSON), `"options":"`+host+`"`) {
					t.Errorf("runtime token trend does not pin the %s series color", host)
				}
			}
		}
		if panel.Type == "table" {
			footer, ok := panel.Options["footer"].(map[string]any)
			if !ok || footer["enablePagination"] != false {
				t.Errorf("table panel %q must scroll without pagination", panel.Title)
			}
		}
		if wantHeight, ok := map[string]int{
			"Usage by host, subagent, model and effort": 14,
			"Tokens by model":                         10,
			"Responses and tokens by selected effort": 10,
			"Token coverage per field":                11,
		}[panel.Title]; ok && panel.GridPos.H < wantHeight {
			t.Errorf("table panel %q height = %d; want at least %d", panel.Title, panel.GridPos.H, wantHeight)
		}
		if panel.Title == "Usage by host, subagent, model and effort" {
			if panel.GridPos.X != 0 || panel.GridPos.Y != 32 || panel.GridPos.W != 24 || !strings.Contains(panel.Description, "which subagent used which model at which effort and how many tokens") {
				t.Error("usage table must be the full-width runtime centerpiece directly below the stat tiles")
			}
		}
		if panel.Title == "Version adoption" {
			for _, fragment := range []string{"instr(key, '-')", "substr(key, 1, instr(key, '-') - 1)", "' (main)'", "GROUP BY Version", "LIMIT 10"} {
				if !strings.Contains(target.RawQueryText, fragment) {
					t.Errorf("version normalization query missing %q", fragment)
				}
			}
		}
		if panel.Title == "Agent adoption" || panel.Title == "Component adoption" || panel.Title == "OS and architecture" || panel.Title == "Version adoption" {
			if panel.Options["showValue"] != "always" || !strings.Contains(string(configJSON), `"fixedColor":"#8AB8A8"`) {
				t.Errorf("categorical adoption panel %q must show values with one neutral color", panel.Title)
			}
		}
		if panel.Title == "Measured duration by kind" && (!strings.Contains(string(configJSON), `"options":"Average ms"`) || !strings.Contains(string(configJSON), `"value":"ms"`)) {
			t.Error("duration table must format only its average column as milliseconds")
		}
	}
	if !reflect.DeepEqual(gotPanels, wantPanels) {
		t.Fatalf("panels = %#v; want %#v", gotPanels, wantPanels)
	}

	s, err := OpenStorage(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	to := time.Date(2026, 1, 3, 23, 59, 59, 0, time.UTC).UnixMilli()
	for _, event := range []struct {
		received                                             int64
		kind, install, version, os, arch, agents, components string
		rdd                                                  int
	}{
		{from * 1000000, "install", "install-a", "1.0.0", "linux", "amd64", `["codex"]`, `["sdd","skills"]`, 1},
		{time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC).UnixNano(), "heartbeat", "install-a", "1.0.0", "linux", "amd64", `["codex"]`, `["sdd","skills"]`, 1},
		{time.Date(2026, 1, 2, 13, 0, 0, 0, time.UTC).UnixNano(), "install", "install-b", "1.1.0", "darwin", "arm64", `["pi"]`, `["engram"]`, 0},
		{to * 1000000, "heartbeat", "install-b", "1.1.0", "darwin", "arm64", `["pi"]`, `["engram"]`, 0},
	} {
		if _, err := s.db.Exec(`INSERT INTO events(received_at,event,install_id,version,os,arch,agents_json,components_json,rdd_enabled,counters_json) VALUES(?,?,?,?,?,?,?,?,?,NULL)`, event.received, event.kind, event.install, event.version, event.os, event.arch, event.agents, event.components, event.rdd); err != nil {
			t.Fatal(err)
		}
	}
	for _, rollup := range []struct {
		day, metric, key string
		value            int
	}{
		{"2026-01-01", "active_install", "install-a", 1},
		{"2026-01-02", "active_install", "install-a", 1},
		{"2026-01-02", "active_install", "install-b", 1},
		{"2026-01-02", "agent", "codex", 1},
		{"2026-01-02", "agent", "pi", 1},
		{"2026-01-02", "component", "sdd", 1},
		{"2026-01-02", "component", "engram", 1},
		{"2026-01-02", "rdd_enabled", "true", 1},
		{"2026-01-02", "rdd_enabled", "false", 1},
		{"2026-01-02", "version", "1.0.0", 1},
		{"2026-01-02", "version", "1.1.0", 1},
		{"2026-01-02", "version", "1.1.0-0.20260102", 1},
		{"2026-01-02", "version", "1.1.0-20260102", 2},
		{"2026-01-02", "npm_downloads_day", "gentle-pi", 100},
		{"2026-01-02", "npm_downloads_day", "gentle-engram", 200},
		{"2026-01-02", "github_release_downloads_total", "v1.0.0", 10},
		{"2026-01-03", "github_release_downloads_total", "v1.0.0", 20},
		{"2026-01-03", "github_release_downloads_total", "v1.1.0", 10},
	} {
		if _, err := s.db.Exec(`INSERT INTO rollups_daily(day,metric,key,value) VALUES(?,?,?,?)`, rollup.day, rollup.metric, rollup.key, rollup.value); err != nil {
			t.Fatal(err)
		}
	}

	token := func(sum int, reported, unavailable, unsupported int) map[string]int {
		return map[string]int{"sum": sum, "reported": reported, "unavailable": unavailable, "unsupported": unsupported}
	}
	runtimeFixtures := []struct {
		id, host string
		received int64
		row      map[string]any
	}{
		{"delivery-codex", "codex", time.Date(2026, 1, 2, 10, 15, 0, 0, time.UTC).UnixNano(), map[string]any{
			"model": map[string]string{"provider": "openai", "id": "gpt-5"}, "model_evidence": "response", "agent_kind": "orchestrator", "agent_class": "orchestrator", "selected_effort": "high", "effective_effort": "high", "launches": 1, "responses": 3,
			"input_tokens": token(10, 1, 0, 0), "output_tokens": token(5, 1, 0, 0), "cache_read_tokens": token(2, 1, 0, 0), "cache_creation_tokens": token(1, 1, 0, 0), "reasoning_tokens": token(3, 1, 0, 0), "total_tokens": token(999, 1, 0, 0),
			"error_category": "none", "duration": map[string]any{"kind": "request", "measured_count": 2, "sum_ms": 50.0},
		}},
		{"delivery-opencode", "opencode", time.Date(2026, 1, 2, 11, 30, 0, 0, time.UTC).UnixNano(), map[string]any{
			"model": map[string]string{"provider": "custom", "id": "custom"}, "model_evidence": "unknown", "agent_kind": "worker", "agent_class": "unknown", "selected_effort": "unknown", "effective_effort": "unknown", "launches": 2, "responses": 2,
			"input_tokens": token(7, 1, 0, 0), "output_tokens": token(4, 1, 0, 0), "cache_read_tokens": token(99, 0, 1, 0), "cache_creation_tokens": token(99, 0, 0, 1), "reasoning_tokens": token(99, 0, 1, 0), "total_tokens": token(999, 0, 1, 0),
			"error_category": "provider", "duration": map[string]any{"kind": "message", "measured_count": 1, "sum_ms": 40.0},
		}},
	}
	for _, fixture := range runtimeFixtures {
		payload, err := json.Marshal(map[string]string{"host": fixture.host})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(fixture.row)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO runtime_deliveries(delivery_id,received_at,canonical_payload) VALUES(?,?,?)`, fixture.id, fixture.received, string(payload)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO runtime_rows(delivery_id,ordinal,row_json) VALUES(?,0,?)`, fixture.id, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	unavailableDuration, err := json.Marshal(map[string]any{
		"model": map[string]string{"provider": "openai", "id": "gpt-5"}, "model_evidence": "response", "agent_kind": "orchestrator", "agent_class": "orchestrator", "selected_effort": "high", "effective_effort": "high", "launches": 0, "responses": 0,
		"input_tokens": token(0, 0, 1, 0), "output_tokens": token(0, 0, 1, 0), "cache_read_tokens": token(0, 0, 1, 0), "cache_creation_tokens": token(0, 0, 1, 0), "reasoning_tokens": token(0, 0, 1, 0), "total_tokens": token(0, 0, 1, 0),
		"error_category": "none", "duration": map[string]any{"kind": "unavailable", "measured_count": 0, "sum_ms": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO runtime_rows(delivery_id,ordinal,row_json) VALUES('delivery-codex',1,?)`, string(unavailableDuration)); err != nil {
		t.Fatal(err)
	}

	queries := map[string]string{}
	for _, panel := range dashboard.Panels {
		if panel.Type == "row" {
			continue
		}
		query := strings.NewReplacer("${__from}", fmt.Sprint(from), "${__to}", fmt.Sprint(to)).Replace(panel.Targets[0].RawQueryText)
		queries[panel.Title] = query
		t.Run("SQL/"+panel.Title, func(t *testing.T) {
			rows, err := s.db.Query(query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatalf("synthetic fixture did not exercise query: %v", rows.Err())
			}
		})
	}

	assertSingleNumber := func(title string, want float64) {
		t.Helper()
		var got float64
		if err := s.db.QueryRow(queries[title]).Scan(&got); err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		if got != want {
			t.Fatalf("%s = %v; want %v", title, got, want)
		}
	}
	assertSingleNumber("Unique installs all-time", 2)
	assertSingleNumber("Active installs yesterday", 2)
	assertSingleNumber("Active installs in range", 2)
	assertSingleNumber("New installs in range", 2)
	assertSingleNumber("Heartbeats in range", 2)
	assertSingleNumber("RDD adoption %", 50)
	assertSingleNumber("npm downloads latest day", 300)
	assertSingleNumber("GitHub release downloads", 30)
	assertSingleNumber("Tokens processed", 32)
	assertSingleNumber("Responses", 5)
	assertSingleNumber("Deliveries", 2)
	assertSingleNumber("Hosts reporting", 2)

	for _, tc := range []struct {
		title string
		want  [][]any
	}{
		{"Tokens processed by host", [][]any{{"codex", int64(21)}, {"opencode", int64(11)}}},
		{"Responses by host", [][]any{{"codex", int64(3)}, {"opencode", int64(2)}}},
	} {
		t.Run(tc.title+" long format", func(t *testing.T) {
			rows, err := s.db.Query(queries[tc.title])
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if columns, err := rows.Columns(); err != nil || !reflect.DeepEqual(columns, []string{"Host", strings.TrimSuffix(tc.title, " by host")}) {
				t.Fatalf("columns = %v, err = %v", columns, err)
			}
			var got [][]any
			for rows.Next() {
				var host string
				var measure int64
				if err := rows.Scan(&host, &measure); err != nil {
					t.Fatal(err)
				}
				got = append(got, []any{host, measure})
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rows = %#v; want %#v", got, tc.want)
			}
		})
	}

	versionRows, err := s.db.Query(queries["Version adoption"])
	if err != nil {
		t.Fatal(err)
	}
	defer versionRows.Close()
	gotVersions := map[string]int64{}
	for versionRows.Next() {
		var version string
		var installs int64
		if err := versionRows.Scan(&version, &installs); err != nil {
			t.Fatal(err)
		}
		gotVersions[version] = installs
	}
	wantVersions := map[string]int64{"1.0.0": 1, "1.1.0": 1, "1.1.0 (main)": 3}
	if !reflect.DeepEqual(gotVersions, wantVersions) {
		t.Fatalf("versions = %#v; want %#v", gotVersions, wantVersions)
	}

	durationRows, err := s.db.Query(queries["Measured duration by kind"])
	if err != nil {
		t.Fatal(err)
	}
	defer durationRows.Close()
	var gotDurations [][]any
	for durationRows.Next() {
		var kind string
		var measured int64
		var average float64
		if err := durationRows.Scan(&kind, &measured, &average); err != nil {
			t.Fatal(err)
		}
		gotDurations = append(gotDurations, []any{kind, measured, average})
	}
	wantDurations := [][]any{{"request", int64(2), float64(25)}, {"message", int64(1), float64(40)}}
	if !reflect.DeepEqual(gotDurations, wantDurations) {
		t.Fatalf("duration rows = %#v; want %#v", gotDurations, wantDurations)
	}
	if strings.Contains(queries["Measured duration by kind"], "= 'request'") || !strings.Contains(queries["Measured duration by kind"], "<> 'unavailable'") {
		t.Fatal("duration panel must include every measured kind and exclude unavailable rows")
	}

	usageRows, err := s.db.Query(queries["Usage by host, subagent, model and effort"])
	if err != nil {
		t.Fatal(err)
	}
	defer usageRows.Close()
	var gotUsage [][]any
	for usageRows.Next() {
		values, pointers := make([]any, 9), make([]any, 9)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := usageRows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		gotUsage = append(gotUsage, values)
	}
	wantUsage := [][]any{
		{"codex", "orchestrator", "orchestrator", "openai/gpt-5", "high", "high", int64(1), int64(3), int64(21)},
		{"opencode", "worker", "unknown", "custom/custom", "unknown", "unknown", int64(2), int64(2), int64(11)},
	}
	if !reflect.DeepEqual(gotUsage, wantUsage) {
		t.Fatalf("usage rows = %#v; want %#v", gotUsage, wantUsage)
	}
	if strings.Contains(queries["Tokens processed"], "$.total_tokens.sum") {
		t.Fatal("primary token measure must be derived from token categories, not total_tokens")
	}
	if !strings.Contains(queries["Token coverage per field"], "$.total_tokens.reported") {
		t.Fatal("coverage must retain explicitly reported total_tokens")
	}
}

func TestRuntimeDashboardEpochBounds(t *testing.T) {
	s, err := OpenStorage(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Largest whole millisecond representable in signed Unix nanoseconds.
	for _, ms := range []int64{0, 1767312000000, 9223372036854} {
		var kind string
		var ns int64
		if err := s.db.QueryRow(`SELECT typeof(? * 1000000), ? * 1000000`, ms, ms).Scan(&kind, &ns); err != nil {
			t.Fatal(err)
		}
		if kind != "integer" || ns != ms*1000000 {
			t.Fatalf("epoch conversion overflow: %d => %s %d", ms, kind, ns)
		}
	}
}
