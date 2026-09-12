package main

import "strings"

// Base is the namespace for every term this repository mints.
const Base = "https://github.com/cbeauhilton/usps-states#"

// Turtle object forms. Qnames such as :scheme or skos:Concept are written as
// they are; these wrap the three kinds of object that need quoting.
func abs(u string) string           { return "<" + u + ">" }
func langLit(s, lang string) string { return lit(s) + "@" + lang }
func typedLit(s, dt string) string  { return lit(s) + "^^" + dt }

// lit escapes a string literal. Written out rather than delegated to %q
// because Go emits \a, \v and \x.. escapes that Turtle does not allow.
func lit(s string) string {
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

// graph accumulates Turtle text one subject block at a time.
type graph struct {
	lines []string
	open  bool
}

// raw writes a line that is not a triple: a header, a comment, a prefix.
func (g *graph) raw(s string) { g.closeSubject(); g.lines = append(g.lines, s) }

// note writes an indented comment inside the current subject block.
func (g *graph) note(lines ...string) {
	for _, l := range lines {
		g.lines = append(g.lines, "    # "+l)
	}
}

// begin opens a subject block. types is a comma-separated Turtle type list.
func (g *graph) begin(s, types string) {
	g.closeSubject()
	g.open = true
	g.lines = append(g.lines, s+" a "+types+" ;")
}

func (g *graph) set(p, o string) { g.lines = append(g.lines, "    "+p+" "+o+" ;") }

// setAll writes one predicate with many objects, comma-separated.
func (g *graph) setAll(p string, os []string) { g.set(p, strings.Join(os, ", ")) }

// closeSubject turns the trailing ";" of the open block into ".".
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

// checksum emits a named spdx:Checksum. Named rather than a blank node so it
// can be cited from outside the file.
func (g *graph) checksum(id, sha string) {
	g.raw("")
	g.begin(id, "spdx:Checksum")
	g.set("spdx:algorithm", "spdx:checksumAlgorithm_sha256")
	g.set("spdx:checksumValue", lit(sha))
	g.closeSubject()
}

func (g *graph) String() string {
	g.closeSubject()
	return strings.Join(g.lines, "\n") + "\n"
}
