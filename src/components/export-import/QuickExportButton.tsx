// QuickExportButton — the outline **Export** button in a custom agent's edit
// sheet (spec US-3): closes the sheet and navigates to
// /export-import?wizard=export&preselect=agent:<id>; not rendered for
// built-in agents or off the local edition.
//
// RED STUB (issue #1967 slice B1, test author scaffold): renders nothing.
// The props below are the sheet's own data (agent identity) plus the sheet's
// close callback — the button's visible behaviour is the tests' contract.
export interface QuickExportButtonProps {
  agent: { id: string; name: string; type: 'core' | 'system' | 'Main' | 'Subagent' | 'subagent_3p'; locked: boolean }
  onClose?: () => void
}

export function QuickExportButton(_props: QuickExportButtonProps) {
  void _props
  return null
}
