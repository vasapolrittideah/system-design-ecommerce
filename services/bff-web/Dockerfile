# syntax=docker/dockerfile:1

# Build context is the repository root, not this directory: one go.mod covers
# every service, so a context scoped to services/bff-web cannot see the module
# it belongs to.
#
# There is no migrate stage here, and there will not be one. The BFF has no
# database — it fans out, merges, and shapes responses — so it ships a single
# binary and no schema.

# ------------------------------------------------------------------------------
# build
# ------------------------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies resolve from go.mod alone, so they land in their own layer and
# survive every change that is not a dependency change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY pkg/ ./pkg/
COPY gen/ ./gen/
COPY services/bff-web/ ./services/bff-web/

# CGO off so the binary is static and runs on a distroless image with no libc.
# -trimpath keeps the build host's paths out of panic traces, which otherwise
# name a directory that exists on nobody's machine.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
      -o /out/ ./services/bff-web/cmd/...

# ------------------------------------------------------------------------------
# server
# ------------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS server

# Matches runAsUser/runAsGroup in the Deployment. Stated here too so the image
# is safe to run without a pod spec supplying it.
USER 65532:65532

COPY --from=build /out/ /app/

# No shell in this image, so this is exec form by necessity as well as by
# preference: the process is PID 1 and receives SIGTERM directly, which is what
# the graceful drain depends on.
ENTRYPOINT ["/app/server"]
