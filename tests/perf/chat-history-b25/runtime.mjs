import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';
import { digest } from './dataset.mjs';

export const REPO = fileURLToPath(new URL('../../../', import.meta.url));
export async function bridge(dependencies, output, sourceRoot = REPO) {
  const dependencyRequire = createRequire(path.join(dependencies, '__b25__.cjs'));
  const esbuild = dependencyRequire('esbuild');
  const file = path.join(output, 'generated-bridge.cjs');
  // Compile current generated validators / existing fixture orchestration only.
  // No duplicate handwritten gateway/SPA payload declarations.
  await esbuild.build({ stdin: { contents: [
    `export * from ${JSON.stringify(path.join(sourceRoot, 'src/lib/api/generated/schemas.ts'))};`,
    `export {WsFrame} from ${JSON.stringify(path.join(sourceRoot, 'src/lib/api/generated/ws-schemas.ts'))};`,
    `export {GatewayProcess} from ${JSON.stringify(path.join(sourceRoot, 'tests/e2e/fixtures/gateway-process.ts'))};`,
    `export {startTerminalOutcomeProvider} from ${JSON.stringify(path.join(sourceRoot, 'tests/e2e/fixtures/terminal-outcome-provider.ts'))};`,
  ].join('\n'), resolveDir: sourceRoot, loader: 'ts' }, bundle: true, platform: 'node', format: 'cjs',
    packages: 'external', nodePaths: [dependencies], outfile: file, logLevel: 'warning',
    banner: { js: `require = require('node:module').createRequire(${JSON.stringify(path.join(dependencies, '__b25__.cjs'))});` },
    plugins: [{ name: 'keep-existing-fixture-import-meta', setup(build) {
      build.onLoad({ filter: /\/tests\/e2e\/fixtures\/e2e-model\.ts$/ }, args => ({
        contents: fs.readFileSync(args.path, 'utf8').replaceAll('import.meta.url', JSON.stringify(pathToFileURL(args.path).href)), loader: 'ts',
      }));
    } }],
  });
  return { ...dependencyRequire('@playwright/test'), ...createRequire(import.meta.url)(file) };
}
export function verifyArtifactAttestation(attestation, candidate, gatewayHash, fixtureHash) {
  const expected = 'docs/internal/design/evidence/design-system-production-provenance.json';
  if (attestation.candidate !== candidate || attestation.gateway_sha256 !== gatewayHash || attestation.fixture_sha256 !== fixtureHash ||
    !attestation.attested_by || !Array.isArray(attestation.allowed_dirty_paths) || attestation.allowed_dirty_paths.length !== 1 ||
    attestation.allowed_dirty_paths[0] !== expected) throw new Error('artifact attestation must match candidate/binary hashes and ONLY generated design-system provenance path');
  return attestation;
}
export function candidateEvidence(candidate, binary, fixtureBinary, attestationPath, sourceRoot = REPO) {
  if (!/^[0-9a-f]{40}$/.test(candidate)) throw new Error('--candidate must be a full 40-character SHA');
  const resolved = execFileSync('git', ['rev-parse', `${candidate}^{commit}`], { cwd: sourceRoot, encoding: 'utf8' }).trim();
  if (resolved !== candidate) throw new Error('candidate does not resolve exactly');
  const changed = execFileSync('git', ['diff', '--name-only', candidate, '--', 'src', 'pkg', 'cmd', 'contracts', 'go.mod', 'go.sum', 'tests/e2e/fixtures'], { cwd: sourceRoot, encoding: 'utf8' }).trim();
  if (changed) throw new Error('candidate runtime/fixture sources differ from this checkout; use the candidate with only evaluator changes');
  const buildInfo = execFileSync('go', ['version', '-m', binary], { encoding: 'utf8' });
  const revision = buildInfo.match(/vcs\.revision=([0-9a-f]{40})/u)?.[1];
  if (revision !== candidate) throw new Error('gateway Go build info must identify the exact candidate');
  const gatewayHash = digest(binary), fixtureHash = digest(fixtureBinary);
  const attestation = attestationPath ? verifyArtifactAttestation(JSON.parse(fs.readFileSync(attestationPath, 'utf8')), candidate, gatewayHash, fixtureHash) : null;
  if (!attestation) throw new Error('explicit candidate/hash-bound artifact attestation required: compiled session.test has no VCS stamp; only generated design-system provenance may differ');
  return { candidate, evaluator_head: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: REPO, encoding: 'utf8' }).trim(),
    binary: { path: binary, sha256: gatewayHash, go_build_info: buildInfo },
    fixture_binary: { path: fixtureBinary, sha256: fixtureHash }, candidate_source: sourceRoot, runtime_source_diff: [], artifact_attestation: attestation };
}
export function isolatedEnvironment(home) {
  return { HOME: home, USERPROFILE: home, APPDATA: path.join(home, 'appdata'), LOCALAPPDATA: path.join(home, 'localappdata'),
    XDG_CONFIG_HOME: path.join(home, 'config'), XDG_DATA_HOME: path.join(home, 'data'),
    OMNIPUS_BEARER_TOKEN: '', OPENROUTER_API_KEY: '', OPENROUTER_API_KEY_CI: '' };
}
export async function provision(gateway, wire) {
  const call = async (method, url, value, schema) => {
    const response = await gateway.apiFetch(method, url, value);
    if (!response.ok) throw new Error(`isolated provision ${url} HTTP ${response.status}`);
    return schema.parse(response.body);
  };
  const agent = await call('POST', '/api/v1/agents', wire.AgentCreateRequestMain.parse({ type: 'Main', name: 'B25 evaluation',
    soul: 'Offline history measurement. No model turns requested.', provider: 'openrouter', model: 'deepseek/deepseek-v4.1-flash' }), wire.Agent);
  const workspace = await call('POST', '/api/v1/workspaces', wire.WorkspaceCreateRequest.parse({ name: 'B25 isolated evaluation', core_team: [agent.id] }), wire.Workspace);
  const newSession = () => call('POST', '/api/v1/sessions', wire.SessionCreateRequest.parse({ type: 'chat', agent_id: agent.id, workspace_id: workspace.id }), wire.Session);
  return { agent, workspace, session: await newSession(), other: await newSession() };
}
export function mountCopy(copy, ids, username) {
  // Only the owned working copy is re-homed to isolated identities. Original ID
  // and ALL archive/index bytes are preserved; original source stays read-only.
  const filename = path.join(copy.directory, 'meta.json');
  const meta = { ...copy.meta, agent_id: ids.agent.id, workspace_id: ids.workspace.id, owner: username };
  fs.writeFileSync(filename, JSON.stringify(meta), { mode: 0o600 });
  return { changed_working_copy_fields: ['agent_id', 'workspace_id', 'owner'], source_unchanged: true,
    archive_and_index_bytes_unchanged: true, preserved_original_session_identity: true };
}
