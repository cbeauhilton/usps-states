package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The claim this repository makes about stability — that the code set has not
// moved in fourteen years — is only worth making if it is measured here rather
// than asserted from somewhere else. `usps-states history` measures it.
//
// Two traps, both of which produce a confident wrong answer:
//
//   - The CDX digest is useless for this. The page carries navigation, banners
//     and analytics that change constantly, so it has well over a hundred
//     distinct digests across the period while the codes never moved. Extract
//     and compare the CONCEPTS, never the bytes.
//
//   - The Archive returns its own "Temporarily Offline" page with a 200. That
//     parses as zero codes and reads as a real absence. A snapshot that yields
//     no concepts is recorded as unusable, never as evidence of change.
const cdxAPI = "http://web.archive.org/cdx/search/cdx"

type Snapshot struct {
	Timestamp string `json:"timestamp"`
	Date      string `json:"date"`
	Digest    string `json:"cdx_digest"`
	Bytes     int    `json:"bytes"`
	Concepts  int    `json:"concepts"`
	Changes   string `json:"changes,omitempty"`  // vs the previous usable snapshot
	Unusable  string `json:"unusable,omitempty"` // why this one proves nothing
}

type History struct {
	URL        string     `json:"url"`
	MeasuredAt string     `json:"measured_at"`
	Captures   int        `json:"archive_captures_total"`
	Usable     int        `json:"snapshots_usable"`
	Changes    int        `json:"code_set_changes"`
	Span       string     `json:"span"`
	Snapshots  []Snapshot `json:"snapshots"`
}

func history() error {
	fmt.Printf("querying the Internet Archive for %s\n", SourceURL)
	rows, err := cdxQuery()
	if err != nil {
		return err
	}
	total := len(rows)

	// One capture per calendar year: enough to bound the claim, few enough to
	// be polite to the Archive.
	perYear := map[string][]string{}
	for _, r := range rows {
		if len(r) < 2 || len(r[0]) < 4 {
			continue
		}
		y := r[0][:4]
		if _, seen := perYear[y]; !seen {
			perYear[y] = r
		}
	}
	years := make([]string, 0, len(perYear))
	for y := range perYear {
		years = append(years, y)
	}
	sort.Strings(years)
	fmt.Printf("  %d captures, sampling one per year across %d years\n", total, len(years))

	h := History{URL: SourceURL, MeasuredAt: time.Now().UTC().Format(time.RFC3339), Captures: total}
	var prev []Concept
	for _, y := range years {
		r := perYear[y]
		ts, digest := r[0], ""
		if len(r) > 1 {
			digest = r[1]
		}
		snap := Snapshot{Timestamp: ts, Date: fmt.Sprintf("%s-%s-%s", ts[:4], ts[4:6], ts[6:8]), Digest: digest}

		body, err := archiveFetch(ts)
		if err != nil {
			snap.Unusable = err.Error()
			h.Snapshots = append(h.Snapshots, snap)
			fmt.Printf("  %s  unusable: %v\n", snap.Date, err)
			continue
		}
		snap.Bytes = len(body)
		cs, err := parse(body)
		if err != nil || len(cs) == 0 {
			// The Archive's apology page returns 200 and parses as nothing.
			// Recording that as "zero codes" would invent a change.
			snap.Unusable = "no concepts parsed — most likely an Archive error page served with a 200"
			h.Snapshots = append(h.Snapshots, snap)
			fmt.Printf("  %s  unusable: %s\n", snap.Date, snap.Unusable)
			continue
		}
		snap.Concepts = len(cs)
		if prev != nil {
			if d := diff(prev, cs); d != "" {
				snap.Changes = d
				h.Changes++
			}
		}
		prev = cs
		h.Usable++
		h.Snapshots = append(h.Snapshots, snap)
		status := "identical"
		if snap.Changes != "" {
			status = "CHANGED"
		} else if h.Usable == 1 {
			status = "baseline"
		}
		fmt.Printf("  %s  %d codes  %s\n", snap.Date, snap.Concepts, status)
		time.Sleep(400 * time.Millisecond) // be polite
	}

	var first, last string
	for _, s := range h.Snapshots {
		if s.Concepts == 0 {
			continue
		}
		if first == "" {
			first = s.Date
		}
		last = s.Date
	}
	h.Span = first + " to " + last

	if err := os.MkdirAll("evidence", 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(h, "", "  ")
	if err := os.WriteFile(filepath.Join("evidence", "history.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("\n%d usable snapshots, %s, %d changes to the code set\n",
		h.Usable, h.Span, h.Changes)
	fmt.Println("written to evidence/history.json")
	return nil
}

func cdxQuery() ([][]string, error) {
	q := url.Values{
		"url":       {SourceURL},
		"output":    {"json"},
		"filter":    {"statuscode:200"},
		"collapse":  {"digest"},
		"fl":        {"timestamp,digest"},
		"limit":     {"400"},
		"from":      {"2000"},
		"pageSize":  {"5"},
		"showResum": {"false"},
	}
	req, _ := http.NewRequest(http.MethodGet, cdxAPI+"?"+q.Encode(), nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var rows [][]string
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("CDX returned something that is not a table: %w", err)
	}
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] == "timestamp" {
		rows = rows[1:] // header
	}
	return rows, nil
}

// archiveFetch retrieves one capture verbatim. The id_ suffix asks the Archive
// for the original bytes rather than its rewritten, banner-injected copy.
func archiveFetch(ts string) ([]byte, error) {
	u := fmt.Sprintf("https://web.archive.org/web/%sid_/%s", ts, SourceURL)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "Temporarily Offline") {
		return nil, fmt.Errorf("Archive apology page returned with a 200")
	}
	return body, nil
}
