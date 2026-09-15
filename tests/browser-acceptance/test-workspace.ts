// Optional isolated UAT workspace; existing recorded scenarios keep their default.
export const browserTestWorkspaceID = process.env.BROWSER_INPUT_WORKSPACE_ID || '01M01TTSDZBFGM28NPHGTFZ17T';
if (!/^[0-9A-HJKMNP-TV-Z]{26}$/.test(browserTestWorkspaceID)) throw Error('A valid UAT workspace identifier is required');
export const browserTestWorkspacePath = `/#/workspaces/${browserTestWorkspaceID}/chat`;
