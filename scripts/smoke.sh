#!/usr/bin/env bash
#
# Smoke-test a deployed stack through the gateway.
#
#	scripts/smoke.sh [base-url]        # default http://localhost:8000
#	make smoke
#
# It answers one question — is this deploy serving? — and is meant to run after
# `make deploy` and to be cheap enough that nobody skips it. It is not an E2E
# suite: it walks the shortest path that touches every hop and asserts the
# contract each hop is supposed to hold.
#
# Everything goes through the gateway on purpose. Reaching bff-web on its
# port-forward would skip Kong's routes, its upstream, and the Service behind
# it, which is most of what a deploy can break.
#
# What each check is here to catch, since none of it is obvious from the
# assertion alone:
#
#   unrouted path   Kong's catch-all reaches bff-web and httpx answers 404 in
#                   the error shape. Kong answering "no Route matched" instead
#                   means the catch-all is gone.
#   settled         nothing below is asserted until the rollout window is over.
#                   It is the only check that waits on purpose, and the only one
#                   whose failure means "still mid-deploy" rather than "broken".
#   products        the storefront read reaches catalog over east-west gRPC. An
#                   empty catalog still answers `products: []`, so this passes on
#                   a fresh cluster and fails on the thing worth catching: a
#                   missing egress rule, which surfaces as a 503 here and as
#                   nothing at all in any pod's status.
#   register/login  the write path works and the identity service is reachable
#                   over east-west gRPC.
#   me              the ES256 key pair agrees. identity holds the private key in
#                   its overlay and bff-web the public one in its own, written
#                   by the same `make keys` into two places. Regenerate one and
#                   not the other and every pod is Ready, every readiness check
#                   is green, and login answers 401 with nothing reporting why.
#                   This is the check that exists for that.
#   me, no token    the auth middleware rejects in this API's shape rather than
#                   Kong's, which is what `no jwt plugin at the edge` buys.
#   correlation id  an inbound X-Correlation-ID is adopted rather than replaced,
#                   which is what makes one operation one story in the logs.
set -euo pipefail

BASE_URL="${1:-${BASE_URL:-http://localhost:8000}}"
BASE_URL="${BASE_URL%/}"

# Only ever printed, never used to reach anything — this script talks to the
# gateway over HTTP and to no cluster. It exists so that a failure hint names
# the namespace the run was actually about, since staging and prod share a
# cluster and differ by nothing else.
NAMESPACE="${NAMESPACE:-ecommerce}"

# Unique per run: register is not idempotent, and a fixed address would turn the
# second run into a conflict that looks like a broken deploy.
EMAIL="smoke-$(date +%s)-${RANDOM}@example.test"
PASSWORD="smoke-password-1234"

# An inbound correlation ID the gateway and every hop below it must carry back.
CORRELATION_ID="smoke-$(date +%s)-${RANDOM}"

body="$(mktemp)"
headers="$(mktemp)"
trap 'rm -f "$body" "$headers"' EXIT

step=0

info() { printf '  %s\n' "$*"; }
pass() { printf '\033[32m  ok\033[0m   %s\n' "$*"; }

# fail prints what came back before exiting. A smoke failure is read by someone
# who was not watching the deploy, so the response is the message.
fail() {
    printf '\033[31m  FAIL\033[0m %s\n' "$1" >&2
    shift
    for line in "$@"; do
        printf '       %s\n' "$line" >&2
    done
    printf '\n       response body:\n' >&2
    sed 's/^/       /' "$body" >&2 || true
    printf '\n' >&2
    exit 1
}

# request METHOD PATH [json-body] [auth-token]
#
# Writes the body and headers to the temp files and echoes the status code.
request() {
    local method="$1" path="$2" data="${3:-}" token="${4:-}"
    local args=(
        --silent --show-error
        --request "$method"
        --output "$body"
        --dump-header "$headers"
        --write-out '%{http_code}'
        --max-time 15
        --header "X-Correlation-ID: ${CORRELATION_ID}"
    )

    [[ -n "$data" ]] && args+=(--header 'Content-Type: application/json' --data "$data")
    [[ -n "$token" ]] && args+=(--header "Authorization: Bearer ${token}")

    curl "${args[@]}" "${BASE_URL}${path}"
}

# json reads a field out of the last response, or the empty string if absent.
json() { jq -r "${1} // empty" <"$body" 2>/dev/null || true; }

# header reads a response header case-insensitively.
header() {
    tr -d '\r' <"$headers" | awk -v name="$1" 'BEGIN { IGNORECASE = 1 }
        tolower($1) == tolower(name ":") { $1 = ""; sub(/^ /, ""); print; exit }'
}

begin() {
    step=$((step + 1))
    printf '\n[%d] %s\n' "$step" "$1"
}

# A 429 on the auth routes is Kong doing its job, not the deploy failing. The
# route-scoped limit is 10/minute per IP, and locally k3d's load balancer
# spreads one caller over three nodes, so a few runs in a row can reach it.
reject_rate_limit() {
    if [[ "$1" == "429" ]]; then
        fail "rate limited by the gateway (429)" \
            "This is Kong working, not a broken deploy: /api/v1/auth/* allows" \
            "10 requests per minute per IP. Wait a minute and run it again."
    fi
}

printf 'smoke: %s\n' "$BASE_URL"
printf '       correlation id %s\n' "$CORRELATION_ID"

# ------------------------------------------------------------------------------
begin 'gateway answers'

# Kong is reachable before its upstream has an endpoint to route to, and a pod
# that just became Ready is not in the ring balancer until the DNS record it
# resolves expires. Both are transient, so the first check is the one that waits.
attempt=0
connect_attempt=0
until status="$(request GET /smoke-does-not-exist)" && [[ "$status" != "000" ]]; do
    connect_attempt=$((connect_attempt + 1))
    if ((connect_attempt >= 20)); then
        fail "no answer from ${BASE_URL} after 20 attempts" \
            "Is the stack up? Try: make up && make dev" \
            "The gateway is k3d's load balancer on 8000, not a port-forward."
    fi
    sleep 3
done

# A separate budget from the connect loop above, and a much wider one, because
# this wait has a measured floor rather than an unknown cause.
#
# A Kong that resolved its upstream while bff-web had no ready endpoint does not
# pick it up when the endpoint appears — it picks it up on its own cycle, which
# measured at 59 seconds twice, on a laptop cluster, watching a healthy bff-web
# be answered 503 the whole time. Lowering KONG_DNS_NOT_FOUND_TTL to 5 changed
# nothing, so the number is not the negative-DNS cache and is not a knob this
# repo currently knows how to turn.
#
# Two situations enter that window: a Kong restart while bff-web is between
# pods, and every fresh cluster, where `make up` starts the gateway before the
# service it routes to exists. The second is what CI does on every run, which
# made a 20-attempt budget shared with the loop above a coin flip — CI failed on
# it, then passed on a rerun of the identical commit. 40 attempts is 120s, which
# clears the measured 59 with room rather than by a second.
while [[ "$status" == "502" || "$status" == "503" ]]; do
    attempt=$((attempt + 1))
    if ((attempt >= 40)); then
        fail "gateway still answering ${status} after 40 attempts (120s)" \
            "Kong is up but has no healthy bff-web to route to." \
            "Try: kubectl -n ${NAMESPACE} get pods,endpoints -l app.kubernetes.io/name=bff-web"
    fi
    sleep 3
    status="$(request GET /smoke-does-not-exist)"
done

pass "reachable after ${attempt} attempt(s)"

# ------------------------------------------------------------------------------
begin 'unrouted path is answered by the API, not by the gateway'

[[ "$status" == "404" ]] || fail "expected 404, got ${status}" \
    "Kong's catch-all route should hand this to bff-web so httpx answers it."

code="$(json '.error.code')"
[[ "$code" == "ROUTE_NOT_FOUND" ]] || fail "expected error.code ROUTE_NOT_FOUND, got '${code:-<none>}'" \
    "A body without error.code means Kong short-circuited this before bff-web saw it."

got="$(header X-Correlation-ID)"
[[ "$got" == "$CORRELATION_ID" ]] || fail "correlation id not echoed" \
    "sent '${CORRELATION_ID}', got back '${got:-<none>}'" \
    "An inbound X-Correlation-ID must be adopted, never replaced."

pass "404 ROUTE_NOT_FOUND in the error shape, correlation id echoed"

# ------------------------------------------------------------------------------
begin 'the deploy has settled'

# Everything below asserts what a deploy is supposed to serve. This waits until
# there is a deploy to assert against, because `rollout status` reports the new
# pods Ready and says nothing about the old ones. Two things outlive it, both
# transient and both indistinguishable from a real failure at the first request:
#
#   404  a replica still draining keeps serving on the keep-alive connection
#        Kong already holds, and answers a route added in this very deploy with
#        ROUTE_NOT_FOUND.
#   504  a replica that just started holds no gRPC channel yet, so its first
#        call pays DNS, the TCP connect and the HTTP/2 handshake inside the
#        800ms request budget — and the callee's first query opens its pool.
#
# The budget is the drain window, not a guess: 5s preStop + 25s SHUTDOWN_TIMEOUT
# bounded by terminationGracePeriodSeconds: 35. Anything decisive — a 200, a 401,
# a 500 — ends the wait immediately and is read by the check it belongs to, so
# waiting here can only cost time on a stack that was going to fail anyway.
#
# A streak rather than one success: Kong balances over every replica, so one 200
# says one of them is warm. Three in a row is evidence, not proof, and that is
# the honest ceiling for a check that only ever sees the far side of a gateway.
settle_deadline=$((SECONDS + 45))
attempt=0
streak=0

while :; do
    attempt=$((attempt + 1))
    status="$(request GET '/api/v1/products?pageSize=1')"

    case "$status" in
    200)
        streak=$((streak + 1))
        ((streak >= 3)) && break
        continue
        ;;
    404 | 502 | 503 | 504)
        streak=0
        info "${status} — the previous version may still be draining (${attempt})"
        ;;
    *)
        break
        ;;
    esac

    ((SECONDS < settle_deadline)) || fail "still answering ${status} after 45s" \
        "The rollout window is over, so this is the deploy, not the drain:" \
        "a 404 means the route is genuinely missing, a 504 means bff-web" \
        "reached catalog too slowly to matter. Try:" \
        "  kubectl -n ecommerce get pods -l app.kubernetes.io/name=bff-web"

    sleep 2
done

pass "serving after ${attempt} attempt(s)"

# ------------------------------------------------------------------------------
begin 'storefront listing — anonymous, and reaches catalog'

status="$(request GET '/api/v1/products?pageSize=5')"

[[ "$status" == "200" ]] || fail "expected 200, got ${status}" \
    "This is a public read: a 401 means it was mounted behind the auth group," \
    "and a 503 means bff-web could not reach catalog. Try:" \
    "  kubectl -n ecommerce get pods,endpoints -l app.kubernetes.io/name=catalog"

jq -e '.products | type == "array"' <"$body" >/dev/null 2>&1 || fail "products is not an array" \
    "An empty catalog must answer products: [] — never null, and never absent."

count="$(jq -r '.products | length' <"$body")"
pass "listing answered with ${count} product(s)"

# ------------------------------------------------------------------------------
begin 'register'

status="$(request POST /api/v1/auth/register "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\"}")"
reject_rate_limit "$status"
[[ "$status" == "201" ]] || fail "expected 201, got ${status}" "registering ${EMAIL}"

user_id="$(json '.user.id')"
[[ -n "$user_id" ]] || fail "response carried no user.id"

[[ "$(json '.tokens')" == "" ]] || fail "register returned tokens" \
    "Registering must not open a session — that is what login is for."

pass "created ${user_id}"

# ------------------------------------------------------------------------------
begin 'login'

status="$(request POST /api/v1/auth/login "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\"}")"
reject_rate_limit "$status"
[[ "$status" == "200" ]] || fail "expected 200, got ${status}" \
    "The account registered a moment ago could not sign in."

access_token="$(json '.tokens.accessToken')"
refresh_token="$(json '.tokens.refreshToken')"
[[ -n "$access_token" ]] || fail "response carried no tokens.accessToken"
[[ -n "$refresh_token" ]] || fail "response carried no tokens.refreshToken"

pass 'issued an access and a refresh token'

# ------------------------------------------------------------------------------
begin 'authenticated read — verifies the signing key pair agrees'

status="$(request GET /api/v1/me '' "$access_token")"
if [[ "$status" == "401" ]]; then
    fail "the token identity just signed was rejected as invalid (401)" \
        "identity signed it with its private key and bff-web could not verify" \
        "it with its public one, so the two are not a pair." \
        "Both are written by one command into the overlay being deployed:" \
        "  deploy/k8s/overlays/<env>/identity/jwt-private.pem" \
        "  deploy/k8s/overlays/<env>/bff-web/jwt-public.pem" \
        "Fix: make keys && make deploy SVC=identity && make deploy SVC=bff-web" \
        "     — adding OVERLAY=<env> to each for anything but local"
fi
[[ "$status" == "200" ]] || fail "expected 200, got ${status}"

me_id="$(json '.user.id')"
[[ "$me_id" == "$user_id" ]] || fail "wrong user" \
    "the token was signed for ${user_id} but /me answered ${me_id:-<none>}"

pass "identified ${me_id} from the verified token"

# ------------------------------------------------------------------------------
begin 'unauthenticated read is refused in this API shape'

status="$(request GET /api/v1/me)"
[[ "$status" == "401" ]] || fail "expected 401, got ${status}"

code="$(json '.error.code')"
[[ "$code" == "TOKEN_MISSING" ]] || fail "expected error.code TOKEN_MISSING, got '${code:-<none>}'" \
    "A 401 without error.code came from somewhere other than pkg/auth."

pass '401 TOKEN_MISSING'

# ------------------------------------------------------------------------------
begin 'logout'

status="$(request POST /api/v1/auth/logout "{\"refreshToken\":\"${refresh_token}\"}")"
[[ "$status" == "204" ]] || fail "expected 204, got ${status}"

pass 'session ended'

printf '\n\033[32msmoke passed\033[0m — %d checks against %s\n\n' "$step" "$BASE_URL"
