# syntax=docker/dockerfile:1

# Build context is the repository root, not this directory: one go.mod covers
# every service, so a context scoped to services/catalog cannot see the module
# it belongs to.

# ------------------------------------------------------------------------------
# build — every cmd/ in this service, compiled once
# ------------------------------------------------------------------------------
FROM golang:1.26-alpine AS build

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
COPY services/catalog/ ./services/catalog/

# CGO off so the binary is static and runs on a distroless image with no libc.
# -trimpath keeps the build host's paths out of panic traces, which otherwise
# name a directory that exists on nobody's machine.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
      -o /out/ ./services/catalog/cmd/...

# Installed here rather than in the migrate stage so that stage needs no Go
# toolchain and stays a few megabytes.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go install -trimpath github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION}

# ------------------------------------------------------------------------------
# server — the gRPC server, and every other binary this service ships
# ------------------------------------------------------------------------------
#
# One image holds all of cmd/, and each workload picks its binary. A worker or
# an outbox relay is the same code, the same dependencies, and the same
# configuration as the server; giving each its own image would mean three
# builds and three tags that must never drift apart.
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

COPY --from=build /go/bin/goose /usr/local/bin/goose
COPY services/catalog/db/migrations/ /migrations/

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/goose"]
