import type { ComponentType } from "react";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

let mockSearch: {
  mailbox?: string;
  folder?: string;
  message?: string;
  view?: "full";
} = {};

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  createFileRoute:
    () =>
    (options: { component: ComponentType; validateSearch?: unknown }) => ({
      ...options,
      useParams: () => ({ workspaceId: "ws-1" }),
      useSearch: () => mockSearch,
    }),
}));

const { mailPanelProps } = vi.hoisted(() => ({ mailPanelProps: vi.fn() }));

vi.mock("@/components/workspaces/mail/MailPanel", () => ({
  MailPanel: (props: unknown) => {
    mailPanelProps(props);
    return <main data-testid="full-page-mail">Full-page Mail</main>;
  },
}));

import { Route } from "./workspaces.$workspaceId.mail";

const WorkspaceMailRoute = (Route as unknown as { component: ComponentType })
  .component;

describe("workspace Mail full-page route (D49)", () => {
  beforeEach(() => {
    mailPanelProps.mockClear();
    mockSearch = {};
  });

  it("renders Mail without chat in the split layout and forwards its address", () => {
    mockSearch = {
      mailbox: "mia",
      folder: "drafts",
      message: "mid:<draft-42@test.local>",
      view: "full",
    };

    render(<WorkspaceMailRoute />);

    expect(screen.getByTestId("full-page-mail")).toBeInTheDocument();
    expect(screen.queryByTestId("chat-screen")).not.toBeInTheDocument();
    expect(mailPanelProps).toHaveBeenCalledWith({
      workspaceId: "ws-1",
      mailboxId: "mia",
      initialFolder: "drafts",
      initialMessageRef: "mid:<draft-42@test.local>",
      layout: "split",
    });
  });
});
