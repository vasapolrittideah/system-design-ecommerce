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
until status="$(request GET /smoke-does-not-exist)" && [[ "$status" != "000" ]]; do
    attempt=$((attempt + 1))
    if ((attempt >= 20)); then
        fail "no answer from ${BASE_URL} after 20 attempts" \
            "Is the stack up? Try: make up && make dev" \
            "The gateway is k3d's load balancer on 8000, not a port-forward."
    fi
    sleep 3
done

while [[ "$status" == "502" || "$status" == "503" ]]; do
    attempt=$((attempt + 1))
    if ((attempt >= 20)); then
        fail "gateway still answering ${status} after 20 attempts" \
            "Kong is up but has no healthy bff-web to route to." \
            "Try: kubectl -n ecommerce get pods,endpoints -l app=bff-web"
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
        "Both are written by one command into two overlays:" \
        "  deploy/k8s/overlays/local/identity/jwt-private.pem" \
        "  deploy/k8s/overlays/local/bff-web/jwt-public.pem" \
        "Fix: make keys && make deploy SVC=identity && make deploy SVC=bff-web"
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
