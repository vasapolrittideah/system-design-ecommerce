#!/usr/bin/env bash
#
# Load-test a deployed stack through the gateway.
#
#	scripts/load/run.sh [scenario]     # me (default) | login
#	make load SCENARIO=login RATE=100 DURATION=2m
#
# It asks a different question from `make smoke`. That one asks whether the
# deploy serves at all; this asks how it behaves while it is being pushed — does
# the HPA add replicas, does Kong's ring balancer send anything to the ones it
# added, does the 800ms budget hold, and what gives way first when it does not.
#
# The absolute numbers are not a capacity result and should never be quoted as
# one. The generator, the cluster, and every database share one laptop, and the
# local overlays request 10m of CPU per pod so that the autoscaler is reachable
# at all. What transfers to production is the shape: which limit is met first,
# which error the system answers with, and whether scaling out changes it.
#
# Traffic goes through Kong on 8000 for the reason the smoke test does — reaching
# bff-web on a port-forward would skip the routes, the upstream, and the Service,
# which is most of what this exists to exercise.
#
# The rate limiter
#
# Kong allows 300 requests a minute globally and 10 on the auth writes. Those are
# limits set for people rather than for a generator: run against them and this
# measures the limiter and nothing behind it. So the run raises both and puts
# them back afterwards, including when k6 fails or the run is interrupted.
#
# The raised config is derived from deploy/k8s/infra/kong/kong.yml by rewriting
# the two numbers in it, never a second copy of the file, so there is nothing to
# drift from what the cluster actually runs. What it deliberately is not is a
# route or a header that skips the plugin: that is a rate limit anyone can opt
# out of, and it would be one merge away from being true in production.
#
# One interaction to know about: `make dev` re-applies this ConfigMap whenever
# kong.yml changes on disk, so editing it mid-run puts the real limits back under
# the generator. That is what the gateway_rate_limited counter reports.
#
# LIMITER=keep leaves Kong alone, for the run whose subject is the limiter.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LOAD_DIR="${ROOT_DIR}/scripts/load"
KONG_CONFIG="${ROOT_DIR}/deploy/k8s/infra/kong/kong.yml"

SCENARIO="${1:-${SCENARIO:-me}}"
BASE_URL="${BASE_URL:-http://localhost:8000}"
BASE_URL="${BASE_URL%/}"
NAMESPACE="${NAMESPACE:-ecommerce}"
LIMITER="${LIMITER:-open}"

# Offered load, not achieved load: k6 starts an iteration on a schedule rather
# than waiting for the last one to finish, so a system that slows down builds a
# queue instead of quietly receiving less traffic. A closed model would hide
# exactly the behaviour this is here to show.
RATE="${RATE:-50}"
DURATION="${DURATION:-1m}"
WARMUP="${WARMUP:-20s}"
USERS="${USERS:-10}"

# Every request carries this as its X-Correlation-ID prefix, which is what makes
# a slow one findable afterwards in Jaeger and in bff-web's logs.
RUN_ID="load-$(date +%s)"

info() { printf '  %s\n' "$*"; }
fail() {
    printf '\033[31m  FAIL\033[0m %s\n' "$1" >&2
    shift
    for line in "$@"; do printf '       %s\n' "$line" >&2; done
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "$1 not found" "install it with: $2"
}

SCRIPT="${LOAD_DIR}/${SCENARIO}.js"
if [[ ! -f "$SCRIPT" ]]; then
    available="$(cd "$LOAD_DIR" && ls -1 ./*.js 2>/dev/null | sed 's|^\./||; s|\.js$||' | grep -v '^lib$' | tr '\n' ' ')"
    fail "no such scenario: ${SCENARIO}" "available: ${available:-none}"
fi

need k6 "brew install k6"

# ------------------------------------------------------------------------------
# The limiter
# ------------------------------------------------------------------------------

# kong_config_matches FILE — true when the cluster is already running that file.
kong_config_matches() {
    local live
    live="$(kubectl -n "$NAMESPACE" get configmap kong-declarative \
        -o jsonpath='{.data.kong\.yml}' 2>/dev/null || true)"
    [[ -n "$live" && "$live" == "$(cat "$1")" ]]
}

# apply_kong_config FILE LABEL — patch the declarative config and wait for the
# gateway to be serving it.
#
# A patch rather than an apply on purpose: the ConfigMap belongs to the infra
# kustomization, and re-applying a hand-built copy of it here would drop the
# labels that kustomization adds and leave the object owned by nobody.
apply_kong_config() {
    local file="$1" label="$2" generation patch waited=0

    if kong_config_matches "$file"; then
        info "kong config already ${label}"
        return
    fi

    generation="$(kubectl -n "$NAMESPACE" get deployment kong -o jsonpath='{.metadata.generation}')"
    patch="$(jq -Rs '{data: {"kong.yml": .}}' <"$file")"
    kubectl -n "$NAMESPACE" patch configmap kong-declarative --type merge -p "$patch" >/dev/null

    # DB-less Kong reads the file once at startup, so the restart is the change
    # taking effect. Reloader is what performs it — the same mechanism every
    # config change in this repo relies on — and the fallback exists because a
    # rollout that never happens would otherwise look like a limit that never
    # lifted, with the run reporting 429s instead of the reason.
    until [[ "$(kubectl -n "$NAMESPACE" get deployment kong -o jsonpath='{.metadata.generation}')" != "$generation" ]]; do
        sleep 2
        waited=$((waited + 2))
        if ((waited >= 40)); then
            info "reloader has not restarted kong in ${waited}s — restarting it here"
            kubectl -n "$NAMESPACE" rollout restart deployment/kong >/dev/null
            break
        fi
    done

    kubectl -n "$NAMESPACE" rollout status deployment/kong --timeout=180s >/dev/null

    # rollout status returns when the new pod is Ready, and the old one is still
    # in the endpoint list for its preStop sleep. That pod is running the config
    # this just replaced, with the request counter it had already filled — so a
    # run that starts here spends its first seconds being refused by a limit it
    # believes it lifted. Waiting for the pod count to come back down to the
    # replica count is waiting for that pod to be gone.
    local desired
    desired="$(kubectl -n "$NAMESPACE" get deployment kong -o jsonpath='{.spec.replicas}')"
    waited=0
    until [[ "$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=kong \
        --no-headers 2>/dev/null | wc -l | tr -d ' ')" == "$desired" ]]; do
        sleep 2
        waited=$((waited + 2))
        ((waited >= 60)) && break
    done

    info "kong config ${label}"
}

limiter_opened=0

restore_limiter() {
    [[ "$limiter_opened" == "1" ]] || return 0
    limiter_opened=0
    printf '\nrestoring the gateway rate limits\n'
    apply_kong_config "$KONG_CONFIG" "restored from deploy/k8s/infra/kong/kong.yml"
}

open_limiter() {
    need kubectl "brew install kubectl"
    need jq "brew install jq"

    local opened
    opened="$(mktemp)"
    # Only the rate-limiting plugins spell `minute:` in this file, so matching
    # the key is enough and there is no YAML parser in the dependency list.
    sed -E 's/^([[:space:]]*)minute: [0-9]+$/\1minute: 1000000/' "$KONG_CONFIG" >"$opened"

    if ! grep -q 'minute: 1000000' "$opened"; then
        rm -f "$opened"
        fail "found no rate limit to raise in ${KONG_CONFIG}" \
            "The plugin config changed shape. Check it and update the sed in this script," \
            "or run with LIMITER=keep to leave the gateway alone."
    fi

    printf '\nraising the gateway rate limits for this run\n'
    trap restore_limiter EXIT INT TERM
    limiter_opened=1
    apply_kong_config "$opened" "raised"
    rm -f "$opened"
}

# wait_for_gateway blocks until the stack answers something other than "nothing
# is here yet".
#
# Kong has just been restarted to pick up the raised limits, and a pod that is
# Ready is not in its ring balancer until the DNS record it resolved expires. The
# 502s from that window are a few seconds long and would otherwise land inside
# the run, where they are indistinguishable from the system failing under load —
# except that they would be the first thing measured and would fail the error
# rate threshold on their own.
wait_for_gateway() {
    local attempt=0 status
    while true; do
        status="$(curl --silent --output /dev/null --max-time 5 --write-out '%{http_code}' \
            "${BASE_URL}/load-warmup-does-not-exist" || true)"
        case "$status" in
            000 | 502 | 503) ;;
            *) break ;;
        esac

        attempt=$((attempt + 1))
        if ((attempt >= 20)); then
            fail "the gateway is not serving after 20 attempts (last: ${status})" \
                "Is the stack up? Try: make up && make dev" \
                "Or: kubectl -n ${NAMESPACE} get pods,endpoints -l app.kubernetes.io/name=bff-web"
        fi
        sleep 3
    done
    info "gateway serving"
}

# ------------------------------------------------------------------------------

printf 'load: %s scenario against %s\n' "$SCENARIO" "$BASE_URL"
printf '      %s req/s offered for %s after a %s warm-up, %s users\n' "$RATE" "$DURATION" "$WARMUP" "$USERS"
printf '      correlation ids start with %s\n' "$RUN_ID"

case "$LIMITER" in
    open) open_limiter ;;
    keep) info "leaving the gateway rate limits alone (LIMITER=keep)" ;;
    *) fail "LIMITER must be open or keep, got '${LIMITER}'" ;;
esac

wait_for_gateway
printf '\n'

status=0
k6 run \
    --env "BASE_URL=${BASE_URL}" \
    --env "RUN_ID=${RUN_ID}" \
    --env "RATE=${RATE}" \
    --env "DURATION=${DURATION}" \
    --env "WARMUP=${WARMUP}" \
    --env "USERS=${USERS}" \
    "$SCRIPT" || status=$?

# Before the pointers below rather than in the trap alone, so that the last thing
# on screen is where to look next and not a gateway restart. The trap stays as
# the path that runs when k6 was interrupted and never returned here.
restore_limiter

# Printed on both paths: a failed threshold is the run's finding, and the places
# that explain it are the same ones either way.
#
# The correlation ID is on the log line and not on the span — nothing here puts
# it on one — so a slow request is chased log first, trace second.
cat <<EOF

where the rest of the answer is:
  kubectl -n ${NAMESPACE} get hpa,pods
      did it scale, and did the pods it added receive anything
  make logs SVC=bff-web | grep ${RUN_ID}
      this run's requests, each line carrying the trace_id behind it
  make port-forward SVC=identity
      grafana :3000, jaeger :16686, prometheus :9092
EOF

exit "$status"
