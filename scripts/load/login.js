// The expensive write path: POST /api/v1/auth/login, offered at a fixed rate.
//
// Every iteration makes identity compute an argon2id hash — m=9216 KiB, t=4 —
// and insert a refresh-token row, which makes this the scenario that saturates
// something rather than the one that measures a round trip. Two couplings it is
// pointed at, both of which look like latency until they are not:
//
//   the 300ms deadline    BFF_WEB_IDENTITY_TIMEOUT bounds the gRPC call, so once
//                         hashes queue behind each other on a contended CPU the
//                         answer stops being a slow 200 and becomes a 5xx.
//   the memory limit      each hash in flight holds 9 MiB against a 256Mi limit,
//                         so concurrency here is bounded by something that
//                         reports itself as an OOMKilled pod.
//
// Run it well below the rate the read path takes: this one is meant to find a
// limit, not to be sustained.

import http from 'k6/http';
import { BASE_URL, USERS, expect, headers, newUser, pick, register, scenario, thresholds } from './lib.js';

export const options = {
  scenarios: { login: scenario() },
  thresholds,
};

export function setup() {
  const users = [];
  for (let i = 0; i < USERS; i++) {
    const user = newUser(i);
    register(user);
    users.push(user);
  }
  return { users };
}

export default function (data) {
  const res = http.post(`${BASE_URL}/api/v1/auth/login`, JSON.stringify(pick(data.users)), {
    headers: headers(),
    tags: { name: 'POST /api/v1/auth/login' },
  });

  expect(res, 200, 'POST /api/v1/auth/login');
}
