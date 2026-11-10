import type { ComponentType } from "react";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let mockSearch: {
  mailbox?: string;
  folder?: string;
  message?: string;
  view?: string;
} = {};

const { mailPanelProps, navigate, openPanel } = vi.hoisted(() => ({
  mailPanelProps: vi.fn(),
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

vi.mock("@/components/workspaces/mail/MailPanel", () => ({
  MailPanel: (props: unknown) => {
    mailPanelProps(props);
    return <main data-testid="full-page-mail">Retired full-page Mail</main>;
  },
}));

vi.mock("@/store/ui", () => ({
  useUiStore: {
    getState: () => ({ activePanel: null, openPanel }),
  },
}));

vi.mock("@/components/panel-shell/leaveGate", () => ({
  leaveGateThen: (_outgoing: unknown, proceed: () => void) => proceed(),
}));

import { Route } from "./workspaces.$workspaceId.mail";

const WorkspaceMailRoute = (Route as unknown as { component: ComponentType })
  .component;

beforeEach(() => {
  vi.clearAllMocks();
  mockSearch = {};
});

afterEach(() => cleanup());

describe("retired workspace Mail page compatibility route (D49 superseded by R10/R11)", () => {
  it("never renders Mail itself and redirects the complete address to chat plus the shared panel", async () => {
    mockSearch = {
      mailbox: "mia",
      folder: "drafts",
      message: "mid:<draft-42@test.local>",
      // Old bookmarked URLs may still carry this retired page marker. It
      // must not resurrect the pre-shell full-page Mail surface.
      view: "full",
    };

    render(<WorkspaceMailRoute />);

    expect(screen.queryByTestId("full-page-mail")).not.toBeInTheDocument();
    expect(mailPanelProps).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(openPanel).toHaveBeenCalledWith("mail", {
        workspaceId: "ws-1",
        mailboxId: "mia",
        folder: "drafts",
        messageRef: "mid:<draft-42@test.local>",
      });
      expect(navigate).toHaveBeenCalledWith({
        to: "/workspaces/$workspaceId/chat",
        params: { workspaceId: "ws-1" },
        search: { panel: "mail" },
        replace: true,
      });
    });
  });
});
