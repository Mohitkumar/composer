import http from 'k6/http';

const BASE_URL = __ENV.BASE_URL || 'http://coordinator:8080';

/**
 * Login and return a JWT token. Retries up to maxRetries times to handle
 * the coordinator not being fully ready at setup() time.
 */
export function login(username, password, maxRetries = 10) {
  username = username || __ENV.K6_USERNAME || 'admin';
  password = password || __ENV.K6_PASSWORD || 'admin123';

  for (let i = 0; i < maxRetries; i++) {
    const res = http.post(
      `${BASE_URL}/api/v1/auth/login`,
      JSON.stringify({ username, password }),
      { headers: { 'Content-Type': 'application/json' }, timeout: '10s' }
    );

    if (res.status === 200) {
      const body = JSON.parse(res.body);
      if (body.token) {
        console.log(`[auth] login successful as '${username}'`);
        return body.token;
      }
    }

    console.warn(`[auth] login attempt ${i + 1}/${maxRetries} failed (status=${res.status}), retrying in 3s...`);
    // k6 setup() runs synchronously; busy-wait is the only option here
    const until = Date.now() + 3000;
    while (Date.now() < until) { /* spin */ }
  }

  throw new Error(`[auth] could not login after ${maxRetries} attempts`);
}

export function authHeaders(token) {
  return {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`,
  };
}
