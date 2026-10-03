import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "@/store/ui";
import { openMailDeepLink } from "./mailDeepLink";
import { readMailPanelIntent } from "./mailPanelIntent";

describe("Mail chat deep links", () => {
  beforeEach(() => {
    sessionStorage.clear();
    window.location.hash = "";
    useUiStore.setState({ activePanel: null });
  });

  it("keeps chat on screen while replacing the active panel and carrying the draft address", () => {
    useUiStore.setState({
      activePanel: { id: "library", context: { workspaceId: "ws-1" } },
    } as never);

    const opened = openMailDeepLink(
      "https://public.example/#/workspaces/ws-1/mail?mailbox=mia&folder=drafts&message=mid%3A%3Cdraft-1%40test%3E",
    );

    expect(opened).toBe(true);
    expect(useUiStore.getState().activePanel).toEqual({
      id: "mail",
      context: { workspaceId: "ws-1" },
    });
    expect(readMailPanelIntent("ws-1")).toEqual({
      agentId: "mia",
      folder: "drafts",
      messageRef: "mid:<draft-1@test>",
    });
    expect(window.location.hash).toBe("#/workspaces/ws-1/chat?panel=mail");
  });

  it("does not change state for a non-Mail link", () => {
    expect(openMailDeepLink("https://example.test/#/library")).toBe(false);
    expect(useUiStore.getState().activePanel).toBeNull();
    expect(window.location.hash).toBe("");
  });
});
