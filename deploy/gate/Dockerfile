# Copyright 2026 The Taisce Authors
# SPDX-License-Identifier: Apache-2.0
#
# The environment `make gate` runs the suite in.
#
# It exists to reproduce what the hosted runner gave that the developer's machine does not: Linux, and
# the tools the tests shell out to. The Go image already carries make, git, python3 and bash. The one
# thing it lacks is helm, which the chart tests run, and a test that skips when its tool is absent is
# a green run that asserted nothing — so the tool is installed rather than the test excused.
#
# The Go version is the one go.mod pins. Change them together.
FROM golang:1.27.1-bookworm

# Helm is pinned and verified rather than installed by a script fetched at build time. A pipe into a
# shell runs whatever the server returns that day; a pinned archive checked against its published
# digest before it is unpacked runs what was reviewed. Both architectures, because the gate runs on
# an arm64 laptop today and a hosted runner is amd64.
#
# The major version matches the one the suite has been passing against on the developer's machine
# (Helm 4). Moving to a new major is a change to what the chart tests exercise, made on purpose.
ARG HELM_VERSION=v4.3.0
ARG HELM_SHA256_ARM64=31c5794dd55c66a51e6b7d2e2ac7a114ae8b1de41ff1d9ba51748ac973b06a08
ARG HELM_SHA256_AMD64=86584a54def73570558f66f5111cc53dfed56689637ae32c1201205d494f54fb
ARG TARGETARCH
RUN set -eu; \
    case "${TARGETARCH:-$(dpkg --print-architecture)}" in \
      arm64) sum="${HELM_SHA256_ARM64}"; arch=arm64 ;; \
      amd64) sum="${HELM_SHA256_AMD64}"; arch=amd64 ;; \
      *) echo "no pinned helm digest for this architecture" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/helm.tgz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${arch}.tar.gz"; \
    echo "${sum}  /tmp/helm.tgz" | sha256sum -c -; \
    tar -xzf /tmp/helm.tgz -C /tmp "linux-${arch}/helm"; \
    install -m 0755 "/tmp/linux-${arch}/helm" /usr/local/bin/helm; \
    rm -rf /tmp/helm.tgz "/tmp/linux-${arch}"; \
    helm version --short

# The clone the gate mounts is owned by the invoking user, and git refuses to work in a repository a
# different uid owns. The clone is created fresh for each run and discarded, so trusting it is not a
# weakening: nothing else writes there.
RUN git config --system --add safe.directory '*'
