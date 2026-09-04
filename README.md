# usps-states

The 62 USPS state, possession and military abbreviations, derived from the
authority and published as data — with the derivation attached.

```
dist/CodeSystem-usps-states.json   FHIR CodeSystem, content: complete
dist/usps-states.ttl               SKOS + PROV-O + PAV, the full derivation chain
dist/usps-states.nt                the same graph, RDFC-1.0 canonical form
dist/usps-states.ndjson            one concept per line
data/usps-states.csv               the source of truth, reviewable in a diff
dist/SHA256SUMS
```

## Why this exists

FHIR's US Core binds `Address.state` to `us-core-usps-state`, a value set
defined as *every code in `https://www.usps.com/`*. Six profiles across US Core
and mCODE bind it — Patient, Practitioner, Organization, Location, and a
jurisdiction extension — so it is every postal address in a record.

Nobody publishes those codes as data.

| asked | answer |
|---|---|
| **USPS**, the authority | all 62, **as HTML** |
| HL7 Terminology | `CodeSystem` with `content: not-present` — identity, zero concepts, USPS's copyright quoted |
| FHIR core R4 and R5 | no CodeSystem |
| VSAC | 68 code systems, USPS is not one |
| UMLS Metathesaurus | not a source vocabulary |
| CDC PHIN VADS | nothing postal or state |
| US Census / ANSI INCITS 38 | **57 of 62** — missing `AA AE AP FM MH PW` |

Every US government vocabulary registry is organised around *health*
vocabularies, and postal codes are general infrastructure that healthcare
borrowed. The one government body that does publish state codes is answering a
different question: **ANSI codes states, USPS codes mailing destinations**,
which is why ANSI has no `AA`/`AE`/`AP` for military mail and no `FM`/`MH`/`PW`
for the freely associated states.

So implementers scrape the page, ask a terminology server, or type the list in
by hand. This does the first one, once, in the open, and shows its working.

## Use it

```sh
# a release, pinned — this file will not change under you
curl -O https://raw.githubusercontent.com/cbeauhilton/usps-states/v0.1.0/dist/CodeSystem-usps-states.json

# or track the latest
curl -O https://raw.githubusercontent.com/cbeauhilton/usps-states/main/dist/CodeSystem-usps-states.json
```

That is a FHIR `CodeSystem` with `content: complete` and 62 concepts inline —
load it straight into a terminology server and `us-core-usps-state` expands.

Verified, not assumed. Against HAPI FHIR:

```
PUT /CodeSystem/usps-states                    201
GET /ValueSet/us-core-usps-state/$expand       200  total 62, returned 62
GET /CodeSystem/$lookup?code=MA                     "Massachusetts"
GET /ValueSet/$validate-code?...&code=MA            result: true
```

and the resource itself passes the HL7 FHIR validator (`validator_cli` 6.10.3,
R4) with no errors and no warnings.

If you want the provenance to travel with the data, take the Turtle. It says
what was derived, from what URL, from what bytes by sha256, when, by what
activity, and under whose claim — so a copy that gets separated from this
repository still answers those questions:

```sparql
PREFIX pav:  <http://purl.org/pav/>
PREFIX spdx: <http://spdx.org/rdf/terms#>
SELECT ?url ?sum WHERE {
  ?scheme pav:importedFrom  ?data .
  ?data   pav:importedFrom  ?source .
  ?source pav:retrievedFrom ?url ;
          spdx:checksum/spdx:checksumValue ?sum .
}
```

PAV rather than bare PROV because the two steps are different kinds of
derivation and saying so costs nothing: `pav:retrievedFrom` is a resource
fetched **as served**, `pav:importedFrom` is one **transformed** to fit another
model. Both are subproperties of `prov:wasDerivedFrom`, so a consumer that only
knows PROV still walks the whole chain.

## How it works

```sh
go run . extract    # fetch the authority, parse it, write data/ and evidence/
go run . generate   # build dist/ from data/usps-states.csv
go run . verify     # re-derive and fail if anything moved
```

`verify` checks two things. That the authority still says what `data/` says, and
that everything in `dist/` reproduces from `data/`. A published artefact that
cannot be re-derived is the thing this repository exists to avoid. CI runs it
weekly.

The reproducibility check is about the **data**, not the build. `usps-states.ttl`
embeds the commit and source digests of the binary that wrote it, and that
legitimately differs between builds — so the check compares the Turtle with its
provenance block removed, and reports a changed stamp instead of failing on it.
A hand-edited artefact still fails, loudly.

### When the page changes but the codes do not

Expect this. The page carries navigation, banners and analytics that move
constantly — the Internet Archive holds **90 distinct digests** of it, over which the
code set never changed once. Diffing the page bytes to decide whether the
terminology moved would cry wolf every month.

So `verify` diffs the **concepts**, not the bytes, and a cosmetic change passes.
But it also notices, and says so:

```
verified: 62 concepts, identical to the authority

  NOTE: the page changed but the codes did not.
    recorded a83a610a… (66912 bytes)
    live     4f2c9e17… (67104 bytes)
  Cosmetic churn — navigation, banners, analytics. The published provenance
  still cites the recorded digest, which no longer resolves. Run
  `usps-states extract && usps-states generate` to refresh it.
```

That matters because the Turtle publishes the source digest as provenance. Once
the page moves, that digest no longer describes anything a reader can fetch. The
claim was true when made and is now unverifiable, which is worth a sentence
rather than silence.

For that reason the provenance chain is anchored to the **committed extract**,
not only to the page:

```
:source  ─ the fetched HTML, sha256, volatile
   │         pav:retrievedFrom <https://pe.usps.com/…>  — as served
   ↓ pav:importedFrom
:data    ─ data/usps-states.csv, sha256, stable and reviewable
   ↓ pav:importedFrom
:scheme  ─ the 62 concepts
             prov:hadPrimarySource :source  — Publication 28 DEFINES these
```

A reader who cannot reproduce `:source` can still verify `:data` against the
committed CSV, and re-run `verify` to confirm the live page still yields it.

If the codes themselves change, `verify` fails with a `+`/`-`/`~` diff and a
human decides what it means. That is the case worth stopping for.

### Not hitting the publisher forty times an hour

`extract` and `verify` cache the fetched page under `.cache/` (gitignored).

```sh
go run . verify              # cache if younger than an hour, else live
go run . verify -refresh     # always ask the authority
go run . verify -offline     # cached copy at any age; never touch the network
go run . verify -ttl 24h
```

Every run prints which it used — `LIVE` or `CACHED (12m30s old)`. A tool that
silently answers from a cache is how "we checked the authority" becomes a claim
nobody can trust, which is the exact failure this repository was built around.
For the same reason a network failure never falls back to a stale copy on its
own; it tells you the copy exists and makes you ask for it.

A cached body whose digest no longer matches what was recorded with it is
treated as corruption and refetched, not served.

The tests need none of this: they parse the committed `evidence/28apb.htm`, so
they pass with the network unplugged.

### Three traps, all of them real

**The page 404s a curl-shaped `User-Agent`** and returns 200 to anything else.
Three separate surveys recorded Appendix B as gone because nobody opened the
404 — it was 54 KB of HTML. There is nothing to spoof here; saying who you are
is enough, and this tool does.

**`NE` is defined twice on the same page.** Nebraska in the State/Possession
table, Northeast in the Geographic Directional table. Tables are therefore
selected by their header text, never by position, the directional table is
excluded by name, and `parse` fails loudly if that table disappears — because
its absence means the page was restructured and every other assumption needs
rechecking.

**USPS writes `AA` without a space**: `Armed Forces Americas(except Canada)`.
`tx.fhir.org`'s expansion has the space. We reproduce the authority verbatim,
typo included. A silent correction is a difference nobody can audit later.

### Which code produced this file

`dist/usps-states.ttl` records the program as well as the data. The build stamps
in the git commit, the commit time, whether the tree was dirty, the Go version,
and a sha256 for every source file — taken from the copy **embedded in the
binary at compile time**, so the digests describe the code that actually ran
rather than whatever is on disk now. Dependencies are listed with the `go.sum`
hashes the build verified.

```sparql
PREFIX prov: <http://www.w3.org/ns/prov#>
PREFIX pav:  <http://purl.org/pav/>
PREFIX dct:  <http://purl.org/dc/terms/>
SELECT ?module ?version ?revision WHERE {
  ?activity prov:qualifiedAssociation [ prov:agent ?agent ; prov:hadPlan ?plan ] .
  ?agent dct:identifier ?module ; pav:version ?version .
  ?plan  pav:version ?revision .
}
```

The revision hangs off the **plan**, not the agent: `prov:value` is defined as
the value of an *entity*, and an Agent is not one. A commit identifies the
source that ran, which is the plan.

Two honest caveats are written into the file rather than left to inference. A
binary built with `go run`, or from a tree with uncommitted changes, carries
**`NOT REPRODUCIBLE`** as an `rdfs:comment` and as a comment at the top — a
recorded commit that does not describe the code that ran is worse than no commit
at all. Such a build also publishes **no permalinks at all**, because a
revision-pinned link into a commit built from a modified tree is a lie with a
checksum attached.

And the commit recorded is the one the code was *built* from, which is the
parent of the commit containing the artefact. That is the right thing to record:
it identifies the code that ran, not the commit that stored the result.

Every published link comes in two halves, kept apart on purpose:

```turtle
:src_build_go dct:source     <…/blob/v0.1.0/build.go> ;   # where to READ it
              dct:hasVersion <…/blob/b84a2d9…/build.go> ; # what was RESOLVED
              spdx:checksum  :ck_src_build_go .
```

A digest paired only with a moving ref cannot be checked once the ref moves: you
have a hash and no way to find the bytes it describes. SLSA keeps the same two
facts apart for the same reason — `externalParameters.ref` against
`resolvedDependencies[].digest` — and collapsing them is what makes provenance
decorative.

### Version

There are **two** version identifiers, because there are two questions.

`pav:version` is the edition of the abbreviations. USPS publishes no version and
no changelog, so this repository mints one: the date the content was last
derived, recorded beside the source's sha256. That is the honest handling for a
source with no release identity — the derivation date is the only thing that
moves when the content does.

`dct:hasVersion` is a hash of this exact graph, and it is a
[Trusty URI](https://arxiv.org/abs/1401.5775), module RA:

```
https://github.com/cbeauhilton/usps-states/version/RA…
```

An artefact naming its own hash sounds circular, and the trick is that it is
not: write the identifier with a placeholder, hash the content reading that
placeholder as a single blank space — unambiguous, because a URI cannot
otherwise contain one — then substitute the computed code in. Verification
reverses the substitution and recomputes.

The bytes it is computed over ship as `dist/usps-states.nt`, so nobody has to
reimplement this program to check the identifier:

```sh
# what the file claims
grep -o 'version/RA[A-Za-z0-9_-]*' dist/usps-states.ttl

# recompute it from the published triples
CODE=$(grep -o 'RA[A-Za-z0-9_-]\{43\}' dist/usps-states.ttl | head -1)
sed "s|<https://github.com/cbeauhilton/usps-states/version/$CODE>|<https://github.com/cbeauhilton/usps-states/version/ >|" \
  dist/usps-states.nt | LC_ALL=C sort | sha256sum | cut -d' ' -f1 |
  xxd -r -p | base64 | tr '+/' '-_' | tr -d '=\n' | sed 's/^/RA/'
```

`LC_ALL=C` is load-bearing, not decoration. "Sort the lines" is ambiguous:
under `en_US.UTF-8` GNU `sort` collates by locale rules and returns a different
order, and therefore a different hash. RDFC-1.0 canonical form sorts by Unicode
code point, which for UTF-8 is byte order, which is what `LC_ALL=C` gives you.

That file is **RDFC-1.0 canonical form** — the W3C Recommendation of 2024-05-21,
formerly URDNA2015 — not a recipe invented here. It can be produced by sorting
rather than by running a canonicalisation algorithm only because this graph
contains no blank nodes, which is why the checksums are named resources rather
than the `[ … ]` that DCAT's own examples use. A test fails the build if a blank
node ever returns, and CI re-checks the canonical form against an independent
implementation on every push.

One caveat, found by measuring rather than by reasoning. Hash the **published
bytes**; do not re-derive them by loading the Turtle into an RDF library and
asking it to write N-Triples. Some libraries normalise literals on the way out:
rdflib 7.6.0 rewrites an `xsd:dateTime` of `"…Z"` as `"…+00:00"`, which is the
same instant, a different RDF literal, and a different hash. `Z` is the
canonical form per XML Schema Part 2, so this file keeps it. Canonicalisation
does not rescue you here either — RDFC-1.0 assigns stable labels to blank nodes,
it does not normalise literals, and by then the original term is already gone.

What the identifier covers: everything that identifies **what was published**,
including the revision-pinned links. What it excludes: anything that merely
varies between two builds of the same commit — the binary digest, the compiler
version, the build time. Rebuilding this tool must not mint a new edition of the
abbreviations.

For what it is worth, it has not moved — and that is measured here, not
borrowed. `go run . history` samples the Internet Archive one capture per year,
extracts the codes from each, and diffs them:

```
90 captures, sampling one per year across 14 years
  2012-09-22  62 codes  baseline
  2013-10-14  62 codes  identical
  ...
  2026-01-10  62 codes  identical

14 usable snapshots, 2012-09-22 to 2026-01-10, 0 changes to the code set
```

The full result is in `evidence/history.json`. Two traps are handled, because
both produce a confident wrong answer: the CDX digest is useless here, so the
comparison is on extracted **concepts** rather than bytes; and the Archive
returns its own "Temporarily Offline" page with a 200, which parses as zero
codes and would read as a real absence — such a snapshot is recorded as unusable
rather than as evidence of change.

## Licensing

**This is not a United States Postal Service product and carries no USPS
endorsement.**

The abbreviations are facts. A complete, alphabetically ordered list of 62
two-letter codes has no creative selection or arrangement, so there is nothing
here to copyright, and this repository claims nothing over them. The
compilation, code and derivation are released under
[CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/).

USPS asserts copyright over material on its website. HL7 Terminology quotes that
notice and ships `content: not-present` rather than the codes. That notice is
reproduced here so you can weigh it yourself:

> Material on this site is the copyrighted property of the United States Postal
> Service® (Postal Service™). All rights reserved.

Read in context that is a claim about that website, not about a factual code
list — but HL7's terminology authority looked at the same question and chose
caution, and you should know that before depending on this.

`CodeSystem.url` is `https://www.usps.com/` because that is the identity every
FHIR artefact binds; `hl7.terminology` registers the same string. It identifies
the code system, not the publisher of this file.
