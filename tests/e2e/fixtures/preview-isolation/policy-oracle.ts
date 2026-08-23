/**
 * policy-oracle.ts — the expected §10.3 isolation policy, derived from the
 * SPECIFICATION and never from the implementation.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHY THIS EXISTS AND WHY IT READS A MARKDOWN FILE
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * MV-13 requires every response on the preview-token path to carry a policy
 * that is BYTE-IDENTICAL to §10.3's — and says why the byte-exactness is the
 * point: "a constraint that only says a policy is present is satisfied by
 * `default-src *`". MV-13 also forbids degrading that to a substring or a
 * `contains` check.
 *
 * A byte-exact assertion needs an expected value, and where that value comes
 * from decides whether the assertion means anything:
 *
 *   from pkg/gateway  → the test asserts the implementation equals itself. A
 *                       directive could be quietly dropped and every test would
 *                       stay green. This is the direction preview-svg.spec.ts's
 *                       own comment has always forbidden, and it stays
 *                       forbidden.
 *   from the live     → same defect, one step further removed: the header IS
 *   response            the implementation's output.
 *   transcribed by    → correct in direction, and it is what this file used to
 *   hand                do. It stopped working on 2026-08-23, when §10.3 became
 *                       a TEMPLATE whose substitution depends on the gateway's
 *                       configuration: there is no longer one literal to
 *                       transcribe.
 *   from the SPEC's   → what this module does. The template is read out of
 *   own markdown        §10.3's fenced code block at test time, and the
 *                       substitution rules in §10.3's table are re-derived here
 *                       from the configuration.
 *
 * So the expected value still comes from the document that DEFINES the
 * requirement. Editing §10.3 changes what the tests demand; editing
 * pkg/gateway does not.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * THE ONE ASYMMETRY THAT WILL LOOK LIKE A BUG — DO NOT "FIX" IT
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * The origin substituted into the policy is the GATEWAY'S canonical origin,
 * derived from `gateway.host` / `gateway.port` / `gateway.public_url` in
 * `$OMNIPUS_HOME/config.json`. It is NOT `baseURL` — not the URL the browser
 * is pointed at.
 *
 * Those two are DIFFERENT in this suite's own rig, on purpose: the E2E gateway
 * binds `127.0.0.1` (the seeded default) while the tests browse
 * `http://localhost:PORT`. That divergence is not an oversight to be tidied
 * away — it is the single most common real-world configuration, and it is
 * exactly what §10.3's loopback-alias rule exists to survive. A default install
 * where the operator types `localhost` and the gateway bound `127.0.0.1` is the
 * case that broke Safari previews, and this rig is the only place it is
 * measured. Substituting `baseURL` here would make the oracle agree with the
 * browser by construction and silently stop testing the rule.
 */
import fs from "node:fs";
import path from "node:path";

/** §10.3's named placeholder, spelled as the specification spells it. */
export const GATEWAY_ORIGIN_PLACEHOLDER = "${GATEWAY_ORIGIN}";

/** Where §10.3 lives. Relative to the repo root, which is Playwright's cwd. */
const SPEC_PATH =
  "docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md";

/**
 * How many of the twelve directives carry the placeholder (§10.3: script-src,
 * style-src, img-src, font-src, media-src, frame-src — and `connect-src` MUST
 * NOT be among them).
 *
 * Asserted rather than assumed, because the failure it guards against is
 * silent: if a future edit to §10.3 dropped the placeholder from a directive,
 * this module would happily build a policy missing it and the byte-comparison
 * would still pass — against a weaker expectation nobody chose.
 */
const PLACEHOLDER_OCCURRENCES = 6;

/**
 * §10.3's template, read out of the specification's own fenced code block.
 *
 * Located by CONTENT, not by line number: line numbers in a 2,000-line document
 * move whenever anyone edits above them, and a line-addressed oracle that
 * drifts onto the wrong block fails in a way that looks like a policy defect.
 * The §10.7 SPA policy is the other fenced policy in this document and begins
 * `default-src 'self'`, so anchoring on `sandbox allow-scripts;` cannot reach
 * it.
 */
export function readSpecPolicyTemplate(): string {
  const specFile = path.resolve(SPEC_PATH);
  if (!fs.existsSync(specFile)) {
    throw new Error(
      `[policy-oracle] cannot read the specification at ${specFile}. ` +
        "This oracle derives its expected value from §10.3 rather than from " +
        "pkg/gateway, so without the spec there is no honest expectation to assert.",
    );
  }
  const markdown = fs.readFileSync(specFile, "utf8");

  const blocks = [
    ...markdown.matchAll(/```\n(sandbox allow-scripts;[^`]*?)\n```/g),
  ].map((m) => m[1].trim());
  if (blocks.length !== 1) {
    throw new Error(
      `[policy-oracle] expected exactly ONE fenced policy block beginning ` +
        `"sandbox allow-scripts;" in ${SPEC_PATH}, found ${blocks.length}. ` +
        "Two blocks means the oracle cannot tell which one is normative; zero " +
        "means §10.3 was restructured and this extractor must be updated with it.",
    );
  }

  const template = blocks[0];
  const occurrences = template.split(GATEWAY_ORIGIN_PLACEHOLDER).length - 1;
  if (occurrences !== PLACEHOLDER_OCCURRENCES) {
    throw new Error(
      `[policy-oracle] §10.3's template carries ${occurrences} ` +
        `${GATEWAY_ORIGIN_PLACEHOLDER} placeholders, expected ${PLACEHOLDER_OCCURRENCES} ` +
        "(script-src, style-src, img-src, font-src, media-src, frame-src). " +
        "If a directive legitimately gained or lost the origin, update " +
        "PLACEHOLDER_OCCURRENCES in the same commit — and check that connect-src " +
        "did not gain it, which FR-006 forbids.",
    );
  }
  return template;
}

/**
 * The gateway's own configuration, as the running gateway read it.
 *
 * `$OMNIPUS_HOME/config.json` and nothing else. The alternative — asking the
 * gateway what origin it thinks it has — would make the implementation the
 * oracle again.
 */
function gatewayConfig(): {
  host?: string;
  port?: number;
  public_url?: string;
} {
  const home =
    process.env.OMNIPUS_HOME ||
    (process.env.HOME ? path.join(process.env.HOME, ".omnipus") : "");
  const file = path.join(home, "config.json");
  if (!home || !fs.existsSync(file)) {
    throw new Error(
      `[policy-oracle] no gateway config at ${file || "(OMNIPUS_HOME unset)"}. ` +
        "§10.3's substitution depends on gateway.host / gateway.port / " +
        "gateway.public_url, so the expected policy cannot be derived without it.",
    );
  }
  const parsed = JSON.parse(fs.readFileSync(file, "utf8")) as {
    gateway?: Record<string, unknown>;
  };
  return (parsed.gateway ?? {}) as {
    host?: string;
    port?: number;
    public_url?: string;
  };
}

/**
 * §10.3's "Base value" row, re-derived from configuration.
 *
 * Mirrors the resolution order the specification states for
 * `middleware.CanonicalGatewayOrigin`: `public_url` verbatim; then a wildcard
 * bind yields nothing; then a host that is already a URL; then host:port with
 * the scheme decided by the port.
 *
 * The defaults are §10.3's own: it names the default install as
 * `127.0.0.1:5000`. They are applied only when config.json omits the key, which
 * is what a seeded install looks like.
 */
export function gatewayCanonicalOrigin(): string {
  const cfg = gatewayConfig();

  const publicURL = (cfg.public_url ?? "").trim();
  if (publicURL !== "") return normaliseToOrigin(publicURL);

  const host = (cfg.host ?? "127.0.0.1").trim();
  if (host === "") return "";

  const bare = host.replace(/^\[/, "").replace(/\]$/, "");
  if (bare === "0.0.0.0" || bare === "::" || bare === "::0") return "";

  if (host.includes("://")) return normaliseToOrigin(host);

  const port = cfg.port ?? 5000;
  const scheme = port === 443 ? "https" : "http";
  if (port > 0 && port !== 80 && port !== 443)
    return `${scheme}://${host}:${port}`;
  return `${scheme}://${host}`;
}

/**
 * scheme://authority, with any path and trailing slash removed.
 *
 * §10.3 requires this and states the consequence of getting it wrong: a CSP
 * host-source carrying a path is a PATH-MATCH, so `https://host/app` would stop
 * matching `https://host/library-preview/…` and block every subresource, on
 * every engine, for a config value that looks perfectly reasonable.
 */
function normaliseToOrigin(value: string): string {
  try {
    const u = new URL(value);
    if (!u.protocol || !u.host) return "";
    return `${u.protocol}//${u.host}`;
  } catch {
    return "";
  }
}

/** Whether a hostname denotes this machine over loopback (§10.3's table). */
function isLoopbackHost(hostname: string): boolean {
  if (hostname.toLowerCase() === "localhost") return true;
  const bare = hostname.replace(/^\[/, "").replace(/\]$/, "");
  // Each octet 0-255, matching Go's net.ParseIP. The looser \d{1,3} form this
  // replaces accepted 127.999.999.999, which Go rejects — an independent
  // oracle that disagrees with the implementation about what a loopback IS
  // will be "fixed" by editing whichever side is easier, and there is a 50%
  // chance that is the product. (Adversarial review, 2026-08-23.)
  const octet = "(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)";
  if (new RegExp(`^127\\.${octet}\\.${octet}\\.${octet}$`).test(bare))
    return true;
  // Go's IP.IsLoopback() also treats the IPv4-mapped form as loopback.
  if (/^::ffff:127\./i.test(bare)) return true;
  return bare === "::1";
}

/**
 * §10.3's `${GATEWAY_ORIGIN}` — a space-separated list of CSP host-sources.
 *
 * Non-loopback: one entry. Loopback: all three spellings, same scheme and port,
 * CANONICAL FIRST and then the remaining two in the fixed order `127.0.0.1`,
 * `localhost`, `[::1]`, skipping the one already emitted. The order is part of
 * the contract, not a detail — MV-13 asserts one identical string on every
 * response, so a list whose order varied would break it without breaking any
 * page.
 */
export function gatewayOriginSources(
  canonicalOrigin = gatewayCanonicalOrigin(),
): string[] {
  const trimmed = canonicalOrigin.trim();
  if (trimmed === "") return [];

  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return [];
  }
  const canonical = `${url.protocol}//${url.host}`;
  if (!isLoopbackHost(url.hostname)) return [canonical];

  const sources = [canonical];
  for (const alias of ["127.0.0.1", "localhost", "::1"]) {
    const host = alias.includes(":") ? `[${alias}]` : alias;
    const candidate = `${url.protocol}//${host}${url.port ? `:${url.port}` : ""}`;
    if (candidate !== canonical) sources.push(candidate);
  }
  return sources;
}

/**
 * The policy §10.3 requires this gateway to serve, byte for byte.
 *
 * THE EMPTY CASE IS THE ONE THAT IS EASY TO GET SUBTLY WRONG. §10.3: "the
 * placeholder AND the single space preceding it are removed", collapsing
 * `'self' ${GATEWAY_ORIGIN}` to `'self'` and reproducing the pre-amendment
 * string exactly. Substituting the empty string alone would leave a DOUBLE
 * SPACE — a policy that still works, on every engine, while failing a
 * byte-oracle for a reason nobody can see from either string.
 */
export function expectedIsolationPolicy(
  template = readSpecPolicyTemplate(),
  sources = gatewayOriginSources(),
): string {
  const joined = sources.join(" ");
  if (joined === "") {
    return template.split(` ${GATEWAY_ORIGIN_PLACEHOLDER}`).join("");
  }
  return template.split(GATEWAY_ORIGIN_PLACEHOLDER).join(joined);
}
