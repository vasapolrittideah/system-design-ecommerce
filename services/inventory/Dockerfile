# syntax=docker/dockerfile:1

# Build context is the repository root, not this directory: one go.mod covers
# every service, so a context scoped to services/inventory cannot see the module
# it belongs to.

# ------------------------------------------------------------------------------
# build — every cmd/ in this service, compiled once
# ------------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

# Set by buildx from --platform, and declared here so the RUNs below can read
# them. The build stage deliberately stays on the builder's own architecture
# and cross-compiles instead, which is what keeps an image built for two
# architectures from costing two emulated compiles — Go needs nothing but
# these two variables to do it.
ARG TARGETOS
ARG TARGETARCH

# Must match GOOSE_VERSION in the Makefile. Both the Makefile and the Tiltfile
# pass it as a build arg, so the default here is only what a bare `docker build`
# would use.
ARG GOOSE_VERSION=v3.27.3

WORKDIR /src

# Dependencies resolve from go.mod alone, so they land in their own layer and
# survive every change that is not a dependency change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY pkg/ ./pkg/
COPY gen/ ./gen/
COPY services/inventory/ ./services/inventory/

# CGO off so the binary is static and runs on a distroless image with no libc.
# -trimpath keeps the build host's paths out of panic traces, which otherwise
# name a directory that exists on nobody's machine.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' \
      -o /out/ ./services/inventory/cmd/...

# Installed here rather than in the migrate stage so that stage needs no Go
# toolchain and stays a few megabytes.
#
# Built in a throwaway module rather than with `go install pkg@version`, which
# is shorter but has nowhere to put a cross-compiled binary that the COPY below
# can name: it refuses to run at all with GOBIN set, and without GOBIN it
# writes to an architecture-suffixed directory that exists only when the target
# differs from the builder.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir /goose && cd /goose && go mod init goose >/dev/null && \
    go get github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION} && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /tools/goose \
      github.com/pressly/goose/v3/cmd/goose

# ------------------------------------------------------------------------------
# server — the gRPC server, and every other binary this service ships
# ------------------------------------------------------------------------------
#
# One image holds all of cmd/, and each workload picks its binary. The reaper
# is the same code, the same dependencies, and the same configuration as the
# server — it differs only in what calls the use cases — so giving it its own
# image would mean two builds and two tags that must never drift apart. Its
# Deployment sets `command: ["/app/reaper"]`; this one takes the default.
FROM gcr.io/distroless/static-debian12:nonroot AS server

# Matches runAsUser/runAsGroup in the Deployment. Stated here too so the image
# is safe to run without a pod spec supplying it.
USER 65532:65532

COPY --from=build /out/ /app/

# No shell in this image, so this is exec form by necessity as well as by
# preference: the process is PID 1 and receives SIGTERM directly, which is what
# the graceful drain depends on.
ENTRYPOINT ["/app/server"]

# ------------------------------------------------------------------------------
# migrate — goose plus this service's migrations, run as a Job
# ------------------------------------------------------------------------------
#
# Alpine rather than distroless because the Job assembles its DSN in a shell
# from the same environment the server reads, which keeps the host, user, and
# database name defined exactly once.
FROM alpine:3.22 AS migrate

RUN apk add --no-cache ca-certificates

COPY --from=build /tools/goose /usr/local/bin/goose
COPY services/inventory/db/migrations/ /migrations/

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/goose"]
