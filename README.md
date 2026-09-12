# usps-states

The 62 USPS state, possession and military abbreviations, derived from the
authority and published as data — with the derivation attached.

```
dist/CodeSystem-usps-states.json   FHIR CodeSystem, content: complete
dist/usps-states.ttl               SKOS + PROV-O + PAV, the full derivation chain
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

The repository is private until its owner makes it public, so these downloads
return 404 until then.

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

and the resource itself passes the HL7 FHIR validator (`validator_cli` 6.10.4,
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
ends with a stamp naming the binary that wrote it — module, version, commit,
ref — and that legitimately differs between builds. So the check compares the
Turtle with the stamp removed and reports a changed stamp instead of failing on
it. A hand-edited artefact still fails, loudly.

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

### Offline

`extract` and `verify` ask the authority by default. `-offline` reads the
committed `evidence/28apb.htm` instead and never touches the network:

```sh
go run . verify -offline
```

Every run prints which it used — `LIVE` or `OFFLINE`. A tool that silently
answers from a local copy is how "we checked the authority" becomes a claim
nobody can trust, which is the exact failure this repository was built around.

The tests need no flag: they parse the same `evidence/28apb.htm`, so they pass
with the network unplugged.

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

`dist/usps-states.ttl` ends with a build stamp: the module path, the version Go
stamped into the binary, the commit it was built at, and the ref the published
links resolve against. The same four fields are in `evidence/build.json`.

```turtle
:software a prov:SoftwareAgent ;
    dct:identifier "github.com/cbeauhilton/usps-states" ;
    pav:version    "v0.1.0" ;
    dct:hasVersion <…/tree/31bd0ff…> ;   # the commit the code was BUILT at
    dct:source     <…/tree/v0.1.0> .     # the ref the links resolve against
```

The commit recorded is the one the code was *built* from, which is the parent
of the commit containing the artefact: a file cannot hold the hash of the
commit holding it. A ref name can, because it is chosen before committing and
created after, so the links use the ref. Everything above the stamp reproduces
from `data/usps-states.csv` at any commit, and `verify` checks that it does.

### Version

`pav:version` is the edition of the abbreviations. USPS publishes no version and
no changelog, so this repository mints one: the date the content last changed,
recorded beside the source's sha256 in `evidence/source.json`. Re-running
`extract` against an unchanged page holds the version and updates only the
fetch date, because "last verified" and "last changed" are different facts.

`dist/SHA256SUMS` identifies the bytes. Two copies of an artefact with the same
sum are the same file; two with different sums are not, and `verify` says
whether the difference is in the data or only in the build stamp.

For what it is worth, it has not moved. `evidence/history.json` records one
measurement, taken on 2026-09-04: of the 90 captures of the page the Internet
Archive held, one per calendar year was fetched — 14 samples, 2012-09-22 to
2026-01-10 — and the codes extracted from each. 62 codes every time, 0 changes
to the code set. The comparison was on extracted **concepts**, not on bytes:
the page's bytes differ in nearly every capture while the codes do not, and the
Archive's own "Temporarily Offline" page returns a 200 that would parse as an
absence, so a sample with no codes would have been recorded as unusable rather
than as a change. None was. The code that took the measurement is not kept; the
file is the fact.

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
