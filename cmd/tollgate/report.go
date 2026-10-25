package main

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/usage"
)

// runReport shows the totals of the usage file, by group. It reads the
// file, so it works when Tollgate does not run.
func runReport(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("report", "report [-config path | -file path] [flags]", stderr)
	path := fs.String("config", defaultConfig, "Path of the configuration `file`. The report reads its usage file.")
	file := fs.String("file", "", "Path of a usage `file`. It replaces the usage file of the configuration.")
	groupBy := fs.String("group-by", usage.BySession, "Group the requests by `group`: "+strings.Join(usage.GroupByValues, ", ")+".")
	tenant := fs.String("tenant", "", "Show only the requests of this tenant.")
	session := fs.String("session", "", "Show only the requests of this session.")
	since := fs.String("since", "", "Show only the requests after this `time`: RFC 3339, or a duration before now, such as 24h.")
	format := fs.String("format", "table", "Output `format`: table or json.")
	if code := parseFlags(fs, args, stderr); code >= 0 {
		return code
	}
	if *format != "table" && *format != "json" {
		fmt.Fprintf(stderr, "The format %q is not known. Use table or json.\n", *format)
		return exitUsage
	}
	f := usage.Filter{TenantID: *tenant, SessionID: *session}
	if *since != "" {
		t, ok := parseSince(*since, time.Now())
		if !ok {
			fmt.Fprintf(stderr, "The -since value %q is not valid. Use RFC 3339, for example 2026-10-01T00:00:00Z, or a duration, for example 24h.\n", *since)
			return exitUsage
		}
		f.From = t
	}
	if *file == "" {
		cfg, ok := loadConfig(*path, stderr)
		if !ok {
			return exitError
		}
		if cfg.Usage.Sink != config.SinkJSONL {
			fmt.Fprintf(stderr, "The configuration file %s has no usage file (usage.sink is %s). Use -file.\n", *path, cfg.Usage.Sink)
			return exitError
		}
		*file = cfg.Usage.Path
	}
	var events []usage.Event
	_, bad, err := usage.ReadEvents(*file, func(e *usage.Event) {
		if f.Match(e) {
			events = append(events, *e)
		}
	})
	if err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot read the usage file %s: %v\n", *file, err)
		return exitError
	}
	groups, err := usage.Summarize(events, *groupBy)
	if err != nil {
		fmt.Fprintf(stderr, "The group %q is not known. Use %s.\n", *groupBy, strings.Join(usage.GroupByValues, ", "))
		return exitUsage
	}
	total, _ := usage.Summarize(events, usage.ByNone)
	if *format == "json" {
		out := struct {
			GroupBy string        `json:"group_by"`
			Groups  []usage.Group `json:"groups"`
			Total   *usage.Group  `json:"total"`
		}{*groupBy, groups, nil}
		if len(total) > 0 {
			out.Total = &total[0]
		}
		if err := json.MarshalWrite(stdout, out, json.Deterministic(true)); err != nil {
			fmt.Fprintf(stderr, "Tollgate cannot write the report: %v\n", err)
			return exitError
		}
		fmt.Fprintln(stdout)
		return exitOK
	}
	if len(events) == 0 {
		fmt.Fprintf(stdout, "The usage file %s has no requests that match.\n", *file)
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, strings.ToUpper(*groupBy)+"\tREQUESTS\tFALLBACKS\tSWITCHES\tINPUT\tCACHE READ\tCACHE WRITE\tOUTPUT\tCOST USD\tCACHE LOSS USD\t")
	row := func(key string, t usage.Totals) {
		if key == "" {
			key = "-"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t\n", key, t.Requests, t.Fallbacks, t.TargetSwitches,
			t.InputTokens, t.CacheReadTokens, t.CacheWriteTokens, t.OutputTokens, config.USD(t.CostMicroUSD), config.USD(t.CacheLossMicroUSD))
	}
	for _, g := range groups {
		row(g.Key, g.Totals)
	}
	if len(groups) > 1 {
		row("total", total[0].Totals)
	}
	if err := tw.Flush(); err != nil {
		return exitError
	}
	if bad > 0 {
		fmt.Fprintf(stderr, "The usage file has %d lines that are not usage events. The report does not include them.\n", bad)
	}
	return exitOK
}

// parseSince reads an RFC 3339 time or a duration before now.
func parseSince(v string, now time.Time) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(-d), true
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil && strings.HasSuffix(v, "d") && n > 0 {
		return now.AddDate(0, 0, -n), true
	}
	return time.Time{}, false
}
