package main

import (
	"crypto/sha256"
	"encoding/base64"
	"sort"
	"strings"
)

// This file is the single source of truth for the DATA graph: every triple is
// written once and comes out as both Turtle (for people) and N-Triples (for the
// hash). Two emitters reading the same call sites cannot drift; two emitters
// reading the same variables can, and the drift would be silent — the published
// identifier would name a graph nobody ever serialised.

// Base is the namespace for every term this repository mints.
const Base = "https://github.com/cbeauhilton/usps-states#"

// VersionBase is where a version identifier lives. The trailing slash is the
// delimiter the trusty URI scheme requires before an artifact code: it is
// outside the Base64url alphabet, so the code can always be found again.
const VersionBase = "https://github.com/cbeauhilton/usps-states/version/"

// trustyPlaceholder marks where the artifact code goes while the graph is still
// being hashed. See trustyCode.
const trustyPlaceholder = "@@TRUSTY@@"

var prefixes = map[string]string{
	"":     Base,
	"skos": "http://www.w3.org/2004/02/skos/core#",
	"prov": "http://www.w3.org/ns/prov#",
	"dct":  "http://purl.org/dc/terms/",
	"dcat": "http://www.w3.org/ns/dcat#",
	"pav":  "http://purl.org/pav/",
	"rdfs": "http://www.w3.org/2000/01/rdf-schema#",
	"xsd":  "http://www.w3.org/2001/XMLSchema#",
	"spdx": "http://spdx.org/rdf/terms#",
}

// expand resolves a Turtle qname to the full IRI. It panics on an unknown
// prefix because that is a bug in this program, not a runtime condition: a
// mistyped prefix would otherwise be hashed into the published identifier.
func expand(q string) string {
	if q == "a" {
		return "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	}
	if strings.HasPrefix(q, "<") {
		return strings.TrimSuffix(strings.TrimPrefix(q, "<"), ">")
	}
	i := strings.Index(q, ":")
	if i < 0 {
		panic("not a qname: " + q)
	}
	base, ok := prefixes[q[:i]]
	if !ok {
		panic("unknown prefix in " + q)
	}
	return base + q[i+1:]
}

// term is one RDF term in both serialisations at once.
type term struct{ ttl, nt string }

func qn(q string) term  { return term{q, "<" + expand(q) + ">"} }
func abs(u string) term { return term{"<" + u + ">", "<" + u + ">"} }
func lit(s string) term { e := escLit(s); return term{e, e} }

func langLit(s, lang string) term {
	e := escLit(s) + "@" + lang
	return term{e, e}
}

func typedLit(s, dt string) term {
	return term{escLit(s) + "^^" + dt, escLit(s) + "^^<" + expand(dt) + ">"}
}

// versionTerm is the artifact's own version identifier. Its Turtle form carries
// a placeholder that is substituted once the hash is known; its N-Triples form
// carries a single blank space in that position, which is what gets hashed.
// A URI cannot otherwise contain a space, so the substitution is unambiguous
// and reversible — that is the whole trick, and it is why an artifact can name
// its own hash without circularity.
func versionTerm() term {
	return term{
		ttl: "<" + VersionBase + trustyPlaceholder + ">",
		nt:  "<" + VersionBase + " >",
	}
}

// escLit escapes a string literal. Turtle and N-Triples share this subset, and
// it is written out rather than delegated to %q because Go emits \a, \v and
// \x.. escapes that N-Triples does not allow.
func escLit(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

type triple struct{ s, p, o term }

// graph accumulates Turtle text and the triple list together. Every predicate
// goes through set, so the two can never describe different graphs.
type graph struct {
	lines []string
	trs   []triple
	subj  term
	open  bool
}

func newGraph() *graph { return &graph{} }

// raw writes a line that is not a triple: a header, a comment, a prefix.
func (g *graph) raw(s string) { g.closeSubject(); g.lines = append(g.lines, s) }

// note writes an indented comment inside the current subject block.
func (g *graph) note(lines ...string) {
	for _, l := range lines {
		g.lines = append(g.lines, "    # "+l)
	}
}

// begin opens a subject block. types is a comma-separated Turtle type list.
func (g *graph) begin(s term, types string) {
	g.closeSubject()
	g.subj, g.open = s, true
	g.lines = append(g.lines, s.ttl+" a "+types+" ;")
	for _, t := range strings.Split(types, ",") {
		g.trs = append(g.trs, triple{s, qn("a"), qn(strings.TrimSpace(t))})
	}
}

func (g *graph) set(p string, o term) {
	g.lines = append(g.lines, "    "+p+" "+o.ttl+" ;")
	g.trs = append(g.trs, triple{g.subj, qn(p), o})
}

// setAll writes one predicate with many objects: comma-separated in Turtle,
// one triple each in N-Triples.
func (g *graph) setAll(p string, os []term) {
	parts := make([]string, len(os))
	for i, o := range os {
		parts[i] = o.ttl
		g.trs = append(g.trs, triple{g.subj, qn(p), o})
	}
	g.lines = append(g.lines, "    "+p+" "+strings.Join(parts, ", ")+" ;")
}

// closeSubject turns the trailing ";" of the open block into ".". It edits the
// last emitted line rather than searching the buffer, so raw text written after
// a subject block — the build stamp, which is not part of the data graph —
// cannot be mistaken for it.
func (g *graph) closeSubject() {
	if !g.open {
		return
	}
	g.open = false
	for i := len(g.lines) - 1; i >= 0; i-- {
		if strings.HasSuffix(g.lines[i], " ;") {
			g.lines[i] = strings.TrimSuffix(g.lines[i], " ;") + " ."
			return
		}
	}
}

// checksum emits a named spdx:Checksum and returns its IRI.
//
// The blank node this replaces was idiomatic DCAT, but a blank node has no
// stable name: it cannot be cited, and canonicalising a graph that contains one
// is a labelling problem rather than a sort. Naming them is what lets the
// identifier below be computed by sorting lines.
func (g *graph) checksum(id, sha string) term {
	t := qn(id)
	g.raw("")
	g.begin(t, "spdx:Checksum")
	g.set("spdx:algorithm", qn("spdx:checksumAlgorithm_sha256"))
	g.set("spdx:checksumValue", lit(sha))
	g.closeSubject()
	return t
}

func (g *graph) String() string {
	g.closeSubject()
	return strings.Join(g.lines, "\n") + "\n"
}

// trustyCode is the artifact code of a Trusty URI, module RA.
//
// Sort the statements, serialise them, read the placeholder as a blank space,
// SHA-256, Base64url. Two characters name the module and its version, then 43
// characters of hash: 32 bytes is 256 bits, and unpadded Base64url of that is
// exactly 43 characters, which is the same as appending the two zero bits the
// scheme calls for.
//
// The line this draws: everything that identifies WHAT WAS PUBLISHED is inside,
// and everything that merely varies between two builds of the same commit is
// outside. So the codes, the digests, the licence and the revision-pinned links
// are covered; the binary digest, the compiler version and the build time are
// not, because those move when nothing was published differently and would make
// the identifier churn on a rebuild that changed nothing.
//
// This is a different question from `pav:version`, which is the date the CODE
// SET last moved. Two identifiers, two jobs: one names this exact graph, the
// other names the edition of the abbreviations it carries.
func trustyCode(trs []triple) string {
	lines := make([]string, 0, len(trs))
	for _, t := range trs {
		lines = append(lines, t.s.nt+" "+t.p.nt+" "+t.o.nt+" .")
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	return "RA" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// ntriples is the canonical serialisation the artifact code is computed over,
// with the version IRI resolved. Published so a third party can recompute the
// identifier without reimplementing this file.
func ntriples(trs []triple, code string) []byte {
	lines := make([]string, 0, len(trs))
	for _, t := range trs {
		o := strings.Replace(t.o.nt, "<"+VersionBase+" >", "<"+VersionBase+code+">", 1)
		lines = append(lines, t.s.nt+" "+t.p.nt+" "+o+" .")
	}
	sort.Strings(lines)
	return []byte(strings.Join(lines, "\n") + "\n")
}
