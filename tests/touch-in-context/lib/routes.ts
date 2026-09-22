import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { APIRequestContext } from '@playwright/test';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

/**
 * design-system/surfaces.json is the checked-in inventory this check is
 * required to run against (D17 enforcement). Only `kind: "route"` entries
 * carry a real, directly-navigable URL template — `redirect` entries resolve
 * to a route already in this list (so including them would just double-test
 * the same destination), and `tab`/`modal`/`screen` entries are reached BY
 * navigating a route, not navigated to directly.
 */
const SURFACES_JSON_PATH = path.resolve(__dirname, '../../../design-system/surfaces.json');

export interface RouteSurface {
  id: string;
  /** e.g. "/workspaces/:workspaceId/chat" */
  templatePath: string;
}

interface SurfacesFile {
  surfaces: Array<{
    id: string;
    kind: string;
    entry?: { type?: string; path?: string };
  }>;
}

export function loadRouteSurfaces(): RouteSurface[] {
  const raw = JSON.parse(readFileSync(SURFACES_JSON_PATH, 'utf8')) as SurfacesFile;
  return raw.surfaces
    .filter((s) => s.kind === 'route' && s.entry?.type === 'route' && typeof s.entry.path === 'string')
    .map((s) => ({ id: s.id, templatePath: s.entry!.path! }));
}

/** The three dynamic path params present in the inventory today (D9/router param names). */
export interface DynamicIds {
  workspaceId: string | null;
  agentId: string | null;
  sessionId: string | null;
}

/**
 * Discover live ids for the dynamic route params by calling the same REST
 * endpoints the SPA itself uses. Best-effort per endpoint: a failure to
 * discover one id (e.g. a fresh home with zero sessions) must not stop
 * discovery of the others — routes needing the missing id are reported as
 * skipped (see resolveRoutes), not fatal.
 */
export async function discoverDynamicIds(request: APIRequestContext): Promise<DynamicIds> {
  const ids: DynamicIds = { workspaceId: null, agentId: null, sessionId: null };

  try {
    const resp = await request.get('/api/v1/workspaces');
    if (resp.ok()) {
      const list = (await resp.json()) as Array<{ id?: string }>;
      ids.workspaceId = list[0]?.id ?? null;
    }
  } catch {
    // Left null — surfaced as a per-route skip, not a suite crash.
  }

  try {
    const resp = await request.get('/api/v1/agents');
    if (resp.ok()) {
      const list = (await resp.json()) as Array<{ id?: string }>;
      ids.agentId = list[0]?.id ?? null;
    }
  } catch {
    // ditto
  }

  try {
    const resp = await request.get('/api/v1/sessions');
    if (resp.ok()) {
      const body = (await resp.json()) as { sessions?: Array<{ id?: string }> };
      ids.sessionId = body.sessions?.[0]?.id ?? null;
    }
  } catch {
    // ditto
  }

  return ids;
}

export interface ResolvedRoute {
  id: string;
  templatePath: string;
  /** null when one or more required params had no live id to substitute. */
  resolvedPath: string | null;
  /** Present only when resolvedPath is null — why this route was skipped. */
  skippedReason: string | null;
}

const PARAM_TO_ID: Record<string, keyof DynamicIds> = {
  workspaceId: 'workspaceId',
  agentId: 'agentId',
  sessionId: 'sessionId',
};

export function resolveRoutes(surfaces: RouteSurface[], ids: DynamicIds): ResolvedRoute[] {
  return surfaces.map((surface) => {
    const params = [...surface.templatePath.matchAll(/:([a-zA-Z]+)/g)].map((m) => m[1]);
    const missing = params.filter((p) => {
      const key = PARAM_TO_ID[p];
      return !key || !ids[key];
    });
    if (missing.length > 0) {
      return {
        id: surface.id,
        templatePath: surface.templatePath,
        resolvedPath: null,
        skippedReason: `no live id available to resolve :${missing.join(', :')} in "${surface.templatePath}"`,
      };
    }
    let resolvedPath = surface.templatePath;
    for (const p of params) {
      const key = PARAM_TO_ID[p];
      resolvedPath = resolvedPath.replace(`:${p}`, ids[key] as string);
    }
    return { id: surface.id, templatePath: surface.templatePath, resolvedPath, skippedReason: null };
  });
}
