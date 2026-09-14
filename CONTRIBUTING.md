<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Contributing to Taisce

Thank you for helping. This page covers how to build and test Taisce, how to propose a change, and
the contributor agreement.

Taisce is open source under Apache-2.0: the service, the portal, the adapters and the clients. The
service lives at [github.com/ensera-ai/taisce](https://github.com/ensera-ai/taisce). The adapters have
their own repositories: `ensera-ai/taisce-python`, `ensera-ai/taisce-java` and
`ensera-ai/taisce-dotnet`.

## Build and test

You need Go (the version in `go.mod`), Docker, and Node.js 24, the active LTS, if you want to build
the documentation site.

```sh
make db-up    # build and start the PostgreSQL image on port 55432
make test     # the full suite against that database, with the coverage check
make site     # build the documentation site (needs the database too)
```

- `make db-up` builds Taisce's own PostgreSQL image, with the extensions the schema needs, and waits
  until it answers. `make db-down` removes it.
- `make test` runs every package with `-count=1` and cross-package coverage, then checks coverage
  against the project's floor. It points the suite at the database from `make db-up`. If you run
  `go test` yourself without `TAISCE_TEST_DSN`, the database tests skip, and a green run that skipped
  them proves nothing. Use `make test`.
- `make site` generates the reference pages from the code and a freshly migrated database, then
  builds the site with Docusaurus. It refuses a broken link or a page the navigation does not reach.
  `make site-serve` serves the result at `http://127.0.0.1:3000/taisce/`.

### Running the whole stack from your working tree

`compose.yaml` names published images and never builds, so `docker compose up` runs a release rather
than your change. To run what you have written, add the overlay:

```sh
docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

The two files are separate on purpose. Compose given both an `image:` and a `build:` builds only when
the image is missing from the local cache, which would make "am I running my change or a release?" a
question about cache state. Naming the overlay makes it a question you answered.

The build is tagged `taisce:dev` and `taisce-postgres:dev`, never with a release's tag. The local
image cache is shared by every install on the machine, so a build written to
`ghcr.io/ensera-ai/taisce:v0.5.1` would be what any other install runs the next time it is recreated.
Leave the overlay off and Compose runs the release again. If a machine already holds a build under a
release tag, `docker compose pull` in that install's directory brings the release back.

Other targets you may need:

- `make gate` runs the same checks a change must pass before it merges (licence headers, the full
  suite with coverage, `go vet`, and the API reference check) in a clean Linux container.
- `make contract` regenerates the API reference from the code. If you change the shape of an API
  operation, run it and include the diff in your pull request.
- `make test-inference` runs the tests that need a real model. It costs money and sends test text to
  whichever provider you configure, so it only runs when you ask. Profiles are in `deploy/inference/`.
  The API key comes from your environment, never from a file.

## Propose a change

1. **Open an issue first** on [github.com/ensera-ai/taisce/issues](https://github.com/ensera-ai/taisce/issues)
   for anything beyond a small fix. Say what problem you are solving. For a design change, say what
   else you considered and why you chose this.
2. **Open a pull request** against `main`. Keep one change per pull request. Link the issue.
3. **Show that it works.** Name the tests that prove the change, so a reviewer can re-run them. If you
   claim something is faster, include how you measured it and on what hardware.
4. **Say what you left out.** If part of the problem is not solved by your change, say so in the pull
   request.
5. **Add yourself to the contributors.** In your first pull request, add your name and GitHub login
   to [`site/src/data/contributors.js`](site/src/data/contributors.js). If you like, add a square
   picture at `site/static/img/contributors/<login>.jpg`. The documentation site's Contributors
   section is built from that list.

If you find a security problem, do not open a public issue. Follow [SECURITY.md](SECURITY.md).

## Code style

- **Tests run against a real PostgreSQL.** No mocks of the database. A test must not skip when its
  dependency is missing; it must fail. Every path that refuses something needs a test that makes it
  refuse.
- **Coverage does not go down.** `make test` checks cross-package coverage against a floor, and the
  floor only moves up. New code is covered by new tests.
- **Every package has a doc comment** that says what the package decides and what it must not decide.
  The site build refuses a package without one.
- **Comments explain why, not what.** When you make a choice, name the alternative you rejected and
  why.
- **Prompt text lives in YAML**, in `internal/infra/inference/prompts/`, and is compiled into the
  binary with `go:embed`. It is never read from disk at runtime. If you change a prompt, run
  `make test-inference` to check the regression corpus, then `make golden` to update the golden file.
- **Every Go file starts with the licence header.** `make test` checks it:

  ```go
  // Copyright 2026 The Taisce Authors
  // SPDX-License-Identifier: Apache-2.0
  ```

  Documentation pages start with the same two lines as HTML comments.
- **Logic belongs in the service, not in an adapter.** An adapter maps a framework's types onto an
  API call and maps the answer back. Retrieval, supersession, compaction and erasure stay behind the
  API, so a rule is written once and tested once rather than copied into every language.
- **Keep private details out.** Issues, commit messages, comments and docs are all public. Do not
  include endpoints, credentials, connection strings or real people's words.

## The contributor agreement

Before we can merge your first contribution, you need to accept the
[Individual Contributor License Agreement](docs/cla/individual.md). If you do the work as part of
your job, your employer signs the [Corporate Contributor License Agreement](docs/cla/corporate.md)
instead and names you on it. Work you create at your job usually belongs to your employer, and an
agreement you were not entitled to sign protects nobody.

**You keep the copyright in everything you write.** The agreement is a licence, not a transfer.

**Why an agreement and not just a sign-off.** Apache-2.0 already licenses your contribution to the
project (section 5), patent grant included. What the agreement adds is a signed record of who owns
what, which a legal review asks for and which is needed to act against someone who infringes. It also
records that you wrote the contribution, or have the right to submit it. Apache-2.0 section 5 only
licenses a contribution as far as you were able to license it, so that is the one gap it leaves.

Ensera intends Taisce to be and stay Apache-2.0, but the agreement does not bind Ensera to that.
Read section 2 before you sign, and do not sign if a differently licensed edition would not be
acceptable to you.

### What signing records about you

Signing records **your name, the email address and GitHub account you sign with, and the date**. If
your employer signs the corporate agreement, it records the same for the person who signs plus the
list of people it covers.

It is kept for as long as the project distributes your contribution, because it is the evidence that
you granted the licence. That means **it cannot be deleted on request while your contribution is
still in the project**; removing it would mean removing the code it covers. It is used for nothing
else: no analytics, no mailing list, and it is not shared with anyone except the service that runs
the signing.

## Who maintains Taisce

Taisce is developed and maintained by **Baserat Al Mustaqbal**, an establishment registered in
Jordan, trading as **Ensera**. The public repository gets one commit per release.
