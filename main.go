// Command usps-states derives the USPS state and possession abbreviations from
// the authority, and publishes them in four shapes with the provenance attached.
//
// Why this exists. FHIR's US Core binds `Address.state` to a value set defined
// as "every code in https://www.usps.com/", and nobody publishes those codes as
// data. HL7 Terminology registers the identity and ships content: not-present,
// quoting USPS's website copyright notice. The Census/ANSI list is 57 of the 62
// and is missing exactly the six a mailing address needs — AA AE AP for military
// mail, FM MH PW for the freely associated states. So implementers either scrape
// the page, ask a terminology server, or type the list in by hand.
//
// This does the first one, once, in the open, and shows its working.
//
//	usps-states extract    fetch the authority, parse it, write data/ + evidence/
//	usps-states generate   build dist/ from data/usps-states.csv
//	usps-states verify     re-derive and fail if anything moved
//	usps-states history    measure the code set against the Internet Archive
//
// extract and verify take -refresh (always ask the authority) and -offline (use
// the local cache at any age, never touch the network). Both always print which
// they used; see cache.go.
//
// The artefact you probably want is dist/CodeSystem-usps-states.json — a FHIR
// CodeSystem with content: complete, loadable straight into a terminology
// server. dist/usps-states.ttl is the same content as SKOS with a PROV-O
// derivation chain, so a copy that gets separated from this repository still
// says where it came from, from what bytes, when, and under whose claim.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// SourceURL is Publication 28 Appendix B — the authority, and the only place
// the full 62 are published.
//
// It serves a 404 to a curl-shaped User-Agent and a 200 to anything else. That
// is not a block worth defeating: saying who you are is enough, and there is
// nothing to spoof. Three separate surveys recorded this page as gone because
// the 404 was never opened; it was 54 KB of HTML.
const SourceURL = "https://pe.usps.com/text/pub28/28apb.htm"

// UserAgent identifies us. See SourceURL.
const UserAgent = "usps-states/1.0 (+https://github.com/cbeauhilton/usps-states)"

// CanonicalSystem is the code system URI every FHIR artefact binds. It is USPS's
// identity, not ours; hl7.terminology registers the same string. We publish
// content under it and claim nothing about USPS's rights — see README.
const CanonicalSystem = "https://www.usps.com/"

// Tables selected by their header text rather than by position.
//
// Appendix B has three two-column tables and taking the wrong one is silent.
// "Geographic Directional" is the trap: it defines NE as Northeast, while
// "State/Possession" defines NE as Nebraska. Position is not stable across a
// redesign; the headers have not moved in fourteen years.
var wanted = map[string]bool{
	"State/Possession": true,
	`Military "State"`: true,
	"Military “State”": true, // the page uses curly quotes
}

// excluded is listed explicitly so a future reader knows it was a decision.
const excluded = "Geographic Directional"

type Concept struct {
	Code    string `json:"code"`
	Display string `json:"display"`
}

type Source struct {
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Bytes      int    `json:"bytes"`
	FetchedAt  string `json:"fetched_at"`
	UserAgent  string `json:"user_agent"`
	StatusCode int    `json:"status_code"`
	Concepts   int    `json:"concepts"`

	// ChangedAt is when the CODE SET last moved, which is not when we last
	// looked. USPS publishes no version, so this repository mints one — and
	// minting it from the fetch date would bump the version every time someone
	// re-ran extract against an identical page. "Last verified" and "last
	// changed" are different facts and the version is the second one.
	ChangedAt string `json:"content_changed_at"`
}

var publishRef string

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: usps-states extract|generate|verify|history [-refresh] [-offline]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	var o FetchOpts
	fs.BoolVar(&o.Refresh, "refresh", false, "always ask the authority, ignoring the cache")
	fs.BoolVar(&o.Offline, "offline", false, "use the cached copy at any age; never touch the network")
	fs.DurationVar(&o.TTL, "ttl", DefaultTTL, "how long a cached copy stays fresh")
	fs.StringVar(&publishRef, "ref", "main", "git ref published links resolve against (a tag, for a release)")
	_ = fs.Parse(os.Args[2:])

	// A release is generated with -ref v1.2.3 so its published links resolve
	// against the tag rather than a moving branch. Those links are part of the
	// artefact's content, so re-deriving it with the default ref produces a
	// different file and `verify` reports a forgery that is really just a
	// forgotten flag. The ref that generated the artefact is recorded in
	// evidence/build.json; unless someone asks for a different one, use it.
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ref" {
			explicit = true
		}
	})
	publishRef = resolvePublishRef(explicit, publishRef, recordedRef())

	var err error
	switch os.Args[1] {
	case "extract":
		err = extract(o)
	case "generate":
		if err = generate("dist"); err == nil {
			err = writeBuild()
		}
	case "verify":
		err = verify(o)
	case "history":
		err = history()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "usps-states:", err)
		os.Exit(1)
	}
}

// fetch retrieves the authority and returns the body with its digest.
func fetch() ([]byte, Source, error) {
	req, err := http.NewRequest(http.MethodGet, SourceURL, nil)
	if err != nil {
		return nil, Source{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, Source{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, Source{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, Source{}, fmt.Errorf("%s: HTTP %d (%d bytes) — if this is 404, check the User-Agent",
			SourceURL, resp.StatusCode, len(body))
	}
	sum := sha256.Sum256(body)
	return body, Source{
		URL: SourceURL, SHA256: hex.EncodeToString(sum[:]), Bytes: len(body),
		FetchedAt: time.Now().UTC().Format(time.RFC3339), UserAgent: UserAgent,
		StatusCode: resp.StatusCode,
	}, nil
}

// parse walks the document and returns the concepts from the wanted tables.
func parse(body []byte) ([]Concept, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out []Concept
	seen := map[string]string{}
	var skipped int

	for _, tbl := range findAll(doc, "table") {
		rows := findAll(tbl, "tr")
		if len(rows) < 2 {
			continue
		}
		head := cells(rows[0])
		if len(head) < 2 {
			continue
		}
		if strings.EqualFold(head[0], excluded) {
			skipped++
			continue
		}
		if !wanted[head[0]] {
			continue
		}
		for _, r := range rows[1:] {
			c := cells(r)
			if len(c) < 2 {
				continue
			}
			name, code := strings.TrimSpace(c[0]), strings.ToUpper(strings.TrimSpace(c[1]))
			if name == "" || len(code) != 2 {
				continue
			}
			if prev, dup := seen[code]; dup {
				return nil, fmt.Errorf("code %s appears twice: %q and %q — the page changed shape", code, prev, name)
			}
			seen[code] = name
			out = append(out, Concept{Code: code, Display: name})
		}
	}
	if skipped == 0 {
		return nil, fmt.Errorf("the %q table is gone — it defines NE as Northeast and must be "+
			"excluded deliberately, so its absence means the page was restructured", excluded)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no concepts found; the wanted table headers %v did not appear", keys(wanted))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

func extract(o FetchOpts) error {
	body, src, err := fetchCached(o)
	if err != nil {
		return err
	}
	cs, err := parse(body)
	if err != nil {
		return err
	}
	src.Concepts = len(cs)

	// Carry the previous change date forward when the codes are unchanged, so
	// the version tracks the content rather than our curiosity.
	src.ChangedAt = src.FetchedAt
	if prev, err := recordedSource(); err == nil && prev.ChangedAt != "" {
		if held, err := readCSV("data/usps-states.csv"); err == nil && diff(held, cs) == "" {
			src.ChangedAt = prev.ChangedAt
			fmt.Printf("  unchanged since %s — version held, last_verified updated\n", prev.ChangedAt[:10])
		} else {
			fmt.Printf("  CONTENT CHANGED — version moves to %s\n", src.ChangedAt[:10])
		}
	}

	if err := writeCSV("data/usps-states.csv", cs); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(src, "", "  ")
	if err := os.WriteFile("evidence/source.json", append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile("evidence/28apb.htm", body, 0o644); err != nil {
		return err
	}
	fmt.Printf("extracted %d concepts from %s\n  sha256 %s  %d bytes\n",
		len(cs), SourceURL, src.SHA256, src.Bytes)
	return nil
}

// resolvePublishRef picks the ref published links resolve against: what was
// asked for, or failing that what the published artefact already used.
func resolvePublishRef(explicit bool, asked, recorded string) string {
	if explicit || recorded == "" {
		return asked
	}
	return recorded
}

// recordedRef is the ref the published artefact was generated with, or "".
func recordedRef() string {
	b, err := os.ReadFile(filepath.Join("evidence", "build.json"))
	if err != nil {
		return ""
	}
	var rec Build
	if json.Unmarshal(b, &rec) != nil {
		return ""
	}
	return rec.Ref
}

// recordedSource is the provenance currently published in evidence/.
func recordedSource() (Source, error) {
	var rec Source
	b, err := os.ReadFile(filepath.Join("evidence", "source.json"))
	if err != nil {
		return rec, err
	}
	return rec, json.Unmarshal(b, &rec)
}

func verify(o FetchOpts) error {
	held, err := readCSV("data/usps-states.csv")
	if err != nil {
		return err
	}
	body, src, err := fetchCached(o)
	if err != nil {
		return err
	}
	live, err := parse(body)
	if err != nil {
		return err
	}
	if d := diff(held, live); d != "" {
		return fmt.Errorf("the authority no longer matches data/usps-states.csv:\n%s\n"+
			"run `usps-states extract && usps-states generate` and review the diff", d)
	}
	fmt.Printf("verified: %d concepts, identical to the authority\n  source sha256 %s\n",
		len(live), src.SHA256)

	// The codes match. Did the page? These are different questions and only the
	// first one matters for correctness — but if the bytes moved, the digest we
	// PUBLISHED as provenance no longer describes anything a reader can fetch,
	// and that has to be said rather than left to rot.
	//
	// Expect this often. The page carries navigation, banners and analytics that
	// change constantly: the Internet Archive holds 118 distinct digests of it
	// across fourteen years, over which the code set never moved once.
	if rec, err := recordedSource(); err == nil && rec.SHA256 != src.SHA256 {
		fmt.Printf("\n  NOTE: the page changed but the codes did not.\n"+
			"    recorded %s (%d bytes)\n    live     %s (%d bytes)\n"+
			"  Cosmetic churn — navigation, banners, analytics. The published provenance\n"+
			"  still cites the recorded digest, which no longer resolves. Run\n"+
			"  `usps-states extract && usps-states generate` to refresh it.\n",
			rec.SHA256, rec.Bytes, src.SHA256, src.Bytes)
	}

	// dist/ must be reproducible from data/ — but only the DATA part of it.
	//
	// usps-states.ttl embeds the build stamp of whatever binary produced it,
	// and that legitimately differs between builds. So: byte-compare the
	// artefacts that carry no build stamp, and compare the Turtle with its
	// provenance block removed. A difference in the stamp alone is reported,
	// not failed.
	//
	// Generate into a scratch directory rather than over dist/: a check that
	// rewrites what it is checking leaves a dirty tree behind on failure and
	// hides the very edit it was meant to catch.
	tmp, err := os.MkdirTemp("", "usps-states-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := generate(tmp); err != nil {
		return err
	}
	for _, n := range []string{"CodeSystem-usps-states.json", "usps-states.ndjson", "usps-states.nt"} {
		want, err := os.ReadFile(filepath.Join("dist", n))
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(tmp, n))
		if err != nil {
			return err
		}
		if !bytes.Equal(want, got) {
			return fmt.Errorf("dist/%s does not reproduce from its inputs.\n"+
				"  Either a published artefact was edited, or data/usps-states.csv or\n"+
				"  evidence/source.json changed without regenerating. `git diff` will say which.", n)
		}
	}
	heldTTL, err := os.ReadFile(filepath.Join("dist", "usps-states.ttl"))
	if err != nil {
		return err
	}
	now, err := os.ReadFile(filepath.Join(tmp, "usps-states.ttl"))
	if err != nil {
		return err
	}
	if a, b := dataTriples(heldTTL), dataTriples(now); a != b {
		return fmt.Errorf("dist/usps-states.ttl does not reproduce from its inputs.\n" +
			"  Either a published artefact was edited, or data/usps-states.csv or\n" +
			"  evidence/source.json changed without regenerating. `git diff` will say which.")
	}
	fmt.Println("verified: 4 artefacts reproduce from data/usps-states.csv")
	if !bytes.Equal(heldTTL, now) {
		fmt.Println("  note: the build stamp in usps-states.ttl changed — dist/ was generated by\n" +
			"  a different build of this tool. The data is identical. Run `generate` and\n" +
			"  commit the result if that is what you intended.")
	}
	return nil
}

// dataTriples is the Turtle with the build-provenance block removed, so two
// artefacts built by different commits can be compared on their content.
func dataTriples(ttl []byte) string {
	s := string(ttl)
	start := strings.Index(s, ":extraction prov:qualifiedAssociation")
	if start < 0 {
		return s
	}
	end := strings.Index(s[start:], "\n:AA ")
	if end < 0 {
		return s[:start]
	}
	return s[:start] + s[start+end:]
}
