package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"sort"
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

// The provenance must name the code that ran, not just the data it read.
func TestBuildProvenanceNamesTheCode(t *testing.T) {
	b := readBuild()
	for _, want := range []string{"main.go", "generate.go", "build.go"} {
		sum, ok := b.Sources[want]
		if !ok {
			t.Errorf("%s not embedded — the plan cannot be checksummed", want)
			continue
		}
		if len(sum) != 64 {
			t.Errorf("%s: %q is not a sha256 hex digest", want, sum)
		}
	}
	ttl := b.provenanceTurtle()
	for _, want := range []string{"prov:SoftwareAgent", "prov:hadPlan", "prov:Plan", "spdx:checksumValue"} {
		if !strings.Contains(ttl, want) {
			t.Errorf("provenance is missing %s", want)
		}
	}
}

// An unreproducible build must say so in the artefact rather than silently
// omitting the commit.
func TestUnstampedBuildWarnsInTheArtefact(t *testing.T) {
	if w := (Build{}).Warning(); w == "" {
		t.Fatal("an unstamped build reported no warning")
	}
	if w := (Build{Stamped: true, Dirty: true}).Warning(); w == "" {
		t.Fatal("a dirty build reported no warning")
	}
	if w := (Build{Stamped: true}).Warning(); w != "" {
		t.Fatalf("a clean stamped build warned anyway: %s", w)
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

	stamped := strings.Replace(string(a), "go1.26.4", "go9.9.9", 1)
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

// Every predicate must be used in its own range and domain. These six were
// wrong in the first published artefact: a literal where a resource belongs, an
// IRI where a literal belongs, a build warning filed as an access restriction,
// and a commit sha hung on an Agent by a property defined for Entities. A
// consumer that reasons over them would draw false conclusions, quietly.
func TestNoOffSpecPredicates(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 66912, FetchedAt: "2026-01-01T00:00:00Z"}
	ttl := string(turtle(cs, src, "2026-01-01"))

	banned := map[string]string{
		`dct:hasVersion "`:   "dcterms:hasVersion takes a resource, not a literal",
		"dct:accessRights":   "accessRights is about who may read it, not whether it rebuilds",
		"dct:extent":         "a byte count belongs in dcat:byteSize, typed",
		`dct:identifier <`:   "dcterms:identifier takes a literal",
		"prov:value     \"a": "a revision identifies the plan, not the agent",
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
		t.Error("checksums are still blank nodes; they cannot be cited or sorted")
	}
}

// Two builds of the SAME commit must produce the same identifier. This build is
// not reproducible — the binary digest and build time move every time — and if
// that noise reached the identifier it would churn on every rebuild while
// nothing published had changed.
func TestVersionIdentifierIgnoresTheBuildStamp(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	rev := "4d537f1fef644ba60905165b0f0593043a9a2396"
	a, _ := dataGraph(cs, src, "2026-01-01", Build{
		Stamped: true, Revision: rev, GoVersion: "go1.26.4",
		Binary: "aaaa", Committed: "2026-01-01T00:00:00Z"})
	b, _ := dataGraph(cs, src, "2026-01-01", Build{
		Stamped: true, Revision: rev, GoVersion: "go9.9.9",
		Binary: "bbbb", Committed: "2027-02-02T00:00:00Z"})
	if trustyCode(a.trs) != trustyCode(b.trs) {
		t.Error("the version identifier moved because the toolchain did")
	}
}

// ...and it must move when the content does, or it is decorative.
func TestVersionIdentifierTracksTheContent(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	base, _ := dataGraph(cs, src, "2026-01-01", Build{})
	for _, mut := range []struct {
		name string
		fn   func() *graph
	}{
		{"a display changed", func() *graph {
			c := append([]Concept(nil), cs...)
			c[0].Display = "Somewhere Else"
			g, _ := dataGraph(c, src, "2026-01-01", Build{})
			return g
		}},
		{"a code dropped", func() *graph {
			g, _ := dataGraph(cs[1:], src, "2026-01-01", Build{})
			return g
		}},
		{"the source digest changed", func() *graph {
			s2 := src
			s2.SHA256 = "def"
			g, _ := dataGraph(cs, s2, "2026-01-01", Build{})
			return g
		}},
	} {
		if trustyCode(base.trs) == trustyCode(mut.fn().trs) {
			t.Errorf("%s did not change the version identifier", mut.name)
		}
	}
}

// A third party must be able to recompute the identifier from what we publish,
// without reimplementing our generator. This test IS that third party: it reads
// dist/usps-states.nt, does the documented steps by hand, and compares.
func TestVersionIdentifierRecomputesFromThePublishedQuads(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	ttl := string(turtle(cs, src, "2026-01-01"))
	nt := string(ntDoc(cs, src, "2026-01-01"))

	i := strings.Index(ttl, VersionBase)
	if i < 0 {
		t.Fatal("no version IRI in the artefact")
	}
	code := ttl[i+len(VersionBase):]
	code = code[:strings.IndexByte(code, '>')]
	if len(code) != 45 || !strings.HasPrefix(code, "RA") {
		t.Fatalf("artifact code %q is not 45 characters of module RA", code)
	}

	// The documented steps, done independently of trustyCode.
	back := strings.ReplaceAll(nt, "<"+VersionBase+code+">", "<"+VersionBase+" >")
	lines := strings.Split(strings.TrimSuffix(back, "\n"), "\n")
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	if want := "RA" + base64.RawURLEncoding.EncodeToString(sum[:]); want != code {
		t.Errorf("recomputed %s, artefact says %s", want, code)
	}
}

// The published links must carry both halves: the ref to read, and the revision
// that ref actually resolved to. A dirty build has no honest permalink and must
// publish none rather than one that points at code which never ran.
func TestPermalinkOnlyWhenItIsTrue(t *testing.T) {
	clean := Build{Stamped: true, Revision: "abc123"}
	if clean.PermalinkURL("main.go") == "" {
		t.Error("a clean stamped build published no permalink")
	}
	for _, b := range []Build{
		{Stamped: true, Dirty: true, Revision: "abc123"},
		{Stamped: false, Revision: "abc123"},
		{Stamped: true},
	} {
		if u := b.PermalinkURL("main.go"); u != "" {
			t.Errorf("build %+v published permalink %s it cannot stand behind", b, u)
		}
	}
}

// Timestamps must stay in the XML Schema canonical form. "Z" and "+00:00" are
// the same instant but different RDF literals, so a change here silently moves
// the content identifier — and some RDF libraries will rewrite one into the
// other if you let them serialise the graph for you.
func TestTimestampsAreCanonical(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	nt := string(ntDoc(cs, src, "2026-01-01"))
	if strings.Contains(nt, "+00:00") {
		t.Error("a timestamp is in the non-canonical +00:00 form")
	}
	if !strings.Contains(nt, `"2026-01-01T00:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime>`) {
		t.Error("the canonical Z form is not being emitted")
	}
}

// The version identifier is computed by sorting N-Triples, which is RDFC-1.0
// canonical form ONLY because this graph has no blank nodes: canonicalisation
// is what assigns them stable labels, and sorting cannot do it. Skolemising the
// checksums removed the last one. If a blank node ever comes back, the shortcut
// silently stops being canonical and the published identifier stops meaning
// what the artefact says it means — so fail here instead.
func TestDataGraphHasNoBlankNodes(t *testing.T) {
	cs := fixture(t)
	src := Source{URL: SourceURL, SHA256: "abc", Bytes: 1, FetchedAt: "2026-01-01T00:00:00Z"}
	nt := string(ntDoc(cs, src, "2026-01-01"))
	if strings.Contains(nt, "_:") {
		t.Error("a blank node reached the data graph; sorted N-Triples is no longer canonical")
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
