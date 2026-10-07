/**
 * #1081 R3 — B-60, MAJ-CW-010/011; ADR "Context budget and tool-result
 * routing", amendment §18.4; tool-iteration-limit spec exact terminal text.
 *
 * Outcome oracle: one narrated iteration-cap failure, one actual completed tool
 * round, one failed completion, one failed lifecycle diagnostic, one distinct
 * durable terminal entry, and exactly one terminal sentence with Verbose OFF.
 * All observations join on the SAME session/turn and durable message identity.
 * The Go companion observes the actual EventBus TurnEndPayload, not a log copy.
 * No invented WebSocket turn.end message: the gateway does not forward one.
 *
 * Cases: stay live; lose the real socket AFTER narration but BEFORE completion.
 * Both reattach and reload through the actual SPA. A held external HTTP provider
 * is the ONLY scripted process edge; no synthetic replay, seeded transcript,
 * mocked chat store, production test hook, or gateway response interception.
 * The Go companion also covers silent cap and successful cap+1 controls.
 * Invalid limits and other terminal causes belong to their dedicated RED packs.
 *
 * CHECK deferred mutations: suppress failure status; suppress post-narration
 * terminal persistence/delivery; duplicate replay; hide notice when Verbose OFF.
 * GREEN and mutation proof are deferred to a fresh CHECK instance. Collection
 * or typechecking alone is NOT execution/RED evidence for these browser cases.
 */
import fs from 'node:fs';
import path from 'node:path';
import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { GatewayProcess } from './fixtures/gateway-process';
import { E2E_MODEL } from './fixtures/e2e-model';
import { chatInput, waitForConnected } from './fixtures/selectors';
import { startTerminalOutcomeProvider, TERMINAL_NARRATION } from './fixtures/terminal-outcome-provider';
import {
  TERMINAL_CAP_NOTICE, TERMINAL_REASON, createTerminalSession, recordTerminalTraffic,
  openTerminalSession, turnVerboseOff, terminalLogRows, terminalTranscript,
  terminalLiveEntries, assertTerminalRendering,
} from './fixtures/terminal-outcome-acceptance';

// Own cookie jar as well as own gateway: never borrow the shared suite's admin.
test.use({ storageState: { cookies: [], origins: [] } });
test.setTimeout(240_000);
test.describe('terminal outcome agrees across real surfaces', () => {
  test.describe.configure({ retries: 0 }); // Deterministic provider, no paid-model variance.
  for (const disconnected of [false, true]) {
    test(`iteration cap after narration: ${disconnected ? 'terminal while disconnected' : 'live'}, reattach and reload render exactly once with Verbose off`, async ({ page, consoleErrors }, info) => {
      const mock = await startTerminalOutcomeProvider();
      let gw: GatewayProcess | undefined;
      const logPath = info.outputPath('terminal-gateway.jsonl');
      fs.mkdirSync(path.dirname(logPath), { recursive: true });
      const traffic = recordTerminalTraffic(page);
      try {
        gw = await GatewayProcess.start({
          apiBase: mock.url, model: E2E_MODEL, adminUsername: 'terminal-acceptance',
          // Lifecycle diagnostics are INFO; the gateway's quiet WARN default
          // deliberately omits them. Enable the real observation surface.
          env: { OMNIPUS_LOG_FILE: logPath, OMNIPUS_LOG_LEVEL: 'info' },
        });
        // All API-fixture calls precede UI login: one token slot per user.
        const { agent, session, otherSession } = await createTerminalSession(gw);
        await page.goto(gw.baseURL);
        await expect(page.locator('#login-username')).toBeVisible();
        await page.locator('#login-username').pressSequentially(gw.adminUsername);
        await page.locator('#login-password').pressSequentially(gw.adminPassword);
        await page.getByRole('button', { name: 'Sign in' }).click();
        await expect(page.locator('#login-username')).toBeHidden();
        await turnVerboseOff(page, gw.baseURL);
        await openTerminalSession(page, gw.baseURL, session.id);
        await expect(page.locator('[data-message-id]')).toHaveCount(0);
        await chatInput(page).fill('Load read_file with ToolSearch and then report what happened.');
        await chatInput(page).press('Enter');
        // This positive observation proves the script gate is holding a REAL
        // rendered turn; a missing notice cannot be blamed on no turn occurring.
        await expect(page.getByText(TERMINAL_NARRATION, { exact: true })).toBeVisible();
        expect(mock.calls(), 'one real streaming provider request').toBe(1);
        expect(mock.failures(), 'the offered real tool and external protocol are valid').toEqual([]);
        const narrated = traffic.frames.find((frame) => frame.type === 'token' &&
          frame.session_id === session.id && frame.content === TERMINAL_NARRATION);
        expect(narrated?.type, 'real narration token was observed').toBe('token');
        if (!narrated || narrated.type !== 'token' || !narrated.turn_id) {
          throw new Error('BLOCKED: real narration token has no turn identity; cannot correlate terminal surfaces');
        }
        const turnId = narrated.turn_id;
        const beforeDrop = [...traffic.sockets];
        if (disconnected) {
          expect(beforeDrop.length, 'positive socket instrument before loss').toBeGreaterThan(0);
          await page.context().setOffline(true);
          // Close only actual sockets. Do not replace WebSocket or its frames.
          await page.evaluate(() => {
            const sockets = (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [];
            for (const socket of sockets) {
              if (socket.readyState === WebSocket.OPEN) socket.close(1000, 'acceptance-network-drop');
            }
          });
          await expect.poll(() => beforeDrop.every((socket) => socket.isClosed())).toBe(true);
        }
        mock.finishToolRound();
        await expect.poll(() => terminalLogRows(logPath).filter((row) =>
          row.event_kind === 'turn_end' && row.turn_id === turnId).length,
        { timeout: 30_000 }).toBe(1);
        const rows = terminalLogRows(logPath);
        const turnRows = rows.filter((row) => row.turn_id === turnId);
        const ends = turnRows.filter((row) => row.event_kind === 'turn_end');
        expect.soft(ends, 'one actual lifecycle diagnostic for this observed turn').toHaveLength(1);
        expect.soft(ends[0].agent_id, 'diagnostic producer matches this session').toBe(agent.id);
        expect.soft(ends[0].status, 'failed-done and lifecycle must BOTH classify cap as error').toBe('error');
        expect.soft(ends[0].iterations_total, 'one actual provider/tool round exhausted cap=1').toBe(1);
        const operations = turnRows.filter((row) => row.event_kind === 'tool_exec_end');
        expect(operations.map((row) => ({ tool: row.tool, is_error: row.is_error })),
          'fixture actually executed the real harmless tool operation').toEqual([{ tool: 'ToolSearch', is_error: false }]);
        const responses = rows.filter((row) => row.session_key === ends[0].session_key &&
          typeof row.message === 'string' && row.message.startsWith('Response: '));
        expect.soft(responses, 'the same session diagnostic names the genuine terminal reason').toHaveLength(1);
        expect.soft(responses[0]?.message, 'complete specified cause sentence, not a provider guess')
          .toEqual(expect.stringMatching(new RegExp('^Response: ' + TERMINAL_REASON.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))));
        const completion = () => traffic.frames.filter((frame) => frame.type === 'done' &&
          frame.session_id === session.id && frame.stats?.tokens !== undefined);
        if (!disconnected) {
          await expect.poll(() => completion().length).toBe(1);
          const done = completion()[0];
          expect(done.type).toBe('done');
          if (done.type !== 'done') throw new Error('BLOCKED: completion instrument selected a non-done frame');
          // Part B's PASS precondition is independent of failed-flag selection.
          expect(done.stats?.turn_failed, 'real failed-done precondition').toBe(true);
          expect.soft(done.turn_id, 'done and actual lifecycle diagnostic identify the SAME turn').toBe(turnId);
        } else {
          expect(completion(), 'no completion was delivered to the closed browser before reconnect').toEqual([]);
          await page.context().setOffline(false);
          await waitForConnected(page);
          await expect.poll(() => traffic.sockets.length).toBeGreaterThan(beforeDrop.length);
        }
        const detail = await terminalTranscript(page, gw.baseURL, session.id);
        expect.soft(detail.session.id, 'read the SAME durable session').toBe(session.id);
        const assistants = detail.messages.filter((entry) => entry.role === 'assistant' && entry.content);
        expect.soft(assistants.map((entry) => entry.content), 'prior narration cannot substitute for terminal outcome')
          .toEqual([TERMINAL_NARRATION, TERMINAL_CAP_NOTICE]);
        expect.soft(assistants.map((entry) => entry.turn_id), 'durability belongs to the SAME lifecycle turn')
          .toEqual([turnId, turnId]);
        expect.soft(assistants.map((entry) => entry.agent_id), 'durable producer identity agrees')
          .toEqual([agent.id, agent.id]);
        const terminalEntries = assistants.filter((entry) => entry.content === TERMINAL_CAP_NOTICE);
        expect.soft(terminalEntries, 'exactly one distinct durable terminal entry').toHaveLength(1);
        if (!disconnected) {
          const live = terminalLiveEntries(traffic.frames, session.id, turnId);
          expect.soft(live.map((entry) => entry.content), 'live content has the independently specified pair')
            .toEqual([TERMINAL_NARRATION, TERMINAL_CAP_NOTICE]);
          expect.soft(live, 'live and durable entries share stable IDs AND exact contents')
            .toEqual(assistants.map(({ id, content }) => ({ id, content })));
        }
        await assertTerminalRendering(page, info, disconnected ? 'reconnected-after-terminal' : 'live', terminalEntries);
        // Re-opening the already-active session sends no attach by design, so
        // switch to the other session first: coming back is a genuine fresh attach.
        await openTerminalSession(page, gw.baseURL, otherSession.id);
        const beforeAttach = traffic.attaches.length;
        await openTerminalSession(page, gw.baseURL, session.id);
        await expect.poll(() => traffic.attaches.slice(beforeAttach).filter((frame) => frame.session_id === session.id).length)
          .toBeGreaterThan(0);
        await assertTerminalRendering(page, info, 'fresh-reattach', terminalEntries);
        const beforeReload = traffic.attaches.length;
        await page.reload();
        await waitForConnected(page);
        await expect.poll(() => traffic.attaches.slice(beforeReload).filter((frame) => frame.session_id === session.id).length)
          .toBeGreaterThan(0);
        await assertTerminalRendering(page, info, 'reload', terminalEntries);
        const after = await terminalTranscript(page, gw.baseURL, session.id);
        expect.soft(after.messages, 'reattach/reload must not duplicate or rewrite durable entries').toEqual(detail.messages);
        expect.soft(mock.calls(), 'replay never triggers a second provider round').toBe(1);
        expect.soft(mock.failures(), 'external fixture remained valid').toEqual([]);
        expect.soft(traffic.failures, 'every captured boundary frame validates against generated contracts').toEqual([]);
        expect.soft(consoleErrors, 'no unrelated browser error may hide the terminal regression').toEqual([]);
        await info.attach('same-turn-surfaces', { contentType: 'application/json', body: JSON.stringify({
          sessionId: session.id, turnId, agentId: agent.id, verbose: false,
          diagnostics: turnRows, responseDiagnostics: responses,
          transcript: detail.messages, frames: traffic.frames, attaches: traffic.attaches,
        }, null, 2) });
      } finally {
        mock.finishToolRound();
        try {
          if (gw) await gw.stop();
        } finally {
          await mock.close();
        }
      }
    });
  }
});
