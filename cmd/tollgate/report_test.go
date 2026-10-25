package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const reportFile = `{"request_id":"1","ts":"2026-10-20T10:00:00Z","tenant_id":"t","key_id":"k","session_id":"s1","route_model":"m","provider":"p","provider_model":"x","status":200,"attempts":1,"cache":"bypass","input_tokens":10,"output_tokens":5,"cache_read_tokens":100,"cache_write_tokens":0,"cache_write_1h_tokens":0,"reasoning_tokens":0,"usage_estimated":false,"cost_micro_usd":1500,"ttft_ms":20,"duration_ms":30,"overhead_us":40}
{"request_id":"2","ts":"2026-10-20T10:01:00Z","tenant_id":"t","key_id":"k","session_id":"s1","route_model":"m","provider":"q","provider_model":"y","status":200,"attempts":2,"cache":"bypass","input_tokens":10,"output_tokens":5,"cache_read_tokens":0,"cache_write_tokens":110,"cache_write_1h_tokens":0,"reasoning_tokens":0,"usage_estimated":false,"cost_micro_usd":2500,"ttft_ms":20,"duration_ms":30,"overhead_us":40,"target_switched":true,"cache_loss_micro_usd":900}
{"request_id":"3","ts":"2026-10-21T10:00:00Z","tenant_id":"u","key_id":"k","session_id":"s2","route_model":"m","provider":"p","provider_model":"x","status":200,"attempts":1,"cache":"bypass","input_tokens":1,"output_tokens":1,"cache_read_tokens":0,"cache_write_tokens":0,"cache_write_1h_tokens":0,"reasoning_tokens":0,"usage_estimated":false,"cost_micro_usd":7,"ttft_ms":null,"duration_ms":30,"overhead_us":40}
not an event
`

func TestReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	if err := os.WriteFile(path, []byte(reportFile), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCmd("report", "-file", path)
	if code != exitOK || !strings.Contains(stderr, "1 lines that are not usage events") {
		t.Fatalf("code %d, stderr %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 4 || !strings.Contains(lines[0], "SESSION") || strings.Join(strings.Fields(lines[1]), " ") != "t/s1 2 1 1 20 100 110 10 0.004 0.0009" || !strings.Contains(lines[3], "total") {
		t.Errorf("report:\n%s", stdout)
	}
	code, stdout, _ = runCmd("report", "-file", path, "-tenant", "t", "-group-by", "provider", "-format", "json")
	if code != exitOK || !strings.Contains(stdout, `"key":"q/y"`) || !strings.Contains(stdout, `"cache_loss_micro_usd":900`) || strings.Contains(stdout, `"u"`) {
		t.Errorf("json report: %s", stdout)
	}
	code, stdout, _ = runCmd("report", "-file", path, "-since", "2026-10-21T00:00:00Z", "-group-by", "none")
	if code != exitOK || strings.Contains(stdout, "total") || !strings.Contains(stdout, "0.000007") {
		t.Errorf("report since: %s", stdout)
	}
	for _, args := range [][]string{
		{"-file", path, "-group-by", "week"},
		{"-file", path, "-format", "xml"},
		{"-file", path, "-since", "yesterday"},
	} {
		if code, _, stderr := runCmd(append([]string{"report"}, args...)...); code != exitUsage {
			t.Errorf("report %v: code %d, stderr %s", args, code, stderr)
		}
	}
	if code, stdout, _ := runCmd("report", "-file", filepath.Join(t.TempDir(), "none.jsonl")); code != exitOK || !strings.Contains(stdout, "has no requests") {
		t.Errorf("missing file: %d %s", code, stdout)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Time{
		"2026-10-01T00:00:00Z": time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		"90m":                  now.Add(-90 * time.Minute),
		"7d":                   now.AddDate(0, 0, -7),
	} {
		if got, ok := parseSince(v, now); !ok || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "-1h", "0d", "soon"} {
		if _, ok := parseSince(v, now); ok {
			t.Errorf("parseSince(%q) accepted the value", v)
		}
	}
}
