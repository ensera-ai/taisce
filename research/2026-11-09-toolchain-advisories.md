<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# The toolchain the build actually uses, and what it carries — 2026-11-09

Part of #162. Two questions were open: whether the standard library this module is built against
carries advisories this code reaches, and whether the version that builds it is recorded at all.

Both were answered by running something. The first answer settles whether moving the pin is a
security fix or hygiene. The second was not on the issue, and is the more serious of the two.

**Method.** `govulncheck@v1.9.0`, advisory database `https://vuln.go.dev`, read 2026-11-09. Four
scans: two trees — `73e61f4` (`main`) and `6e8c065` (the head of #163, which moves
`golang.org/x/text` to v0.43.0) — each under both toolchains, selected with `GOTOOLCHAIN`. A clean
worktree per run, no local edits. `govulncheck` reports a vulnerability as affecting the code only
when it finds a call path to the vulnerable symbol, so every count below is reachability rather than
presence.

## What the scans say

| Tree | Toolchain | Reachable | Exit |
|---|---|---|---|
| `73e61f4` (`main`) | 1.27.1 | **13** — 12 standard library, 1 `golang.org/x/text` | 3 |
| `73e61f4` (`main`) | 1.27.3 | **1** — `golang.org/x/text` only | 3 |
| `6e8c065` (#163) | 1.27.1 | **12** — standard library only | 3 |
| `6e8c065` (#163) | 1.27.3 | **0** | 0 |

**Fact.** The claim #162 recorded as unverified is true. Under 1.27.1 this code reaches twelve
advisories in the standard library it is built against:

| Advisory | Package | Fixed in |
|---|---|---|
| `GO-2026-6809` | `crypto/x509` | go1.27.3 |
| `GO-2026-6617`, `GO-2026-6612`, `GO-2026-6611`, `GO-2026-6610`, `GO-2026-6603` | `net/http/internal/http2` | go1.27.2 |
| `GO-2026-6613`, `GO-2026-6605` | `net/http` | go1.27.2 |
| `GO-2026-6608` | `mime/multipart` | go1.27.2 |
| `GO-2026-6607` | `crypto/tls` | go1.27.2 |
| `GO-2026-6600`, `GO-2026-6599` | `html/template` | go1.27.2 |

The call paths are not incidental. `GO-2026-6600` and `GO-2026-6599` are reached through
`api.Portal.render` calling `template.Template.ExecuteTemplate` — the portal renders operator-facing
HTML, so a context-tracking defect in `html/template` sits on the surface an operator looks at. The
`http2` paths arrive through `api.writeJSON`, `notify.Sender.Send` and the server's own
`http.Server.Serve`: the read path, the outbound notification path, and every request the service
answers.

**Fact.** Moving the pin to 1.27.3 clears all twelve, and clears the eleven fixed in 1.27.2 as well
as the `crypto/x509` one that needs 1.27.3 specifically. With #163 also in, the scan exits 0.

**So:** the toolchain move is a security fix in its own right, not hygiene, and it is the larger half
of the reachable-advisory problem — twelve against #163's one. #162's own framing stands: the gate and
the pin move together, because a `vuln` target switched on under 1.27.1 fails on the standard library
on the first run. The order is the pin first, then the gate, and the gate is then green from the
commit that adds it.

## The pin does not pin — #171

**Fact.** `go.mod` carries `go 1.27.1` on line 7 and **no `toolchain` line** — on `main` and on the
#163 head. The comment above it says "a `go` directive plus a `toolchain` line means the version this
module is built with is a decision recorded in the file rather than whatever the builder happens to
have". The second half of that sentence describes what the file does not do.

A `go` directive alone is a *minimum*. With `GOTOOLCHAIN=auto`, which is the default, a builder
holding a newer release builds with the newer release and reports success. Measured here: `go
version` in this worktree answers `go1.27.3 darwin/arm64` with `GOTOOLCHAIN=auto` and the module
unchanged.

**Fact.** What the hosted build uses is 1.27.1, and it is 1.27.1 by a different mechanism than the
comment claims. `.github/workflows/test.yml`, `release.yml` and `docs.yml` pass
`go-version-file: go.mod` to `actions/setup-go`, which reads the `go` directive when no `toolchain`
line is present and installs that version exactly. Run 42596463428 logs `Setup go version spec
1.27.1`, then `go version go1.27.1 linux/amd64` and `GOVERSION='go1.27.1'`. `Dockerfile` and
`deploy/gate/Dockerfile` pin `golang:1.27.1-*` as image tags, independently of `go.mod`.

**Inference.** Every artefact this project publishes — the release binary, the image, the gate
container — is therefore built against the standard library carrying those twelve advisories, while a
developer's local `go build` and `go test` may use any newer toolchain installed on the machine. The
two agree today only because three separate places happen to name the same number.

**The defect is not the missing digit; it is that nothing fails when they disagree.** A `toolchain`
line is what makes the local build refuse to proceed on the wrong version instead of quietly
succeeding. Without it the file records an intention that only CI honours, and the comment asserting
otherwise is the kind of claim rule 6 exists to stop: it was written, never run.

This is #171. It is separate from #162 because it is true at whatever version #162 settles on: the
pin has to exist before the number in it is worth arguing about.

## What this hands to #168

#168 reports `make test` failing the coverage floor on macOS on a commit CI called green. This finding
supplies a mechanism that costs nothing to test: the macOS run and the CI run were not using the same
toolchain, because nothing made them. Whether a different toolchain counts statements differently is a
separate question, measured and recorded below.

## The meter, measured

#162 would not accept two summary percentages, and it was right not to. What follows is two coverage
profiles from the same tree under the two toolchains, reduced to a statement-level account of what
changed.

**Method.** Tree `73e61f4`, unmodified. One machine (darwin/arm64), one substrate container built from
`deploy/postgres` at `0.1.0` on a port of its own, so nothing else running on this host could touch
it. Command per run, exactly what `make test` runs:

```
GOTOOLCHAIN=go1.27.X make test TEST_DSN=postgres://postgres:taisce@localhost:55462/taisce?sslmode=disable COVER_PROFILE=cov-1.27.X.out
```

Both profiles are `mode: set`. Each was aggregated per block — `-coverpkg=./...` emits one line per
block per test binary — and compared by block span.

| | 1.27.1 | 1.27.3 |
|---|---|---|
| Wall clock | 311s | 398s |
| Blocks | 8702 | 8702 |
| Statements counted | 13077 | **12438** |
| Statements covered | 12085 | 11448 |
| Statements uncovered | 992 | 990 |
| Reported coverage | **92.4%** | **92.0%** |
| `coverage-gate.sh` | passes the 92.3 floor | fails it |

**Fact.** The claim #162 recorded as unverified is true, and the figures are the two it predicted:
92.4% under 1.27.1, 92.0% under 1.27.3, same tree, same suite, same database.

**Fact.** Nothing became untested. Both runs instrument the same 8702 blocks with the same spans — no
block exists in one profile and not the other. No block went from covered to uncovered. Two
statements went the other way, from uncovered to covered, in `internal/formation/driver.go` and
`internal/infra/pg/factreceipt.go`, which is suite nondeterminism and not the effect being measured.

**Fact.** The entire difference is a statement count inside blocks that were covered in both runs. 336
blocks across 61 files carry a lower count under 1.27.3: 1163 statements against 524, a fall of 639.
Every one of those 336 blocks was executed in both runs. No uncovered block changed count at all.

That is why the ratio falls. The 639 statements the newer toolchain stops counting were all covered
ones, so they were removed from the numerator and the denominator together — uncovered stayed at 992
against 990 — and a ratio above 92% drops when you take equal amounts off both ends of it.

The widest cases, by statements lost:

| Block | 1.27.1 | 1.27.3 |
|---|---|---|
| `internal/api/contract.go:43.2,44.1` | 17 | 1 |
| `internal/api/contract.go:77.2,88.1` | 17 | 2 |
| `internal/api/contract.go:45.2,59.1` | 17 | 2 |
| `internal/api/contract.go:60.2,76.1` | 17 | 3 |
| `internal/api/contract.go:89.2,131.31` | 17 | 9 |
| `internal/docsite/schema.go:295.2,296.1` | 9 | 1 |
| `internal/notify/signature.go:36.2,38.1` | 8 | 2 |

**Inference.** The counts move where a single statement spans many source lines. `Contract()` is one
`strings.Builder` filled by a handful of calls, each passing a string literal concatenated across a
dozen or more lines; under 1.27.1 four of its blocks are each counted as 17 statements, under 1.27.3
as 1, 2, 2 and 3. The 1.27.1 figure tracks the lines the expression occupies. The 1.27.3 figure tracks
the statements in it. On that reading the newer toolchain is the more honest meter, and the older one
inflated both ends of the ratio wherever the code wraps an expression — which this codebase does
freely, because its string literals carry prose.

**Unverified.** That this is a deliberate change announced in 1.27.2 or 1.27.3. #162 searched the
release notes and the `internal/coverage` documentation on 2026-11-09 and found nothing describing
it, and this pass found nothing either. The behaviour is measured; its provenance is not.

### What this settles

**#168 is this.** `make test` failing the floor on macOS on a commit CI called green is not a macOS
property and not a flake: the local run used 1.27.3 and CI used 1.27.1, because nothing makes them
agree (#171). The same tree gives 92.0 and 92.4 depending only on which toolchain ran it.

**The floor is a number with no basis recorded.** 92.3 was set by runs under 1.27.1, against a meter
that counted line-wrapped continuations of covered statements as statements. It is not a figure the
corrected meter can reach on this tree, and no test anyone could write would close that gap, because
nothing on this tree is untested that was tested before. Writing tests to lift 92.0 back over 92.3
would be the exact move #162 forbids.

So the floor becomes a pair — the percentage and the toolchain that produced it — re-measured in the
commit that moves the pin, and that is a decision for `docs/01-decisions.md` rather than a quiet edit
to `coverage.floor`. Re-check the highest D-number against the remote immediately before writing it.

### Artefacts

The two profiles are 16 MB each and are not committed: a 32 MB pair of generated files in the history
buys nothing that the command above does not reproduce on demand, and the reduction that carries the
argument is the table in this document. What is kept is the exact tree, the exact command, the block
and statement totals, and the per-block figures for every case named above.
