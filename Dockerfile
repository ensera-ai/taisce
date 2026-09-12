# The service image.
#
# Two stages, because the toolchain is a build dependency and shipping it would put a compiler and a
# module cache inside the thing that serves memory. What runs is a static binary and nothing else:
# no shell, no package manager, and no interpreter for anything that gets in to use.

FROM golang:1.27.1-alpine AS build
WORKDIR /src

# Modules first, so a change to the code does not re-download the module graph on every build. This
# is the layer that makes a rebuild seconds rather than minutes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off so the result runs on a distroless base with no libc to match. Trimpath so the binary does
# not embed the build machine's directory layout, which is both noise and a small disclosure.
# The version the binary reports is the release tag the workflow passes; a local build says so.
ARG VERSION=unknown
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" -o /taisce ./cmd/taisce

# Distroless rather than alpine: there is no shell to exec into and no package manager to install
# with, so a foothold in the container has nothing to build on. The cost is that debugging needs a
# sidecar rather than `docker exec sh`, which is the right trade for a process holding a customer's
# memory.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /taisce /taisce

# Non-root by default. Root in a container is one escape away from root on the host, and nothing this
# process does needs it.
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/taisce"]
CMD ["serve"]
