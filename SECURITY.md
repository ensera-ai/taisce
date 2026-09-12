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

Every release of the service is built by the workflow in `.github/workflows/release.yml` from a
tagged commit on the public repository, and nothing else is a release:

- the container image `ghcr.io/ensera-ai/taisce:vX.Y.Z`, signed with Sigstore keyless signing by
  the workflow's identity and carrying a SLSA build provenance attestation and an SBOM;
- the Helm chart as an OCI artifact under `ghcr.io/ensera-ai/charts/taisce`, signed the same way;
- the `taisce` binary for Linux and macOS with checksums and a provenance attestation on the release
  page, built with `-trimpath` and no VCS stamp so the same tag builds the same bytes.

Verify before running:

```sh
cosign verify ghcr.io/ensera-ai/taisce:vX.Y.Z \
  --certificate-identity-regexp '^https://github.com/ensera-ai/taisce/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify oci://ghcr.io/ensera-ai/taisce:vX.Y.Z --owner ensera-ai
```

The adapters are published by their own repositories' release workflows: PyPI through trusted
publishing (no long-lived token), NuGet and Maven Central through scoped credentials held only as
repository secrets, each with a provenance attestation. Every account that can publish has
two-factor authentication on.

## What is deliberately not promised

No release process defends against a maintainer who controls the repository, the workflow and the
registry account together. The controls above make a release traceable to a public commit and a
workflow identity, and make a package that did not come from there distinguishable; they do not
make a compromised maintainer account harmless. Two-factor authentication and a small set of
publishers are the answer to that, and they are policy, not code.
