# -*- mode: Python -*-
#
# The local development loop: infrastructure and services in the k3d cluster,
# rebuilt and redeployed as files change.
#
#	make cluster-create   # once
#	tilt up               # or: make dev
#
# Kubernetes locally rather than docker compose is what lets the couplings this
# repo actually cares about be exercised before production — a headless Service
# with client-side gRPC balancing, readiness removing a pod from the endpoint
# list, migrations as a Job, a drain that fits inside the grace period.
#
# There is deliberately no live update here, and it is worth saying why, because
# syncing a host-compiled binary into the running container is the usual reason
# to reach for Tilt. It was tried. Making it work costs four things: the Go
# sources have to leave the image build context or every edit invalidates it and
# the sync never fires; the server has to run a different distroless variant
# locally, because the restart wrapper needs a `touch` the minimal image does
# not have; readOnlyRootFilesystem has to be off, because the sync writes into
# the container; and the binary has to be compiled twice, once on the host and
# once in the image.
#
# What it would buy, against a rebuild measured rather than assumed: editing a
# Go file and waiting for the new binary to serve takes about eleven seconds —
# nine to build and push, the rest to roll the pod — because BuildKit caches
# the module download and the compile, and the registry is a container on the
# same machine. Live update would take that to two or three. Four divergences
# from production is a poor price for eight seconds, in a repo whose entire
# reason for running Kubernetes locally is not having them.
#
# Re-measure before revisiting this. The trade changes if the build slows down.

# ------------------------------------------------------------------------------
# Guards
#
# Both failures below are cheap to hit and expensive to diagnose from the error
# Tilt would otherwise produce.
# ------------------------------------------------------------------------------

# Tilt refuses non-local clusters by default; this narrows it to this cluster in
# particular, so a kubectl context left pointing somewhere else cannot be
# deployed into by a tool whose whole purpose is to apply on every keystroke.
allow_k8s_contexts([])
if k8s_context() != 'k3d-ecommerce':
    fail('kubectl context is %s, expected k3d-ecommerce — run: make cluster-create' % k8s_context())

# kustomize fails on the missing file with a path and no explanation. Both
# halves are checked because they are written by the same command but land in
# different overlays — a tree from before bff-web existed has the private key
# and not the public one, and only re-running `make keys` fixes it.
for key in [
    'deploy/k8s/overlays/local/identity/jwt-private.pem',
    'deploy/k8s/overlays/local/bff-web/jwt-public.pem',
]:
    if not os.path.exists(key):
        fail('%s missing — run: make keys' % key)

# ------------------------------------------------------------------------------
# Infrastructure
#
# The namespace is applied separately because deploy/k8s/infra deliberately
# excludes it: `make down` has to be able to remove the workloads without taking
# the PersistentVolumeClaim with them.
# ------------------------------------------------------------------------------

k8s_yaml('deploy/k8s/infra/namespace.yaml')
k8s_yaml(kustomize('deploy/k8s/infra'))

k8s_resource(
    'jaeger',
    port_forwards=['16686:16686'],
    labels=['infra'],
)

k8s_resource(
    'reloader-reloader',
    new_name='reloader',
    labels=['infra'],
)

# No port_forward: the gateway is reached on localhost:8000 through the k3d load
# balancer, which is the same path a browser takes and the only one that
# exercises the Service, the upstream, and the routes. Forwarding straight to
# the pod would skip all three.
#
# Editing deploy/k8s/infra/kong/kong.yml is enough to apply a routing change —
# Tilt re-runs kustomize, the ConfigMap content changes, and Reloader restarts
# the pod.
k8s_resource(
    'kong',
    labels=['infra'],
)

# ------------------------------------------------------------------------------
# identity
# ------------------------------------------------------------------------------

k8s_yaml(kustomize('deploy/k8s/overlays/local/identity'))

# identity's own Postgres instance, not a shared one. It comes from the local
# overlay because production has no equivalent object — the instance is managed
# there and only the host differs.
k8s_resource(
    'identity-postgres',
    port_forwards=['5432:5432'],
    labels=['identity'],
)

docker_build(
    'ecommerce/identity',
    context='.',
    dockerfile='services/identity/Dockerfile',
    target='server',
    build_args={'GOOSE_VERSION': 'v3.27.3'},
    # Everything the image needs and nothing else, so that editing a manifest
    # or another service does not rebuild this one.
    only=['go.mod', 'go.sum', 'pkg', 'gen', 'services/identity'],
)

docker_build(
    'ecommerce/identity-migrate',
    context='.',
    dockerfile='services/identity/Dockerfile',
    target='migrate',
    build_args={'GOOSE_VERSION': 'v3.27.3'},
    # The same list as the server image even though this stage only copies
    # migrations: it is built from the same `build` stage, which needs the
    # module to compile goose, and a narrower context fails on the COPY.
    only=['go.mod', 'go.sum', 'pkg', 'gen', 'services/identity'],
)

k8s_resource(
    'identity-migrate',
    resource_deps=['identity-postgres'],
    labels=['identity'],
)

k8s_resource(
    'identity',
    # The schema has to exist before the pool's readiness check can mean
    # anything, and Reloader has to be up before a config change can restart
    # anything — generated ConfigMaps and Secrets carry fixed names here, so
    # kubectl apply alone rolls nothing.
    resource_deps=['identity-migrate', 'reloader'],
    port_forwards=[
        '50051:50051',
        '9090:9090',
    ],
    labels=['identity'],
)

# ------------------------------------------------------------------------------
# bff-web
#
# No Postgres and no migration Job: the BFF has no database. Its only stateful
# dependency is identity, reached over gRPC.
# ------------------------------------------------------------------------------

k8s_yaml(kustomize('deploy/k8s/overlays/local/bff-web'))

docker_build(
    'ecommerce/bff-web',
    context='.',
    dockerfile='services/bff-web/Dockerfile',
    target='server',
    # Everything the image needs and nothing else, so that editing a manifest
    # or another service does not rebuild this one.
    only=['go.mod', 'go.sum', 'pkg', 'gen', 'services/bff-web'],
)

k8s_resource(
    'bff-web',
    # identity does not have to be *up* for this to serve — an unreachable one
    # is a 503, not a failed start — but ordering it after the rollout means
    # the first request in a fresh cluster hits a service that exists.
    resource_deps=['identity', 'reloader'],
    port_forwards=[
        '8080:8080',
        '9091:9090',
    ],
    labels=['bff'],
)

# ------------------------------------------------------------------------------
# Buttons
#
# Manual triggers rather than watchers. Each of these regenerates committed
# files, and a generator that fires on every keystroke produces a working tree
# that changes while it is being read.
# ------------------------------------------------------------------------------

local_resource(
    'proto',
    'make proto',
    deps=['proto'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
    labels=['generate'],
)

local_resource(
    'sqlc',
    'make sqlc',
    deps=['services/identity/db'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
    labels=['generate'],
)

local_resource(
    'test',
    'make test',
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
    labels=['check'],
)

local_resource(
    'lint',
    'make lint',
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False,
    labels=['check'],
)
