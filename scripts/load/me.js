// The read path: GET /api/v1/me, offered at a fixed rate.
//
// One request here is the whole synchronous shape of this system — Kong's ring
// balancer, an ES256 signature verified in bff-web, one east-west gRPC call to
// identity under a 300ms deadline, and a row read from identity's database. It
// is the scenario to run when the question is about the system rather than about
// one endpoint: whether the HPA reacts, whether the pods it adds receive
// anything, and where the 800ms budget goes.
//
// Tokens are minted once in setup and reused, which bounds a useful run at
// IDENTITY_JWT_TTL (15m). Past it every request is a 401, latency improves, and
// the thing that says so is the access_token_rejected counter.

import http from 'k6/http';
import { BASE_URL, USERS, expect, headers, login, newUser, pick, register, scenario, thresholds } from './lib.js';

export const options = {
  scenarios: { me: scenario() },
  thresholds,
};

export function setup() {
  const tokens = [];
  for (let i = 0; i < USERS; i++) {
    const user = newUser(i);
    register(user);
    tokens.push(login(user).accessToken);
  }
  return { tokens };
}

export default function (data) {
  const res = http.get(`${BASE_URL}/api/v1/me`, {
    headers: headers({ Authorization: `Bearer ${pick(data.tokens)}` }),
    // Without an explicit name every URL is its own metric row. Here they are
    // all one path, but the tag is what keeps that true when a scenario grows a
    // second endpoint with an ID in it.
    tags: { name: 'GET /api/v1/me' },
  });

  expect(res, 200, 'GET /api/v1/me');
}
