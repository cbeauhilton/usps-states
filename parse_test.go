package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// The committed evidence file is the fixture, so these run without network and
// keep working when USPS is down or has moved.
func fixture(t *testing.T) []Concept {
	t.Helper()
	body, err := os.ReadFile("evidence/28apb.htm")
	if err != nil {
		t.Fatalf("evidence/28apb.htm missing — run `usps-states extract`: %v", err)
	}
	cs, err := parse(body)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestSixtyTwoCodes(t *testing.T) {
	if got := len(fixture(t)); got != 62 {
		t.Fatalf("got %d concepts, want 62 — 59 states and possessions plus AA AE AP", got)
	}
}

// The whole reason tables are selected by header rather than by position.
// Appendix B defines NE twice on one page: Nebraska in State/Possession, and
// Northeast in Geographic Directional. Taking the wrong table is silent.
func TestNEIsNebraskaNotNortheast(t *testing.T) {
	for _, c := range fixture(t) {
		if c.Code == "NE" {
			if c.Display != "Nebraska" {
				t.Fatalf("NE = %q, want Nebraska — the Geographic Directional table leaked in", c.Display)
			}
			return
		}
	}
	t.Fatal("NE absent entirely")
}

// No geographic directional may appear at all. These are the codes that table
// defines; every one of them is a bug if it reaches the output.
func TestNoGeographicDirectionals(t *testing.T) {
	got := map[string]string{}
	for _, c := range fixture(t) {
		got[c.Code] = c.Display
	}
	for _, bad := range []string{"N", "S", "E", "W", "NW", "SW", "SE"} {
		if d, ok := got[bad]; ok {
			t.Errorf("directional %s (%q) leaked into the output", bad, d)
		}
	}
}

// The six the Census/ANSI list lacks are the entire reason this repository is
// not just a pointer at ANSI INCITS 38.
func TestTheSixANSILacks(t *testing.T) {
	got := map[string]bool{}
	for _, c := range fixture(t) {
		got[c.Code] = true
	}
	for _, code := range []string{"AA", "AE", "AP", "FM", "MH", "PW"} {
		if !got[code] {
			t.Errorf("%s missing — ANSI INCITS 38 lacks it too, so we would have nothing", code)
		}
	}
}

func TestEveryCodeIsTwoUppercaseLetters(t *testing.T) {
	for _, c := range fixture(t) {
		if len(c.Code) != 2 {
			t.Errorf("%q is not two characters", c.Code)
		}
		for _, r := range c.Code {
			if r < 'A' || r > 'Z' {
				t.Errorf("%q is not uppercase letters", c.Code)
				break
			}
		}
		if c.Display == "" {
			t.Errorf("%s has no display", c.Code)
		}
	}
}

func TestSortedAndUnique(t *testing.T) {
	cs := fixture(t)
	for i := 1; i < len(cs); i++ {
		if cs[i-1].Code >= cs[i].Code {
			t.Fatalf("not sorted or not unique at %d: %s then %s", i, cs[i-1].Code, cs[i].Code)
		}
	}
}

// USPS renders AA without a space before the bracket. tx.fhir.org's expansion
// has one. We reproduce the authority, typo and all, rather than tidying it —
// a silent correction is a difference nobody can audit later.
func TestReproducesTheAuthorityVerbatim(t *testing.T) {
	for _, c := range fixture(t) {
		if c.Code == "AA" && c.Display != "Armed Forces Americas(except Canada)" {
			t.Fatalf("AA = %q; the authority writes it without the space", c.Display)
		}
	}
}

// Generation must be deterministic or SHA256SUMS means nothing.
func TestGenerationIsDeterministic(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "deadbeef", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	for _, gen := range []func([]Concept, Source, string) []byte{fhirCodeSystem, turtle, ndjson} {
		a, b := gen(cs, src, "2026-01-01"), gen(cs, src, "2026-01-01")
		if string(a) != string(b) {
			t.Error("generator is not deterministic")
		}
	}
}

// The stamp must name the program that ran, and sit outside the content graph
// so that verify can compare the content at any commit.
func TestStampNamesTheProgram(t *testing.T) {
	stamp := readBuild().stamp()
	for _, want := range []string{"prov:SoftwareAgent", `dct:identifier "github.com/cbeauhilton/usps-states"`} {
		if !strings.Contains(stamp, want) {
			t.Errorf("stamp is missing %s", want)
		}
	}
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	ttl := string(turtle(cs, src, "2026-01-01"))
	if !strings.HasSuffix(ttl, stamp) {
		t.Error("the build stamp is not the last block of the Turtle")
	}
}

// The cache must never let a stale answer pass as a live one, and must refuse
// a corrupt entry rather than serve it.
func TestCacheRejectsACorruptedEntry(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)

	body := []byte("<html>original</html>")
	sum := sha256.Sum256(body)
	src := Source{URL: SourceURL, SHA256: hex.EncodeToString(sum[:]), Bytes: len(body)}
	if err := writeCache(SourceURL, body, src); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := readCache(SourceURL); !ok {
		t.Fatal("a freshly written entry did not read back")
	}
	bp, _ := cachePaths(SourceURL)
	if err := os.WriteFile(bp, []byte("<html>tampered</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := readCache(SourceURL); ok {
		t.Fatal("a body that no longer matches its recorded digest was served from cache")
	}
}

func TestOfflineWithoutACacheFails(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)
	if _, _, err := fetchCached(FetchOpts{Offline: true}); err == nil {
		t.Fatal("-offline with an empty cache should fail, not reach the network")
	}
}

func TestOfflineAndRefreshAreRefused(t *testing.T) {
	if _, _, err := fetchCached(FetchOpts{Offline: true, Refresh: true}); err == nil {
		t.Fatal("contradictory flags were accepted")
	}
}

// dataTriples must ignore the build stamp and nothing else: two artefacts from
// different commits compare equal on content, but a changed code does not.
func TestDataTriplesIgnoresOnlyTheBuildStamp(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	a := turtle(cs, src, "2026-01-01")

	stamped := strings.Replace(string(a), `dct:identifier "github.com/cbeauhilton/usps-states"`,
		`dct:identifier "example.com/rebuilt/elsewhere"`, 1)
	if dataTriples(a) != dataTriples([]byte(stamped)) {
		t.Error("a changed build stamp altered the data comparison")
	}
	changed := strings.Replace(string(a), "Nebraska", "Northeast", 1)
	if dataTriples(a) == dataTriples([]byte(changed)) {
		t.Error("a changed concept did NOT alter the data comparison")
	}
}

// Cosmetic churn: the page's bytes move constantly while the code set does not.
// The Internet Archive holds 118 distinct digests across fourteen years and the
// 62 never changed. That must not fail, and must not pass silently either.
func TestCosmeticChurnIsDetectedNotFatal(t *testing.T) {
	orig, err := os.ReadFile("evidence/28apb.htm")
	if err != nil {
		t.Fatal(err)
	}
	// Same tables, different bytes — exactly what a banner change looks like.
	churned := bytes.Replace(orig, []byte("<body"), []byte("<body data-banner=\"promo-2027\""), 1)
	if bytes.Equal(orig, churned) {
		t.Skip("fixture has no <body to perturb")
	}
	a, err := parse(orig)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parse(churned)
	if err != nil {
		t.Fatalf("a cosmetic change broke parsing: %v", err)
	}
	if d := diff(a, b); d != "" {
		t.Fatalf("cosmetic change altered the concepts:\n%s", d)
	}
	if sha256.Sum256(orig) == sha256.Sum256(churned) {
		t.Fatal("the fixture was not actually perturbed")
	}
}

// The published provenance must be anchored to the reviewable extract, not only
// to the volatile page, or it stops resolving the first time a banner changes.
func TestTurtleCitesTheCanonicalExtract(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	ttl := string(turtle(cs, src, "2026-01-01"))
	for _, want := range []string{":data a prov:Entity", "data/usps-states.csv",
		"pav:importedFrom :data", "pav:importedFrom :source",
		"prov:hadPrimarySource :source"} {
		if !strings.Contains(ttl, want) {
			t.Errorf("provenance chain is missing %q", want)
		}
	}
}

// Every predicate must be used in its own range and domain. These were wrong
// in the first published artefact: a literal where a resource belongs, an IRI
// where a literal belongs, a build warning filed as an access restriction. A
// consumer that reasons over them would draw false conclusions, quietly.
func TestNoOffSpecPredicates(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 66912, FetchedAt: "2026-01-01T00:00:00Z"}
	ttl := string(turtle(cs, src, "2026-01-01"))

	banned := map[string]string{
		`dct:hasVersion "`: "dcterms:hasVersion takes a resource, not a literal",
		"dct:accessRights": "accessRights is about who may read it, not whether it rebuilds",
		"dct:extent":       "a byte count belongs in dcat:byteSize, typed",
		`dct:identifier <`: "dcterms:identifier takes a literal",
	}
	for bad, why := range banned {
		if strings.Contains(ttl, bad) {
			t.Errorf("off-spec predicate %q is back: %s", bad, why)
		}
	}
	for _, want := range []string{
		"dcat:byteSize", "pav:version", "pav:retrievedFrom", "pav:importedFrom",
		"pav:createdWith", "prov:hadPrimarySource", "a spdx:Checksum",
	} {
		if !strings.Contains(ttl, want) {
			t.Errorf("expected %s in the artefact", want)
		}
	}
	if strings.Contains(ttl, "spdx:checksum [") {
		t.Error("checksums are blank nodes again; they cannot be cited")
	}
}

// A release artefact must verify without anyone having to remember which -ref
// produced it. The links are part of the content, so a forgotten flag would
// otherwise look exactly like a tampered file.
func TestReleaseVerifiesWithoutRepeatingTheFlag(t *testing.T) {
	if got := resolvePublishRef(false, "main", "v0.1.0"); got != "v0.1.0" {
		t.Errorf("verify used %q, not the ref the artefact was generated with", got)
	}
	if got := resolvePublishRef(true, "main", "v0.1.0"); got != "main" {
		t.Errorf("an explicit -ref was overridden by the recorded one: %q", got)
	}
	if got := resolvePublishRef(false, "main", ""); got != "main" {
		t.Errorf("with nothing recorded, the default should stand, got %q", got)
	}
}
