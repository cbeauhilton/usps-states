package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// generate builds every published artefact from data/usps-states.csv.
//
// One source, four shapes, all digested. Nothing in dist/ is edited by hand and
// `verify` fails if it was: a published artefact that cannot be re-derived is
// exactly the thing this repository exists to avoid.
func generate() error {
	cs, err := readCSV("data/usps-states.csv")
	if err != nil {
		return err
	}
	var src Source
	b, err := os.ReadFile("evidence/source.json")
	if err != nil {
		return fmt.Errorf("%w — run `usps-states extract` first", err)
	}
	if err := json.Unmarshal(b, &src); err != nil {
		return err
	}
	// USPS publishes no version and no changelog, so this repository mints one:
	// the date the CODE SET last changed. Not the date we last looked — a
	// version that moves when nothing moved is noise, and it is exactly the
	// mistake the HGNC bucket forces you to avoid by exposing an object
	// timestamp rather than a fetch time.
	changed := src.ChangedAt
	if changed == "" {
		changed = src.FetchedAt // first extract, or a record written before this field existed
	}
	version := changed[:10]

	if err := os.MkdirAll("dist", 0o755); err != nil {
		return err
	}
	for name, gen := range map[string]func([]Concept, Source, string) []byte{
		"CodeSystem-usps-states.json": fhirCodeSystem,
		"usps-states.ttl":             turtle,
		"usps-states.nt":              ntDoc,
		"usps-states.ndjson":          ndjson,
	} {
		if err := os.WriteFile(filepath.Join("dist", name), gen(cs, src, version), 0o644); err != nil {
			return err
		}
	}
	rb := readBuild()
	rb.Ref = publishRef
	if bj, err := json.MarshalIndent(rb, "", "  "); err == nil {
		if err := os.WriteFile("evidence/build.json", append(bj, '\n'), 0o644); err != nil {
			return err
		}
	}
	sums, err := digests("dist")
	if err != nil {
		return err
	}
	var lines []string
	for _, n := range sortedKeys(sums) {
		if n == "SHA256SUMS" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %s", sums[n], n))
	}
	if err := os.WriteFile("dist/SHA256SUMS", []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("generated %d artefacts for version %s (%d concepts)\n", len(lines), version, len(cs))
	return nil
}

// fhirCodeSystem is the artefact a terminology server loads. content: complete
// is the load-bearing field — it is the difference between "here are the codes"
// and hl7.terminology's not-present declaration.
func fhirCodeSystem(cs []Concept, src Source, version string) []byte {
	concepts := make([]map[string]string, 0, len(cs))
	for _, c := range cs {
		concepts = append(concepts, map[string]string{"code": c.Code, "display": c.Display})
	}
	// dom-6: a resource should carry narrative. The FHIR validator warns without
	// it, and a human opening the raw resource deserves to see what it is.
	var nar strings.Builder
	nar.WriteString(`<div xmlns="http://www.w3.org/1999/xhtml"><p>`)
	nar.WriteString(fmt.Sprintf("USPS state and possession abbreviations, version %s: %d codes "+
		"derived from Publication 28 Appendix B. Not a United States Postal Service product.</p>"+
		"<table><tr><th>Code</th><th>Display</th></tr>", version, len(cs)))
	for _, c := range cs {
		nar.WriteString("<tr><td>" + xmlEscape(c.Code) + "</td><td>" + xmlEscape(c.Display) + "</td></tr>")
	}
	nar.WriteString("</table></div>")

	doc := map[string]any{
		"resourceType": "CodeSystem",
		"id":           "usps-states",
		"text":         map[string]string{"status": "generated", "div": nar.String()},
		"url":          CanonicalSystem,
		"version":      version,
		"name":         "USPSStates",
		"title":        "USPS state and possession abbreviations",
		"status":       "active",
		"experimental": false,
		"date":         src.FetchedAt,
		"publisher":    "github.com/cbeauhilton/usps-states",
		"description": "The two-letter state, possession and military abbreviations from USPS " +
			"Publication 28 Appendix B, derived from the authority and republished as data. " +
			"This is NOT a United States Postal Service product and carries no USPS endorsement. " +
			"See dist/usps-states.ttl for the full derivation chain.",
		"copyright": "The abbreviations are facts and are not claimed. This compilation is released " +
			"under CC0 1.0. USPS asserts copyright over material on its website; that notice is " +
			"reproduced in the README and is a claim about that site, not about this list.",
		"caseSensitive": true,
		"content":       "complete",
		"count":         len(cs),
		"concept":       concepts,
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return append(b, '\n')
}

// turtle is the richest artefact: the same concepts as SKOS, with a PROV-O and
// PAV derivation chain so a copy separated from this repository still says
// where it came from, from what bytes, when, and under whose claim.
//
// The European Commission's ICD-O-3 ontology is 2.1 MB of RDF carrying zero
// licence or provenance annotations — the terms live in a copyright.txt on a
// different path entirely. RDF can carry this; most publishers just do not.
//
// Every predicate here is used in its own range and domain. That is not
// pedantry: a consumer that reasons over dcterms:accessRights will conclude
// this dataset is access-restricted if we park a build warning in it, and a
// harvester that reads dcterms:hasVersion expects a resource, not a date.
func turtle(cs []Concept, src Source, version string) []byte {
	bld := readBuild()
	bld.Ref = publishRef
	g, code := dataGraph(cs, src, version, bld)

	var b strings.Builder
	b.WriteString(strings.Replace(g.String(), trustyPlaceholder, code, 1))
	b.WriteString("\n")
	b.WriteString(bld.provenanceTurtle())
	b.WriteString("\n")
	for _, c := range cs {
		b.WriteString(fmt.Sprintf(":%s a skos:Concept ;\n", c.Code))
		b.WriteString("    skos:inScheme  :scheme ;\n")
		b.WriteString(fmt.Sprintf("    skos:notation  %s ;\n", escLit(c.Code)))
		b.WriteString(fmt.Sprintf("    skos:prefLabel %s@en .\n", escLit(c.Display)))
	}
	b.WriteString(trustyNote(code))
	return []byte(b.String())
}

// ntDoc is the canonical N-Triples the version identifier is computed over,
// published so nobody has to reimplement this file to check it.
func ntDoc(cs []Concept, src Source, version string) []byte {
	bld := readBuild()
	bld.Ref = publishRef
	g, code := dataGraph(cs, src, version, bld)
	return ntriples(g.trs, code)
}

// dataGraph builds the content half of the artefact: the concepts, where they
// came from, and under what claim. The build stamp is not in here on purpose —
// see trustyCode.
func dataGraph(cs []Concept, src Source, version string, bld Build) (*graph, string) {
	changed := src.ChangedAt
	if changed == "" {
		changed = src.FetchedAt
	}
	csvSum := fileSum("data/usps-states.csv")
	csvBytes := fileSize("data/usps-states.csv")

	g := newGraph()
	g.raw("# USPS state and possession abbreviations, derived from the authority.")
	g.raw("# Generated by github.com/cbeauhilton/usps-states — do not edit.")
	g.raw("")
	for _, l := range []string{
		"@prefix skos: <http://www.w3.org/2004/02/skos/core#> .",
		"@prefix prov: <http://www.w3.org/ns/prov#> .",
		"@prefix pav:  <http://purl.org/pav/> .",
		"@prefix dct:  <http://purl.org/dc/terms/> .",
		"@prefix dcat: <http://www.w3.org/ns/dcat#> .",
		"@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .",
		"@prefix xsd:  <http://www.w3.org/2001/XMLSchema#> .",
		"@prefix spdx: <http://spdx.org/rdf/terms#> .",
		"@prefix :     <" + Base + "> .",
	} {
		g.raw(l)
	}
	g.raw("")

	// --- the scheme ---------------------------------------------------------
	g.begin(qn(":scheme"), "skos:ConceptScheme")
	g.set("dct:title", langLit("USPS state and possession abbreviations", "en"))
	g.note("In FHIR this code system is identified by its canonical URL. That is a",
		"string used as a name, which is what dcterms:identifier takes — not an IRI",
		"reference to the United States Postal Service, which is a different thing.")
	g.set("dct:identifier", lit(CanonicalSystem))
	g.note("This release, named by a hash of its own content. See the note at the",
		"foot of this file for how to recompute it.")
	g.set("dct:hasVersion", versionTerm())
	g.set("pav:version", lit(version))
	g.set("dct:issued", typedLit(version, "xsd:date"))
	g.set("dct:license", abs("https://creativecommons.org/publicdomain/zero/1.0/"))
	g.set("dct:publisher", qn(":publisher"))
	g.set("dct:rights", lit("The abbreviations are facts and are not claimed. This "+
		"compilation is CC0. USPS asserts copyright over material on its website; that is a "+
		"claim about that site, not about this list. This is not a USPS product."))
	g.note("Publication 28 is the document that DEFINES these abbreviations, which is",
		"what prov:hadPrimarySource is for: an entity produced by an agent with direct",
		"knowledge of the topic, as opposed to anyone who merely repeats them.")
	g.set("prov:hadPrimarySource", qn(":source"))
	g.note("CSV to SKOS is a transformation into another model, which PAV calls an",
		"import. Both PAV predicates below are subproperties of prov:wasDerivedFrom,",
		"so a consumer that only knows PROV still sees the derivation chain.")
	g.set("pav:importedFrom", qn(":data"))
	g.set("prov:wasGeneratedBy", qn(":extraction"))
	g.setAll("skos:hasTopConcept", conceptTerms(cs))
	g.raw("")

	// --- who published it ---------------------------------------------------
	g.begin(qn(":publisher"), "prov:Agent, prov:Organization")
	g.set("dct:title", lit("github.com/cbeauhilton/usps-states"))
	g.set("dct:source", abs(RepoURL))
	g.raw("")

	// --- what was done ------------------------------------------------------
	g.begin(qn(":extraction"), "prov:Activity")
	g.set("prov:startedAtTime", typedLit(src.FetchedAt, "xsd:dateTime"))
	g.set("prov:used", qn(":source"))
	g.set("prov:wasAssociatedWith", qn(":publisher"))
	g.set("pav:createdWith", qn(":software"))
	g.set("dct:description", lit("Parsed the State/Possession and Military tables from "+
		"Publication 28 Appendix B. The Geographic Directional table on the same page is "+
		"excluded deliberately: it defines NE as Northeast, where the State/Possession "+
		"table defines NE as Nebraska."))
	g.raw("")

	// --- the canonical extract ----------------------------------------------
	g.raw("# The canonical extract: reviewable in a diff, and stable across the page's")
	g.raw("# cosmetic churn. The scheme derives from THIS; this derives from the page.")
	g.begin(qn(":data"), "prov:Entity, dcat:Distribution")
	g.set("dct:title", lit("data/usps-states.csv"))
	linkPair(g, bld, "data/usps-states.csv")
	g.set("dcat:mediaType", abs("https://www.iana.org/assignments/media-types/text/csv"))
	if csvBytes > 0 {
		g.set("dcat:byteSize", typedLit(fmt.Sprint(csvBytes), "xsd:nonNegativeInteger"))
	}
	g.set("pav:importedFrom", qn(":source"))
	g.set("prov:wasGeneratedBy", qn(":extraction"))
	g.set("spdx:checksum", qn(":ck_data"))
	g.checksum(":ck_data", csvSum)
	g.raw("")

	// --- the bytes we read --------------------------------------------------
	g.raw("# The authority as fetched. pav:retrievedFrom, not importedFrom: these are")
	g.raw("# the bytes as served, with nothing transformed.")
	g.begin(qn(":source"), "prov:Entity, dcat:Distribution")
	g.set("pav:retrievedFrom", abs(src.URL))
	g.set("dcat:downloadURL", abs(src.URL))
	g.set("dcat:mediaType", abs("https://www.iana.org/assignments/media-types/text/html"))
	g.set("dcat:byteSize", typedLit(fmt.Sprint(src.Bytes), "xsd:nonNegativeInteger"))
	g.set("prov:generatedAtTime", typedLit(src.FetchedAt, "xsd:dateTime"))
	g.set("dct:modified", typedLit(changed, "xsd:dateTime"))
	if h, err := readHistory(); err == nil {
		g.set("dct:isReferencedBy", qn(":history"))
		g.set("spdx:checksum", qn(":ck_source"))
		g.checksum(":ck_source", src.SHA256)
		g.raw("")
		g.raw("# How stable is the authority? Measured against the Internet Archive rather")
		g.raw("# than asserted. The page's bytes churn constantly; the code set does not.")
		g.begin(qn(":history"), "prov:Entity, dcat:Distribution")
		g.set("dct:title", lit("evidence/history.json"))
		g.set("dct:description", lit(fmt.Sprintf(
			"%d Internet Archive captures of the authority spanning %s. %d snapshots were "+
				"usable; across them the code set changed %d times.",
			h.Captures, h.Span, h.Usable, h.Changes)))
		linkPair(g, bld, "evidence/history.json")
		g.set("dcat:mediaType", abs("https://www.iana.org/assignments/media-types/application/json"))
		g.set("prov:wasDerivedFrom", abs("https://web.archive.org/"))
		g.set("prov:generatedAtTime", typedLit(h.MeasuredAt, "xsd:dateTime"))
		g.set("spdx:checksum", qn(":ck_history"))
		g.checksum(":ck_history", fileSum("evidence/history.json"))
	} else {
		g.set("spdx:checksum", qn(":ck_source"))
		g.checksum(":ck_source", src.SHA256)
	}
	g.closeSubject()

	// The concepts are part of the content, so they are part of the identifier.
	// They are emitted as text below for readability, but the triples must be
	// in the graph or the hash would not cover the codes themselves.
	for _, c := range cs {
		g.subj, g.open = qn(":"+c.Code), false
		g.trs = append(g.trs,
			triple{g.subj, qn("a"), qn("skos:Concept")},
			triple{g.subj, qn("skos:inScheme"), qn(":scheme")},
			triple{g.subj, qn("skos:notation"), lit(c.Code)},
			triple{g.subj, qn("skos:prefLabel"), langLit(c.Display, "en")})
	}
	return g, trustyCode(g.trs)
}

// conceptTerms is every concept as a term, for skos:hasTopConcept.
func conceptTerms(cs []Concept) []term {
	out := make([]term, 0, len(cs))
	for _, c := range cs {
		out = append(out, qn(":"+c.Code))
	}
	return out
}

// linkPair writes both halves of a link: where to READ the file, against a ref
// name that moves by design, and WHICH revision was actually resolved when this
// ran. SLSA keeps the same two facts apart — externalParameters.ref against
// resolvedDependencies[].digest — because collapsing them loses the only thing
// that makes the digest checkable later.
func linkPair(g *graph, b Build, path string) {
	g.set("dct:source", abs(b.SourceURL(path)))
	if u := b.PermalinkURL(path); u != "" {
		g.set("dct:hasVersion", abs(u))
	}
}

// trustyNote tells a reader how to check the identifier without trusting us.
func trustyNote(code string) string {
	return "\n" +
		"# The version IRI above is a Trusty URI, module RA. To check it: take the\n" +
		"# triples of this file EXCLUDING the build-stamp block (:software, :plan,\n" +
		"# :src_*, :dep*), write them as N-Triples, replace the artifact code in the\n" +
		"# version IRI with a single space, sort the lines BY BYTE VALUE (LC_ALL=C for\n" +
		"# GNU sort — locale collation gives a different order and a different\n" +
		"# hash), join with newlines, add a\n" +
		"# trailing newline, and SHA-256 it. Base64url of that digest, unpadded, is the\n" +
		"# 43 characters that follow the module identifier. The full artifact code,\n" +
		"# module and hash together, is:\n" +
		"#\n" +
		"#   " + code + "\n" +
		"#\n" +
		"# The same triples are published as dist/usps-states.nt, which is that exact\n" +
		"# serialisation with the code substituted back in. Hash THOSE BYTES.\n" +
		"#\n" +
		"# Do not re-derive them by loading this file into an RDF library and asking\n" +
		"# it to write N-Triples. Some libraries normalise literals on the way out\n" +
		"# and hand back a different graph: rdflib 7.6.0 rewrites an xsd:dateTime of\n" +
		"# \"...Z\" as \"...+00:00\", which is the same instant, a different RDF literal,\n" +
		"# and a different hash. Z is the canonical form per XML Schema Part 2, so\n" +
		"# this file keeps it. That is the cost of hashing a serialisation, stated\n" +
		"# out loud rather than discovered by whoever checks first.\n" +
		"#\n" +
		"# That .nt file is RDFC-1.0 canonical form — the W3C Recommendation of\n" +
		"# 2024-05-21, formerly URDNA2015 — and not merely a recipe of our own. It\n" +
		"# can be sorted rather than canonicalised only because this graph contains\n" +
		"# no blank nodes, which is why the checksums above are named resources\n" +
		"# instead. A test fails the build if one ever comes back.\n"
}

// fileSize is the length of a file in bytes, or 0 when it cannot be read.
func fileSize(path string) int {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return int(fi.Size())
}

// readHistory loads the measured archive history, when it has been run.
func readHistory() (History, error) {
	var h History
	b, err := os.ReadFile(filepath.Join("evidence", "history.json"))
	if err != nil {
		return h, err
	}
	return h, json.Unmarshal(b, &h)
}

// ndjson is one concept per line — the shape a bulk loader wants, and the shape
// this content already had when it was pinned from a terminology server.
func ndjson(cs []Concept, _ Source, _ string) []byte {
	var b strings.Builder
	for _, c := range cs {
		line, _ := json.Marshal(map[string]string{
			"system": CanonicalSystem, "code": c.Code, "display": c.Display,
		})
		b.Write(line)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// xmlEscape is the minimum needed for XHTML narrative; displays are plain
// place names, but escaping is not optional in generated markup.
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// fileSum is the sha256 of a file, or "" when it cannot be read.
func fileSum(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// --- csv ---------------------------------------------------------------------

func writeCSV(path string, cs []Concept) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"code", "display"}); err != nil {
		return err
	}
	for _, c := range cs {
		if err := w.Write([]string{c.Code, c.Display}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func readCSV(path string) ([]Concept, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%s: no data rows", path)
	}
	out := make([]Concept, 0, len(rows)-1)
	for _, r := range rows[1:] {
		if len(r) < 2 {
			continue
		}
		out = append(out, Concept{Code: r[0], Display: r[1]})
	}
	return out, nil
}

// --- helpers -----------------------------------------------------------------

func findAll(n *html.Node, tag string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && x.Data == tag {
			out = append(out, x)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// cells returns the trimmed text of a row's th/td, in order.
func cells(row *html.Node) []string {
	var out []string
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.Data != "td" && c.Data != "th") {
			continue
		}
		out = append(out, strings.Join(strings.Fields(text(c)), " "))
	}
	return out
}

func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

func refs(cs []Concept) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, ":"+c.Code)
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func digests(dir string) (map[string]string, error) {
	out := map[string]string{}
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out[e.Name()] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// diff reports what moved between the held list and a freshly derived one.
func diff(held, live []Concept) string {
	h := map[string]string{}
	for _, c := range held {
		h[c.Code] = c.Display
	}
	l := map[string]string{}
	for _, c := range live {
		l[c.Code] = c.Display
	}
	var out []string
	for _, c := range live {
		if _, ok := h[c.Code]; !ok {
			out = append(out, fmt.Sprintf("  + %s  %s", c.Code, c.Display))
		} else if h[c.Code] != c.Display {
			out = append(out, fmt.Sprintf("  ~ %s  %q -> %q", c.Code, h[c.Code], c.Display))
		}
	}
	for _, c := range held {
		if _, ok := l[c.Code]; !ok {
			out = append(out, fmt.Sprintf("  - %s  %s", c.Code, c.Display))
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}
