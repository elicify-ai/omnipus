/** Only the explicitly designated local and Amsterdam UAT runtimes are allowed. */
export function browserRuntimeTarget(value: string | undefined): URL {
  if (!value) throw new Error('OMNIPUS_URL is required for manual browser acceptance/diagnostics');
  const target = new URL(value);
  const local = target.protocol === 'http:' && ['localhost', '127.0.0.1'].includes(target.hostname) && target.port === '11094';
  const uat = target.origin === 'https://uat-omnipus.fly.dev';
  if ((!local && !uat) || target.username || target.password || target.pathname !== '/' || target.search || target.hash) {
    throw new Error('OMNIPUS_URL must be the isolated local port 11094 or https://uat-omnipus.fly.dev origin');
  }
  return target;
}
