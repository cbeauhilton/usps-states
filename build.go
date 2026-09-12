package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// RepoURL is where the recorded commit can be read.
const RepoURL = "https://github.com/cbeauhilton/usps-states"

// Build names the program that wrote an artefact: the module, the version Go
// stamped into the binary (a tag, a pseudo-version, or "(devel)" under
// `go run`, with "+dirty" when the tree had uncommitted changes), the git
// commit it was built at, and the ref the published links resolve against.
//
// The commit is the one the code was BUILT from, which is the parent of the
// commit that will contain this artefact — a file cannot hold the hash of the
// commit holding it. A ref NAME can, because we choose it before committing
// and create it afterwards. So links use the ref, and the commit is recorded
// separately as the weaker claim it is.
type Build struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Revision string `json:"vcs_revision,omitempty"`
	Ref      string `json:"ref"`
}

func readBuild() Build {
	b := Build{Ref: publishRef}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	b.Module, b.Version = bi.Main.Path, bi.Main.Version
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			b.Revision = s.Value
		}
	}
	return b
}

// writeBuild records which build wrote dist/ and the ref its links resolve
// against. Only the `generate` command writes it: `verify` must leave the tree
// exactly as it found it.
func writeBuild() error {
	j, err := json.MarshalIndent(readBuild(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("evidence/build.json", append(j, '\n'), 0o644)
}

// blobURL is where to READ one file, at the ref the links resolve against.
func blobURL(path string) string { return RepoURL + "/blob/" + publishRef + "/" + path }

// stamp is the Turtle block naming the program that wrote the file. It sits
// after the content graph and outside it on purpose: verify compares
// everything above it byte for byte and only reports a change here, because a
// new build of this tool is not a new edition of the abbreviations.
func (b Build) stamp() string {
	var s strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&s, f+"\n", a...) }
	p("# The program that wrote this file. Everything above reproduces from")
	p("# data/usps-states.csv at any commit; this block names the build that ran.")
	p(":software a prov:SoftwareAgent ;")
	p("    dct:title      %s ;", lit("usps-states"))
	p("    dct:identifier %s ;", lit(b.Module))
	p("    pav:version    %s ;", lit(b.Version))
	if b.Revision != "" {
		p("    dct:hasVersion <%s/tree/%s> ;", RepoURL, b.Revision)
	}
	p("    dct:source     <%s/tree/%s> .", RepoURL, b.Ref)
	return s.String()
}
