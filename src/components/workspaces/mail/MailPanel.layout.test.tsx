import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailSummary,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
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
  fetchMailSummary,
}));

import { MailPanel } from "./MailPanel";

function renderPanel(layout?: "stacked" | "split") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MailPanel workspaceId="ws-1" {...(layout ? { layout } : {})} />
    </QueryClientProvider>,
  );
}

describe("Mail list and preview layout (D48)", () => {
  beforeEach(() => {
    fetchAgents.mockReset().mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: "mia", name: "Mia" }]);
    fetchMailboxes.mockReset().mockResolvedValue([
      {
        agent_id: "mia",
        workspace_id: "ws-1",
        enabled: true,
        configured: true,
        username: "mia@test.local",
      },
    ]);
    fetchMailFolders.mockReset().mockResolvedValue({
      folders: [
        { slug: "inbox", display_name: "Inbox", total: 0, unread_count: 0 },
      ],
    });
    fetchMailMessages.mockReset().mockResolvedValue({
      messages: [],
      truncated: false,
      next_before_uid: null,
    });
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] });
  });

  afterEach(() => cleanup());

  it("stacks the message list above the preview in the side panel", async () => {
    renderPanel();

    const layout = await screen.findByTestId("mail-list-preview-layout");
    expect(layout).toHaveAttribute("data-layout", "stacked");
    expect(layout).toHaveClass("flex-col");
    expect(screen.getByTestId("mail-list-zone")).toBeInTheDocument();
    expect(screen.getByTestId("mail-reading-zone")).toBeInTheDocument();
  });

  it("places the message list left of the preview in the full-page layout", async () => {
    renderPanel("split");

    const layout = await screen.findByTestId("mail-list-preview-layout");
    expect(layout).toHaveAttribute("data-layout", "split");
    expect(layout).toHaveClass("flex-row");
  });
});
