import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  PanelContentProps,
  WorkspacePanelContext,
} from "@/components/panel-shell/types";

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  fetchAgents,
  fetchMailboxes,
}));

vi.mock("@/lib/api/mail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/mail")>()),
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
}));

import { mailPanelDefinition } from "./mailPanelDefinition";

function requireMailLeaveGuard() {
  if (
    typeof mailPanelDefinition.beforeLeave !== "function" ||
    typeof mailPanelDefinition.beforeLeaveRequired !== "function"
  ) {
    throw new Error(
      "RED: Mail must register beforeLeave and beforeLeaveRequired for unsaved compose/draft text",
    );
  }
  return {
    beforeLeave: mailPanelDefinition.beforeLeave,
    beforeLeaveRequired: mailPanelDefinition.beforeLeaveRequired,
  };
}

function renderMail(
  context: WorkspacePanelContext,
  presentation: PanelContentProps["presentation"],
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const Content = mailPanelDefinition.content;
  return render(
    <QueryClientProvider client={client}>
      <Suspense fallback={<div>Loading Mail…</div>}>
        <Content
          context={context}
          presentation={presentation}
          close={() => undefined}
          expand={() => undefined}
          registerExpandContext={() => undefined}
          onWidthSettle={() => undefined}
        />
      </Suspense>
    </QueryClientProvider>,
  );
}

async function cancelLeave(beforeLeave: () => Promise<boolean>) {
  const decision = beforeLeave();
  const dialog = await screen.findByRole("alertdialog", {
    name: /discard unsaved changes/i,
  });
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await expect(decision).resolves.toBe(false);
}

// Preload the real lazy chunk in setup, so its cold transform does not spend
// the first test's 30s budget while the Suspense fallback is still visible.
beforeAll(async () => {
  await import("./MailPanel");
});

beforeEach(() => {
  fetchAgents.mockReset().mockResolvedValue([{ id: "mia", name: "Mia" }]);
  fetchMailboxes.mockReset().mockResolvedValue([
    {
      agent_id: "mia",
      workspace_id: "ws-1",
      enabled: true,
      configured: true,
      username: "mia@example.test",
    },
  ]);
  fetchMailFolders.mockReset().mockResolvedValue({
    folders: [
      { slug: "inbox", display_name: "Inbox", total: 0, unread_count: 0 },
      { slug: "drafts", display_name: "Drafts", total: 1, unread_count: null },
    ],
  });
  fetchMailMessages.mockReset().mockResolvedValue({
    messages: [],
    truncated: false,
    next_before_uid: null,
  });
  fetchMailMessage
    .mockReset()
    .mockImplementation(() => new Promise(() => undefined));
  fetchMailSummary.mockReset().mockResolvedValue({ items: [] });
});

afterEach(() => cleanup());

describe("Mail unsaved-edit leave guard", () => {
  it("guards unsaved compose text and resolves false when the user cancels", async () => {
    const guard = requireMailLeaveGuard();
    renderMail({ workspaceId: "ws-1", mailboxId: "mia" }, "docked");

    // The real React.lazy path still mounts MailPanel; the setup hook above
    // absorbs its cold import. Keep the existing async UI wait for mailbox
    // readiness, as in the sibling fullscreen-context Mail tests.
    const composeButton = await screen.findByRole(
      "button",
      { name: "Compose" },
      { timeout: 30000 },
    );
    // F4 (MailPanel.tsx::MailPanel, `disabled={agentId === null}`): Compose
    // stays disabled until the mailbox list resolves and an agent is
    // selected. The lazy chunk mounts the real component before the mocked
    // fetchMailboxes() promise has settled, so the button can appear in the
    // DOM — findByRole matches disabled buttons too — before it is
    // clickable. Wait for the real user-observable enabled state (a mailbox
    // has resolved) before clicking; clicking while disabled is a no-op and
    // never opens the compose editor.
    await waitFor(() => expect(composeButton).toBeEnabled());
    fireEvent.click(composeButton);
    fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
      target: { value: "Unsent compose text" },
    });

    await waitFor(() => expect(guard.beforeLeaveRequired()).toBe(true));
    await cancelLeave(guard.beforeLeave);
    expect(guard.beforeLeaveRequired()).toBe(true);
  });

  it("guards unsaved draft-editor text and resolves false when the user cancels", async () => {
    const guard = requireMailLeaveGuard();
    fetchMailMessage.mockResolvedValue({
      message_id: "<draft-42@example.test>",
      uid: 42,
      uidvalidity: 777,
      folder: "drafts",
      subject: "Existing draft",
      from: "mia@example.test",
      from_name: "Mia",
      to: ["ada@example.test"],
      cc: [],
      date: "2026-09-29T08:00:00Z",
      seen: true,
      is_draft: true,
      is_omnipus_draft: true,
      read_by_agent: false,
      body_markdown: "Existing body",
      body_text: "Existing body",
      has_html: false,
      attachments: [],
    });

    renderMail(
      {
        workspaceId: "ws-1",
        mailboxId: "mia",
        folder: "drafts",
        messageRef: "uid:777:42",
      },
      "fullscreen",
    );

    // Same cold-lazy-chunk wait as the compose-flow test above.
    fireEvent.click(
      await screen.findByRole("button", { name: "Edit" }, { timeout: 30000 }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
      target: { value: "Changed draft text" },
    });

    await waitFor(() => expect(guard.beforeLeaveRequired()).toBe(true));
    await cancelLeave(guard.beforeLeave);
    expect(guard.beforeLeaveRequired()).toBe(true);
  });
});
