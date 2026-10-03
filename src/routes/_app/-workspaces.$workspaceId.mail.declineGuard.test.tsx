// RED — IMPORTANT: the legacy `/workspaces/{id}/mail` redirect races its own
// leave guard.
//
// Spec source: leaveGate.ts's own documented contract (module comment,
// lines 1-8: "EVERY path that closes or replaces a panel awaits the
// outgoing panel's leave confirmation first — 'the guard runs before the
// store moves'") and `leaveGateThen`'s signature (leaveGate.ts:38 —
// synchronous return type `void`, `go()` invoked asynchronously only if the
// guard resolves true, lines 45-51).
//
// workspaces.$workspaceId.mail.tsx::WorkspaceMailPage calls
// `leaveGateThen(outgoingPanelId, () => openPanel('mail', ...))` — a
// fire-and-forget call that returns synchronously regardless of the guard's
// eventual outcome — and, in the SAME synchronous tick, unconditionally
// calls `navigate({ to: '/workspaces/$workspaceId/chat', search: { panel:
// 'mail' }, replace: true })`. Per the spec above, when the outgoing panel's
// guard is later DECLINED, `openPanel('mail', ...)` is correctly skipped —
// but the URL has already changed to `chat?panel=mail`, and the chat
// route's own deep-link restoration (usePanelDeepLink.ts) opens Mail
// directly from that URL param, bypassing the guard entirely. Contrast the
// sibling workspaces.$workspaceId.media.tsx::WorkspaceMediaRedirect, whose
// comment (lines 10-20) states the URL is deliberately the ONLY writer —
// this route violates that by writing the URL AND calling openPanel through
// two independent, un-coordinated paths.
//
// The existing `-workspaces.$workspaceId.mail.test.tsx` mocks `leaveGateThen`
// to always proceed immediately (`(_outgoing, proceed) => proceed()`), so the
// decline path has never been exercised. Per the brief, that file is left
// untouched; this is a NEW file for the decline path.
//
// Oracle: when the guard is declined (leaveGateThen never invokes its
// `proceed` callback — the documented "guard runs before the store moves"
// contract), `navigate` must NOT be called with the chat+`panel=mail` deep
// link, because a URL that already says "open mail" is exactly what lets
// the chat route's deep-link restoration open Mail without ever consulting
// the guard the founder click just triggered.

import type { ComponentType } from "react";
import { cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let mockSearch: {
  mailbox?: string;
  folder?: string;
  message?: string;
} = {};

const { navigate, openPanel } = vi.hoisted(() => ({
  navigate: vi.fn(),
  openPanel: vi.fn(),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  createFileRoute:
    () =>
    (options: { component: ComponentType; validateSearch?: unknown }) => ({
      ...options,
      useParams: () => ({ workspaceId: "ws-1" }),
      useSearch: () => mockSearch,
    }),
  useNavigate: () => navigate,
}));

vi.mock("@/store/ui", () => ({
  useUiStore: {
    getState: () => ({ activePanel: "library", openPanel }),
  },
}));

// The guard is DECLINED: leaveGateThen's real contract only calls its
// `proceed` callback if the outgoing panel's guard resolves true (leaveGate.ts
// lines 45-51: `if (await guard()) go()`). A decline never calls it at all —
// modelled here by a mock that receives `proceed` but never invokes it,
// exactly like a real declined guard never reaching `go()`.
vi.mock("@/components/panel-shell/leaveGate", () => ({
  leaveGateThen: vi.fn(() => {
    /* declined: intentionally never calls the proceed callback */
  }),
}));

import { Route } from "./workspaces.$workspaceId.mail";

const WorkspaceMailRoute = (Route as unknown as { component: ComponentType })
  .component;

beforeEach(() => {
  vi.clearAllMocks();
  mockSearch = {};
});

afterEach(() => cleanup());

describe("retired workspace Mail page compatibility route — declined leave guard", () => {
  it("does NOT navigate to chat?panel=mail when the outgoing panel's leave guard is declined", async () => {
    mockSearch = {
      mailbox: "mia",
      folder: "drafts",
      message: "mid:<draft-42@test.local>",
    };

    render(<WorkspaceMailRoute />);

    // openPanel was correctly skipped — the guard never called `proceed`.
    expect(openPanel).not.toHaveBeenCalled();

    // But the URL-changing navigate() must ALSO not have fired: it is called
    // unconditionally today, in the same synchronous tick, regardless of the
    // guard's outcome. If it fires here, the browser lands on
    // /workspaces/ws-1/chat?panel=mail even though the guard said no — and
    // the chat route's deep-link restore will open Mail from that URL with
    // no guard involved at all.
    expect(navigate).not.toHaveBeenCalledWith({
      to: "/workspaces/$workspaceId/chat",
      params: { workspaceId: "ws-1" },
      search: { panel: "mail" },
      replace: true,
    });
  });
});
