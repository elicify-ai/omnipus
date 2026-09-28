// Mail's final shared-panel contract. Expand is shell-owned: the definition
// supplies a context codec for the chrome-less /panel/mail route, while the
// content reports its current selection through registerExpandContext.

import { Suspense } from "react";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  PanelContentProps,
  PanelContext,
  WorkspacePanelContext,
} from "@/components/panel-shell/types";

const { mailPanelModuleLoaded, mailPanelProps } = vi.hoisted(() => ({
  mailPanelModuleLoaded: vi.fn(),
  mailPanelProps: vi.fn(),
}));

interface MockMailPanelProps {
  workspaceId: string;
  mailboxId?: string | null;
  initialFolder?: string;
  initialMessageRef?: string;
  layout?: "stacked" | "split";
  onLocationChange?: (location: {
    mailboxId: string | null;
    folder: string;
    messageRef: string | null;
  }) => void;
}

vi.mock("./MailPanel", () => {
  mailPanelModuleLoaded();
  return {
    MailPanel: (props: MockMailPanelProps) => {
      mailPanelProps(props);
      return (
        <div data-testid="mock-mail-panel">
          Loaded Mail for {props.workspaceId}
        </div>
      );
    },
  };
});

import { mailPanelDefinition } from "./mailPanelDefinition";

function renderContent(
  presentation: PanelContentProps["presentation"],
  context: WorkspacePanelContext,
  registerExpandContext: PanelContentProps["registerExpandContext"] = () =>
    undefined,
) {
  const Content = mailPanelDefinition.content;
  return render(
    <Suspense fallback={<div>Loading Mail…</div>}>
      <Content
        context={context}
        presentation={presentation}
        close={() => undefined}
        expand={() => undefined}
        registerExpandContext={registerExpandContext}
        onWidthSettle={() => undefined}
      />
    </Suspense>,
  );
}

afterEach(() => {
  cleanup();
  mailPanelProps.mockClear();
});

describe("Mail PanelDefinition shared full-screen contract (R10/R11, D48/D49)", () => {
  it('registers id "mail" with title "Mail"', () => {
    expect(mailPanelDefinition.id).toBe("mail");
    expect(mailPanelDefinition.title).toBe("Mail");
  });

  it("round-trips every Mail address through fullScreen and rejects junk", () => {
    const longMessageRef = `mid:<${"thread.".repeat(700)}final@example.test>`;
    const contexts: WorkspacePanelContext[] = [
      {
        workspaceId: "workspace A",
        mailboxId: "mia/primary",
        folder: "drafts",
        messageRef: longMessageRef,
      },
      {
        workspaceId: "workspace A",
        mailboxId: null,
        folder: null,
        messageRef: null,
      },
      {
        workspaceId: "workspace A",
        mailboxId: undefined,
        folder: "inbox",
        messageRef: "mid:<ordinary@example.test>",
      },
    ];

    for (const context of contexts) {
      const search = mailPanelDefinition.fullScreen.toSearch(context);
      const decoded = mailPanelDefinition.fullScreen.fromSearch(search);
      expect(decoded).toEqual(context);
      expect(decoded?.mailboxId).toBe(context.mailboxId);
    }

    expect(
      mailPanelDefinition.fullScreen.fromSearch({
        workspace: 42,
        mailbox: ["mia"],
        folder: "spam",
        message: { id: "not-a-string" },
      }),
    ).toBeNull();
    expect(mailPanelDefinition).not.toHaveProperty("expandTarget");
  });

  it("loads Mail panel code only when the registered content is rendered", async () => {
    expect(mailPanelModuleLoaded).not.toHaveBeenCalled();

    renderContent("docked", { workspaceId: "ws-1" });

    expect(await screen.findByText("Loaded Mail for ws-1")).toBeInTheDocument();
    expect(mailPanelModuleLoaded).toHaveBeenCalledTimes(1);
  });

  it("registers a getter for the current mailbox, folder and message, then unregisters on unmount", async () => {
    const registrations: Array<(() => PanelContext) | null> = [];
    const view = renderContent(
      "docked",
      {
        workspaceId: "ws-1",
        mailboxId: "mia",
        folder: "inbox",
        messageRef: null,
      },
      (getter) => registrations.push(getter),
    );
    await screen.findByTestId("mock-mail-panel");

    const props = mailPanelProps.mock.lastCall?.[0] as MockMailPanelProps;
    act(() => {
      props.onLocationChange?.({
        mailboxId: "mia agent",
        folder: "drafts",
        messageRef: "mid:<draft/42@test.local>",
      });
    });

    await waitFor(() => {
      const getter = [...registrations]
        .reverse()
        .find(
          (entry): entry is () => PanelContext => typeof entry === "function",
        );
      expect(getter?.()).toEqual({
        workspaceId: "ws-1",
        mailboxId: "mia agent",
        folder: "drafts",
        messageRef: "mid:<draft/42@test.local>",
      });
    });

    view.unmount();
    expect(registrations.at(-1)).toBeNull();
  });

  it("chooses stacked versus split layout only from the shell presentation", async () => {
    const context: WorkspacePanelContext = {
      workspaceId: "ws-1",
      mailboxId: "mia",
      folder: "sent",
      messageRef: "mid:<same-context@example.test>",
    };

    const docked = renderContent("docked", context);
    await screen.findByTestId("mock-mail-panel");
    expect(mailPanelProps).toHaveBeenLastCalledWith(
      expect.objectContaining({ layout: "stacked" }),
    );
    docked.unmount();

    mailPanelProps.mockClear();
    renderContent("fullscreen", context);
    await screen.findByTestId("mock-mail-panel");
    expect(mailPanelProps).toHaveBeenLastCalledWith(
      expect.objectContaining({ layout: "split" }),
    );
  });
});
