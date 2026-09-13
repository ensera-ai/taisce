<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Security

Taisce holds people's words and hands them to models. An adapter runs inside your agent with your
credential and sees every observation and every recall on its way past. That is the most privileged
position in the system, and it is why the release process below is stricter than a library's.

## Reporting a vulnerability

Report privately through GitHub's vulnerability reporting on the repository the problem is in:
`ensera-ai/taisce` for the service, chart and plugin, `ensera-ai/taisce-python`, `ensera-ai/taisce-dotnet`
or `ensera-ai/taisce-java` for an adapter ("Security" → "Report a vulnerability"). Do not open a public
issue for something exploitable, and do not send memory content you found exposed: describe how it
could be reached.

We acknowledge a report within three working days, say what we found within ten, and credit the
reporter in the fix's release notes unless asked not to. A fix ships as a patch release on the
current minor line; older lines are not patched.

## What a release is

Every release of the service is built by `.github/workflows/release.yml` from a tagged commit on this
repository, and nothing else is a release:

- `ghcr.io/ensera-ai/taisce:vX.Y.Z`, the service image, for `linux/amd64` and `linux/arm64`;
- `ghcr.io/ensera-ai/taisce-postgres:vX.Y.Z`, the PostgreSQL substrate the compose file runs;
- the Helm chart, `oci://ghcr.io/ensera-ai/charts/taisce` at version `X.Y.Z`;
- the `taisce` binary for Linux and macOS, with checksums, on the release page, built with
  `-trimpath` and no VCS stamp so the same tag builds the same bytes.

Both images and every binary carry a build provenance attestation. The images and the chart carry a
Sigstore keyless signature made with the workflow's own identity — there is no signing key to steal —
and the images carry an SBOM.

## What that provenance is worth: SLSA v1.0 Build Level 2

**What the levels mean.** SLSA v1.0 Build Level 2 requires that the build ran on a hosted build
platform and that the provenance is tied to that platform by a signature. Build Level 3 adds that the
platform keeps runs from influencing each other and keeps the secret material used to sign the
provenance out of reach of the build's own steps. Source: [SLSA v1.0 levels](https://slsa.dev/spec/v1.0/levels),
read 2026-09-13.

**What GitHub provides.** GitHub's documentation states that artifact attestations by themselves
provide Build Level 2, and that Level 3 needs the build moved into a reusable workflow isolated from
the workflow that calls it. Source:
[Artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations),
read 2026-09-13.

**What this release reaches: Build Level 2, not Level 3.** The job that runs `go build`,
`docker buildx` and `helm package` is the same job that holds the identity used to sign and attest.
Why that is the choice rather than an oversight is in [D6](docs/01-decisions.md).

**What Level 2 gives you.** An artifact that verifies below was built on GitHub's hosted runners by
this repository's release workflow, at the tag you name. One built anywhere else — a laptop, a fork,
another workflow, another tag — does not verify.

**What it does not give you.**
- It does not show the build steps were unaltered. Anyone who can change the workflow can change what
  it builds, and the result is signed and attested like any other release.
- It does not show an artifact is safe. A signature and an attestation say where something was built,
  not what it does.

## Verifying a release yourself

The release runs these same checks against what it has just pushed, and publishes its release page
only if they pass.

Provenance — built by this repository's release workflow, from this tag:

```sh
gh attestation verify oci://ghcr.io/ensera-ai/taisce:vX.Y.Z \
  --repo ensera-ai/taisce \
  --signer-workflow ensera-ai/taisce/.github/workflows/release.yml \
  --source-ref refs/tags/vX.Y.Z
```

The same command verifies `oci://ghcr.io/ensera-ai/taisce-postgres:vX.Y.Z`, and a downloaded binary
when you pass its path instead of an `oci://` reference.

Signature — made by that workflow's identity at that tag, for an image or the chart:

```sh
cosign verify ghcr.io/ensera-ai/taisce:vX.Y.Z \
  --certificate-identity https://github.com/ensera-ai/taisce/.github/workflows/release.yml@refs/tags/vX.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Name the exact identity and tag. A pattern that accepts any workflow or any ref in the repository
accepts artifacts this release did not produce.

## The adapters

Each adapter repository publishes through its own release workflow:

- **PyPI** (`taisce-python`): trusted publishing, one environment per package, no stored token.
- **NuGet** (`taisce-dotnet`): trusted publishing from the `nuget` environment, which admits only
  version tags and waits for a reviewer; no stored key.
- **Maven Central** (`taisce-java`): Central requires PGP signatures and issues no short-lived
  credential, so an expiring signing subkey and an expiring token are stored — only in the
  `maven-central` environment, which admits only version tags and waits for a reviewer, and only for a
  job that runs no build tool. That repository's `SECURITY.md` says how to verify its releases.

Every account that can publish has two-factor authentication on.

## What is deliberately not promised

No release process defends against a maintainer who controls the repository, the workflow and the
registry account together. The controls above make a release traceable to a public commit and a
workflow identity, and make a package that did not come from there distinguishable; they do not
make a compromised maintainer account harmless. Two-factor authentication and a small set of
publishers are the answer to that, and they are policy, not code.
