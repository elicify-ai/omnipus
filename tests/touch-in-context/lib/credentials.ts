/**
 * Credentials for the "sign in" task flow and for the authenticated
 * `APIRequestContext` calls used to discover live workspace/agent/session
 * ids (see routes.ts).
 *
 * Deliberately NOT hardcoded: this suite is designed to run against
 * whichever OMNIPUS_HOME the operator seeded for the run (a copied,
 * disposable dev home per tests/touch-in-context/README.md, never the
 * shared/production home), so the username and password are read from the
 * environment the operator already had to set to point the suite at that
 * home's gateway. Failing fast with a clear message here is the same
 * pattern tests/e2e/global-setup.ts uses for OPENROUTER_API_KEY_CI — a
 * missing credential should surface immediately, not as a 60s login-form
 * timeout three layers of stack trace away from this file.
 */
export interface TouchCheckCredentials {
  username: string;
  password: string;
}

export function readCredentials(): TouchCheckCredentials {
  const username = process.env.TOUCH_CHECK_USER;
  const password = process.env.TOUCH_CHECK_PASS;
  if (!username || !password) {
    throw new Error(
      '[touch-in-context] TOUCH_CHECK_USER / TOUCH_CHECK_PASS are not set.\n' +
        'This suite signs in for real (D17 task flow: "sign in") against whatever ' +
        'OMNIPUS_HOME/gateway you pointed OMNIPUS_URL at — it does not assume a ' +
        'default password. Set both before running:\n\n' +
        '  export TOUCH_CHECK_USER="admin"\n' +
        '  export TOUCH_CHECK_PASS="<the seeded home\'s admin password>"\n\n' +
        "See tests/touch-in-context/README.md for how to read a seeded home's " +
        'password without printing it.',
    );
  }
  return { username, password };
}
