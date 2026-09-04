package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"
)

// sources embeds this program's own source so the checksums recorded in the
// provenance are of the code that was actually compiled, not of whatever
// happens to be on disk when the tool runs. A distributed binary carries them
// with it.
//
//go:embed *.go
var sources embed.FS

// RepoURL is where the recorded commit can be read.
const RepoURL = "https://github.com/cbeauhilton/usps-states"

// Build is everything needed to answer "which code produced this file".
//
// Three identities, because they answer different questions and only two of
// them can be known before the artefact is committed:
//
//	Sources      per-file sha256 of the //go:embed'd source. Exact, and free of
//	             the fixed point below.
//	Binary       sha256 of the executable that ran. Proves the compiler, flags
//	             and toolchain too, which the source digests cannot. Stable
//	             across commits ONLY when built -trimpath -buildvcs=false;
//	             otherwise Go bakes vcs.revision into the binary and the digest
//	             moves every time you commit. Measured, not assumed.
//	Revision     the git commit. READABLE — it is what lets a person open the
//	             source in a browser — but it cannot describe the commit that
//	             contains this file, so it is always the parent. Absent
//	             entirely under -buildvcs=false, which is why Ref exists.
type Build struct {
	Module    string            `json:"module"`
	Version   string            `json:"version"`
	GoVersion string            `json:"go_version"`
	Revision  string            `json:"vcs_revision,omitempty"`
	Committed string            `json:"vcs_time,omitempty"`
	Dirty     bool              `json:"vcs_modified"`
	Stamped   bool              `json:"vcs_stamped"`
	OS        string            `json:"goos,omitempty"`
	Arch      string            `json:"goarch,omitempty"`
	Sources   map[string]string `json:"sources"`      // filename -> sha256
	Deps      map[string]string `json:"dependencies"` // module@version -> go.sum hash

	// Binary is the sha256 of the executable that produced the artefact, and
	// Reproducible reports whether it was built such that anyone else can
	// arrive at the same digest from the same source.
	Binary       string `json:"binary_sha256,omitempty"`
	Trimpath     bool   `json:"trimpath"`
	Reproducible bool   `json:"binary_reproducible"`

	// Ref is the git ref the published links should use. A commit sha cannot
	// name the commit that contains this artefact; a tag or branch name can,
	// because we choose it before committing and create it after.
	Ref string `json:"ref"`
}

func readBuild() Build {
	b := Build{Sources: map[string]string{}, Deps: map[string]string{}}

	ents, _ := sources.ReadDir(".")
	for _, e := range ents {
		data, err := sources.ReadFile(e.Name())
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		b.Sources[e.Name()] = hex.EncodeToString(sum[:])
	}

	if exe, err := os.Executable(); err == nil {
		if data, err := os.ReadFile(exe); err == nil {
			sum := sha256.Sum256(data)
			b.Binary = hex.EncodeToString(sum[:])
		}
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	b.Module, b.Version, b.GoVersion = bi.Main.Path, bi.Main.Version, bi.GoVersion
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision, b.Stamped = s.Value, true
		case "vcs.time":
			b.Committed = s.Value
		case "vcs.modified":
			b.Dirty = s.Value == "true"
		case "GOOS":
			b.OS = s.Value
		case "GOARCH":
			b.Arch = s.Value
		case "-trimpath":
			b.Trimpath = s.Value == "true"
		}
	}
	// Reproducible when the two sources of build-to-build variance are gone:
	// the absolute build path (-trimpath) and the stamped commit (-buildvcs=
	// false, which is exactly why Stamped is false).
	b.Reproducible = b.Trimpath && !b.Stamped

	for _, d := range bi.Deps {
		if d.Sum != "" {
			b.Deps[d.Path+"@"+d.Version] = d.Sum
		}
	}
	return b
}

// Warning is the honest caveat to put in the artefact, or "" when there is none.
//
// `go run` does not stamp VCS information, and a build from a modified tree is
// not reproducible from the recorded commit. Both are worth saying out loud in
// the file rather than leaving a reader to notice the field is missing.
func (b Build) Warning() string {
	switch {
	case !b.Stamped:
		return "NOT REPRODUCIBLE: no VCS information was stamped into this binary. " +
			"`go run` does not stamp it; build with `go build` from a clean checkout."
	case b.Dirty:
		return "NOT REPRODUCIBLE: built from a working tree with uncommitted changes, " +
			"so the recorded commit does not describe the code that ran."
	}
	return ""
}

// ref is the git ref published links resolve against.
//
// A commit sha cannot name the commit that contains this artefact — a file
// cannot hold the hash of the commit holding it. A ref NAME can, because we
// choose it before committing and create it afterwards. So links use the ref
// and the sha is recorded separately as the (parent) commit the code was built
// from, which is a different and weaker claim, honestly labelled.
func (b Build) ref() string {
	if b.Ref != "" {
		return b.Ref
	}
	return "main"
}

// CommitURL points at the tree the published links resolve against.
func (b Build) CommitURL() string { return RepoURL + "/tree/" + b.ref() }

// SourceURL points at one file at that ref. This is the link to READ, and it
// moves when the ref moves — that is what a ref is for.
func (b Build) SourceURL(name string) string { return RepoURL + "/blob/" + b.ref() + "/" + name }

// PermalinkURL points at one file at the exact revision this build resolved,
// and is empty when there is no honest one to give.
//
// A permalink into a commit built from a modified working tree would be a lie
// with a checksum attached: the commit exists, but it does not contain the code
// that ran. Better to publish the ref alone and let Warning say why.
func (b Build) PermalinkURL(name string) string {
	if !b.Stamped || b.Dirty || b.Revision == "" {
		return ""
	}
	return RepoURL + "/blob/" + b.Revision + "/" + name
}

// PermalinkTree is PermalinkURL for the repository as a whole.
func (b Build) PermalinkTree() string {
	if !b.Stamped || b.Dirty || b.Revision == "" {
		return ""
	}
	return RepoURL + "/tree/" + b.Revision
}

func (b Build) sortedSources() []string {
	out := make([]string, 0, len(b.Sources))
	for k := range b.Sources {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (b Build) sortedDeps() []string {
	out := make([]string, 0, len(b.Deps))
	for k := range b.Deps {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// provenanceTurtle emits the software agent, the plan it followed, and the
// dependency closure — the part that answers "which code ran", as opposed to
// "what did it read".
//
// This block is deliberately NOT covered by the version identifier in the data
// graph. It describes the toolchain, and a new compiler is not a new edition of
// the abbreviations.
//
// The commit recorded is the one the code was built from, which is the PARENT
// of the commit that will contain this artefact. That is the correct thing to
// record: it identifies the code that ran. It is recorded on the plan, which is
// an Entity, because that is what a revision identifies — prov:value is defined
// as the value of an entity, and an Agent is not one.
func (b Build) provenanceTurtle() string {
	var s strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&s, f+"\n", a...) }

	p(":extraction prov:qualifiedAssociation [")
	p("        a prov:Association ;")
	p("        prov:agent   :software ;")
	p("        prov:hadPlan :plan ] .")
	p("")
	p(":software a prov:SoftwareAgent ;")
	p("    dct:title      %q ;", "usps-states")
	p("    dct:identifier %q ;", b.Module)
	p("    pav:version    %q ;", b.Version)
	p("    dct:source     <%s> ;", b.CommitURL())
	if u := b.PermalinkTree(); u != "" {
		p("    dct:hasVersion <%s> ;", u)
	}
	if b.Binary != "" {
		p("    # sha256 of the executable that ran. Reproducible by a third party")
		p("    # only when built -trimpath -buildvcs=false; %v here.", b.Reproducible)
		p("    spdx:checksum  :ck_binary ;")
	}
	if b.Committed != "" {
		p("    dct:created    %q^^xsd:dateTime ;", b.Committed)
	}
	if w := b.Warning(); w != "" {
		p("    rdfs:comment   %q ;", w)
	}
	p("    dct:requires   %q .", b.GoVersion+" "+b.OS+"/"+b.Arch)
	if b.Binary != "" {
		p("")
		p(":ck_binary a spdx:Checksum ;")
		p("    spdx:algorithm    spdx:checksumAlgorithm_sha256 ;")
		p("    spdx:checksumValue %q .", b.Binary)
	}
	p("")
	p("# The plan is the source that ran. Each file is checksummed from the copy")
	p("# embedded in the binary at compile time, so these describe the compiled")
	p("# code and not whatever is on disk now.")
	p(":plan a prov:Plan, prov:Entity ;")
	p("    dct:source <%s> ;", b.CommitURL())
	if u := b.PermalinkTree(); u != "" {
		p("    dct:hasVersion <%s> ;", u)
	}
	if b.Stamped {
		p("    # the revision resolved when this ran — the commit the code was BUILT")
		p("    # from, which is the parent of the commit containing this file")
		p("    pav:version %q ;", b.Revision)
	}
	parts := make([]string, 0, len(b.Sources))
	for _, name := range b.sortedSources() {
		parts = append(parts, ":src_"+strings.ReplaceAll(name, ".", "_"))
	}
	p("    dct:hasPart %s .", strings.Join(parts, ", "))
	p("")
	for _, name := range b.sortedSources() {
		id := ":src_" + strings.ReplaceAll(name, ".", "_")
		p("%s a prov:Entity ;", id)
		p("    dct:title %q ;", name)
		p("    dct:source <%s> ;", b.SourceURL(name))
		if u := b.PermalinkURL(name); u != "" {
			p("    dct:hasVersion <%s> ;", u)
		}
		ck := ":ck_src_" + strings.ReplaceAll(name, ".", "_")
		p("    spdx:checksum %s .", ck)
		p("%s a spdx:Checksum ;", ck)
		p("    spdx:algorithm    spdx:checksumAlgorithm_sha256 ;")
		p("    spdx:checksumValue %q .", b.Sources[name])
	}
	if len(b.Deps) > 0 {
		p("")
		p("# Dependency closure with the go.sum hashes the build verified. These are")
		p("# Go module dirhashes (base64, h1: prefix), NOT raw sha256 digests, so they")
		p("# are recorded as go.sum values rather than mislabelled as spdx checksums.")
		p("# SLSA records the same shape: a uri, and a digest keyed by algorithm name.")
		for i, d := range b.sortedDeps() {
			id := fmt.Sprintf(":dep%d", i)
			p("%s a prov:Entity ;", id)
			p("    dct:identifier %q ;", d)
			p("    dct:conformsTo <https://go.dev/ref/mod#go-sum-files> ;")
			p("    prov:value     %q .", b.Deps[d])
			p(":plan dct:requires %s .", id)
		}
	}
	return s.String()
}
