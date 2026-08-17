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

# 9092 on the host, because 9090 and 9091 are taken by the admin ports this pod
# is scraping. Editing prometheus.yml or alerts.yml is enough to apply a change
# — Tilt re-runs kustomize, the ConfigMap content changes, and Reloader restarts
# the pod, the same path a Kong routing change takes.
k8s_resource(
    'prometheus',
    port_forwards=[port_forward(9092, 9090, name='prometheus')],
    labels=['infra'],
)

# Where the three signals meet: metrics from Prometheus, traces from Jaeger, and
# a link between them on every latency panel. Editing a dashboard means editing
# its JSON — the provisioned copies cannot be saved over from the UI, which is
# what keeps them in git rather than in a pod's sqlite.
k8s_resource(
    'grafana',
    port_forwards=[port_forward(3000, 3000, name='grafana')],
    labels=['infra'],
)

# The proxy is deliberately not forwarded: the gateway is reached on
# localhost:8000 through the k3d load balancer, which is the same path a browser
# takes and the only one that exercises the Service, the upstream, and the
# routes. Forwarding straight to the pod would skip all three.
#
# The two ports below are the opposite case — Kong Manager and the Admin API it
# reads are bound to loopback in the pod so that nothing in the cluster can
# reach them, and a forward is the only way in. Manager is read-only under
# DB-less, so it is somewhere to see the routing table Kong actually loaded,
# never somewhere to change it. Its origin and the Admin API URL it calls are
# fixed in the Deployment's env and have to agree with these host ports.
#
# Editing deploy/k8s/infra/kong/kong.yml is enough to apply a routing change —
# Tilt re-runs kustomize, the ConfigMap content changes, and Reloader restarts
# the pod.
k8s_resource(
    'kong',
    port_forwards=[
        port_forward(8002, 8002, name='manager'),
        port_forward(8001, 8001, name='admin api'),
    ],
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
# catalog
#
# The same shape as identity, one file at a time: its own Postgres instance, its
# own migration Job, its own images. Nothing is shared but the pattern — which
# is the point of copying it rather than factoring it into a helper that would
# have to be right about every service before the second one exists.
# ------------------------------------------------------------------------------

k8s_yaml(kustomize('deploy/k8s/overlays/local/catalog'))

k8s_resource(
    'catalog-postgres',
    # 5433 on the host: 5432 is identity's. Two databases on a laptop is what
    # "database per service" costs, and the port collision is where that first
    # shows up.
    port_forwards=[port_forward(5433, 5432, name='postgres')],
    labels=['catalog'],
)

docker_build(
    'ecommerce/catalog',
    context='.',
    dockerfile='services/catalog/Dockerfile',
    target='server',
    build_args={'GOOSE_VERSION': 'v3.27.3'},
    # Everything the image needs and nothing else, so that editing a manifest
    # or another service does not rebuild this one.
    only=['go.mod', 'go.sum', 'pkg', 'gen', 'services/catalog'],
)

docker_build(
    'ecommerce/catalog-migrate',
    context='.',
    dockerfile='services/catalog/Dockerfile',
    target='migrate',
    build_args={'GOOSE_VERSION': 'v3.27.3'},
    # The same list as the server image even though this stage only copies
    # migrations: it is built from the same `build` stage, which needs the
    # module to compile goose, and a narrower context fails on the COPY.
    only=['go.mod', 'go.sum', 'pkg', 'gen', 'services/catalog'],
)

k8s_resource(
    'catalog-migrate',
    resource_deps=['catalog-postgres'],
    labels=['catalog'],
)

k8s_resource(
    'catalog',
    resource_deps=['catalog-migrate', 'reloader'],
    port_forwards=[
        '50052:50051',
        '9093:9090',
    ],
    labels=['catalog'],
)

# ------------------------------------------------------------------------------
# bff-web
#
# No Postgres and no migration Job: the BFF has no database. Everything it
# answers with comes from identity and catalog over gRPC.
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
    # Neither downstream has to be *up* for this to serve — an unreachable one
    # is a 503, not a failed start — but ordering it after their rollouts means
    # the first request in a fresh cluster hits services that exist.
    resource_deps=['identity', 'catalog', 'reloader'],
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
    deps=['services/identity/db', 'services/catalog/db'],
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

# The one check here that runs on its own, because it is what turns a dashboard
# of green boxes into a claim worth reading: the boxes say the pods are Ready,
# and this says the API answered. It goes through the gateway on 8000 rather
# than the port-forwards above, so it exercises the routes and the Service too.
#
# It waits for the gateway itself, so ordering it after bff-web is enough — a
# pod that just became Ready is not in Kong's ring balancer until the record it
# resolved expires.
local_resource(
    'smoke',
    'make smoke',
    resource_deps=['kong', 'bff-web'],
    trigger_mode=TRIGGER_MODE_MANUAL,
    labels=['check'],
)
