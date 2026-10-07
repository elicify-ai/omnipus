// Production registry contract at wave 3. Library, Browser, Mail, Tasks, Team
// and Calendar are registered (spec §10 Wave 2 + Wave 3, FR-014); any other id
// resolves nothing. Every registered definition uses the shell-owned
// full-screen codec, and panels that can hold unsaved edits expose the shared
// leave gate.

import { describe, expect, it } from "vitest";
import type { PanelDefinition } from "@/components/panel-shell/types";

// Dynamic import keeps a missing/mis-exported production registry as an
// explicit acceptance failure rather than a collection-time skip.
const MODULE_PATH = "@/components/panel-shell/registry";

async function loadRegistry(): Promise<{ panels: PanelDefinition[] }> {
  let mod: unknown;
  try {
    mod = await import(/* @vite-ignore */ MODULE_PATH);
  } catch (importErr) {
    throw new Error(
      `BLOCKED: production panel registry module not implemented — required by side-panel-shell-spec.md §8.1/§12 test #1 (import of ${MODULE_PATH} failed: ${importErr instanceof Error ? importErr.message : String(importErr)})`,
      { cause: importErr },
    );
  }
  const panels =
    (mod as { panels?: PanelDefinition[] }).panels ??
    (mod as { PANEL_REGISTRY?: PanelDefinition[] }).PANEL_REGISTRY;
  if (!Array.isArray(panels)) {
    throw new Error(
      `BLOCKED: ${MODULE_PATH} exists but exports no panels/PANEL_REGISTRY array — required by side-panel-shell-spec.md §8.1/§12 test #1`,
    );
  }
  return { panels };
}

describe("panel registry — wave-2/3 registrations (§12 #1, SC-002)", () => {
  it("exposes a production registry module the shell can consume (spec §8.1; missing module fails loudly, never skips)", async () => {
    let panels: PanelDefinition[] | null = null;
    let blocked = "";
    try {
      panels = (await loadRegistry()).panels;
    } catch (err) {
      blocked = err instanceof Error ? err.message : String(err);
    }
    expect(blocked, blocked || "registry module must exist").toBe("");
    expect(panels).not.toBeNull();
  });

  it("registers exactly the six panels — library, browser, mail (wave 2) plus tasks, team, calendar (wave 3) (spec §10 Wave 2/3 + FR-014; supersedes the wave-2 exactly-three pin)", async () => {
    // Oracle: spec §10 "Wave 3 — Team, Tasks, Calendar become panels (SP-6)"
    // and FR-014 (a new panel is one registry entry). The wave-2
    // exactly-three pin is superseded: the registered set is the six panels
    // named in the spec, alphabetically sorted here only for a stable compare.
    const { panels } = await loadRegistry();
    expect(panels.map((p) => p.id).sort()).toEqual([
      "browser",
      "calendar",
      "library",
      "mail",
      "tasks",
      "team",
    ]);
  });

  it('library resolves: title "Library" and its full-screen codec round-trips workspace plus path (SP-38/R11)', async () => {
    const { panels } = await loadRegistry();
    const library = panels.find((p) => p.id === "library");
    expect(library).toBeDefined();
    expect(library?.title).toBe("Library");
    expect(library?.fullScreen).toBeDefined();
    const context = { workspaceId: "ws-1", path: "Notes/Current.md" };
    const search = library?.fullScreen.toSearch(context);
    expect(search).toEqual({ workspace: "ws-1", path: "Notes/Current.md" });
    expect(library?.fullScreen.fromSearch(search ?? {})).toEqual(context);
  });

  it('browser resolves: title "Browser" and its full-screen codec round-trips session plus agent (SP-38/R11)', async () => {
    const { panels } = await loadRegistry();
    const browser = panels.find((p) => p.id === "browser");
    expect(browser).toBeDefined();
    expect(browser?.title).toBe("Browser");
    expect(browser?.fullScreen).toBeDefined();
    const context = { sessionId: "s1", agentId: "a1" };
    const search = browser?.fullScreen.toSearch(context);
    expect(search).toEqual({ session: "s1", agent: "a1" });
    expect(browser?.fullScreen.fromSearch(search ?? {})).toEqual(context);
  });

  it("library carries beforeLeave (the CRIT-001 unsaved-edits guard — §8.1); browser carries NONE (transitions replace it freely)", async () => {
    const { panels } = await loadRegistry();
    const library = panels.find((p) => p.id === "library");
    const browser = panels.find((p) => p.id === "browser");
    expect(typeof library?.beforeLeave).toBe("function");
    expect(browser?.beforeLeave).toBeUndefined();
  });

  it("mail resolves with the shared full-screen codec and unsaved-edit leave guard (SP-38/R11, D49)", async () => {
    const { panels } = await loadRegistry();
    const mail = panels.find((p) => p.id === "mail");
    expect(mail).toBeDefined();
    expect(mail?.title).toBe("Mail");
    const context = {
      workspaceId: "ws-1",
      mailboxId: "mia",
      folder: "drafts" as const,
      messageRef: "mid:<draft-42@test.local>",
    };
    const search = mail?.fullScreen.toSearch(context);
    expect(mail?.fullScreen.fromSearch(search ?? {})).toEqual(context);
    expect(mail).not.toHaveProperty("expandTarget");
    expect(mail?.beforeLeave).toBeTypeOf("function");
    expect(mail?.beforeLeaveRequired).toBeTypeOf("function");
  });

  it.each([
    ["tasks", "Tasks"],
    ["team", "Team"],
    ["calendar", "Calendar"],
  ])(
    "%s resolves at wave 3 with title %s and a workspace-scoped full-screen codec (SP-6, SP-38/R11, FR-014)",
    async (id, title) => {
      const { panels } = await loadRegistry();
      const panel = panels.find((p) => p.id === id);
      expect(panel).toBeDefined();
      expect(panel?.title).toBe(title);
      const context = { workspaceId: "ws-1" };
      const search = panel?.fullScreen.toSearch(context);
      expect(search).toEqual({ workspace: "ws-1" });
      expect(panel?.fullScreen.fromSearch(search ?? {})).toEqual(context);
    },
  );

  it("unregistered ids resolve NOTHING — an id outside the six registered panels is dropped exactly like an unknown one (MAJ-012; settings stays a page in every wave, SP-6)", async () => {
    const { panels } = await loadRegistry();
    for (const id of ["bogus", "settings", "chat"]) {
      expect(panels.find((p) => p.id === id)).toBeUndefined();
    }
  });
});
