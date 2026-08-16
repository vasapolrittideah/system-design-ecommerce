// Shared pieces of the load scenarios: how load is offered, what counts as a
// pass, and the two failures that would otherwise be read as latency.
//
// Every scenario in this directory is an open model at a fixed arrival rate. A
// closed model — N virtual users each waiting for its own last response — offers
// less traffic as the system slows down, which is a load generator quietly
// protecting the thing it is supposed to be pushing.

import http from 'k6/http';
import { check, fail } from 'k6';
import { Counter } from 'k6/metrics';

export const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8000').replace(/\/$/, '');
export const RUN_ID = __ENV.RUN_ID || `load-${Date.now()}`;
export const USERS = Number(__ENV.USERS || 10);

const RATE = Number(__ENV.RATE || 50);
const DURATION = __ENV.DURATION || '1m';
const WARMUP = __ENV.WARMUP || '20s';

// Requests the gateway refused. It is counted rather than left to show up as a
// failure rate because a run that hits the limiter is not a result at all: the
// numbers describe Kong's counter and nothing behind it.
export const rateLimited = new Counter('gateway_rate_limited');

// Requests rejected as unauthenticated. In a run whose tokens were all valid a
// minute ago this means one thing — the run outlived IDENTITY_JWT_TTL (15m) —
// and without its own counter it reads as the system getting fast, because a
// rejection is cheaper than the work it replaced.
export const tokenRejected = new Counter('access_token_rejected');

// scenario builds the executor. The ramp is a warm-up rather than a result: the
// first requests pay for connection setup, the JIT, and an autoscaler that has
// not been given a reason to act yet.
export function scenario() {
  return {
    executor: 'ramping-arrival-rate',
    startRate: Math.max(1, Math.round(RATE / 10)),
    timeUnit: '1s',
    // Enough VUs to keep offering the rate while responses are slow, and a
    // ceiling so that a system which has stopped answering cannot turn into a
    // laptop allocating VUs until it swaps.
    preAllocatedVUs: Math.max(10, Math.ceil(RATE / 2)),
    maxVUs: Math.max(50, RATE * 4),
    stages: [
      { target: RATE, duration: WARMUP },
      { target: RATE, duration: DURATION },
    ],
  };
}

// The pass conditions, and each one is a number this repo already committed to
// somewhere else rather than a round figure chosen here.
export const thresholds = {
  // BFF_WEB_REQUEST_TIMEOUT. Past it the request is not slow, it is a 503 with
  // a reason code — so this threshold and the error rate below tend to fail
  // together, and which one moved first says whether the budget was the cause.
  http_req_duration: ['p(95)<800'],
  // The rate the ErrorRate alert fires at in deploy/k8s/infra/prometheus.
  http_req_failed: ['rate<0.01'],
  checks: ['rate>0.99'],
  // A run whose setup failed executes no iteration and therefore no check, and
  // k6 reports an empty rate as a pass — every line above it green, describing
  // nothing that happened.
  iterations: ['count>0'],
  // Neither is a degree of badness: one of these above zero means the run
  // measured something other than what it was pointed at.
  gateway_rate_limited: ['count<1'],
  access_token_rejected: ['count<1'],
};

// headers returns the JSON request headers, with a correlation ID unique to this
// iteration. pkg/httpx adopts an inbound one rather than minting over it, so
// this is the string that ties a log line here to every hop it caused.
export function headers(extra) {
  // setup() runs outside any iteration, where the iteration counter is not a
  // defined global at all rather than a number to read.
  const iteration = typeof __ITER === 'undefined' ? 'setup' : __ITER;

  return Object.assign(
    {
      'Content-Type': 'application/json',
      'X-Correlation-ID': `${RUN_ID}-${__VU}-${iteration}`,
    },
    extra || {},
  );
}

// expect records whether a response was the one the scenario was asking for, and
// classifies the two answers that mean the run itself is invalid.
export function expect(res, status, name) {
  const ok = check(res, { [`${name} -> ${status}`]: (r) => r.status === status });
  if (!ok) {
    if (res.status === 429) rateLimited.add(1);
    if (res.status === 401 && status !== 401) tokenRejected.add(1);
  }
  return ok;
}

// newUser returns credentials unique to this run. Registering is not idempotent,
// and a fixed address would make every run after the first a conflict.
export function newUser(index) {
  return {
    email: `${RUN_ID}-${index}@load.test`,
    password: 'load-test-password-1234',
  };
}

// setupFailed ends the run and says which kind of failure it was.
//
// Setup fails the whole run rather than the iteration, because a pool that was
// half built produces a scenario measuring a mix of two things. It classifies
// before it does that: the auth routes allow ten requests a minute, so a pool of
// any size meets the limiter first, and this is where a run under LIMITER=keep
// stops — before the counter in expect() has ever been reached.
function setupFailed(res, what) {
  if (res.status === 429) {
    rateLimited.add(1);
    fail(
      `setup: the gateway refused ${what} (429). /api/v1/auth/* allows 10 requests ` +
      `a minute, which a pool of ${USERS} users cannot build. Drop LIMITER=keep, ` +
      `or wait a minute and lower USERS.`,
    );
  }
  fail(`setup: ${what} answered ${res.status}: ${res.body}`);
}

// register creates an account.
export function register(user) {
  const res = http.post(
    `${BASE_URL}/api/v1/auth/register`,
    JSON.stringify(user),
    { headers: headers(), tags: { name: 'POST /api/v1/auth/register' } },
  );

  if (res.status !== 201) setupFailed(res, `register ${user.email}`);
  return res;
}

// login signs in and returns the token pair.
export function login(user) {
  const res = http.post(
    `${BASE_URL}/api/v1/auth/login`,
    JSON.stringify(user),
    { headers: headers(), tags: { name: 'POST /api/v1/auth/login' } },
  );

  if (res.status !== 200) setupFailed(res, `login ${user.email}`);
  return res.json('tokens');
}

// pick returns one element at random. Which user a request belongs to should not
// correlate with when it is sent — a round-robin over a small pool lines up with
// the arrival rate and produces a pattern no client has.
export function pick(items) {
  return items[Math.floor(Math.random() * items.length)];
}
