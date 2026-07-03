// Step2Personality — wizard step ② (Personality).
//
// Per spec FR-017 / US-5.AC3: heartbeat fields have been removed from this
// step — heartbeat is now workspace-scoped (member_configs) and NOT set at
// agent-creation time.
//
// PARITY (P3 bug, 2026-07-03): this step used to carry a drifted duplicate of
// the profile's Behavior section — upload button ABOVE the textarea, no
// SOUL.md hint, no minLength hint, different textarea styling. It now renders
// the SAME shared `BehaviorFields` the edit profile uses (that component
// exists precisely so create and edit cannot drift), with the shared
// `UploadMdButton` placed below the box.
//
// Create-time soul is REQUIRED for every type (AgentCreateRequest.soul is
// minLength: 1; the server rejects whitespace-only) — `soulRequired` renders
// the asterisk + the minimum-length hint.
//
// W4 testids (unchanged contract):
//   wizard-soul, wizard-voice (Main only), wizard-soul-upload

import { BehaviorFields, UploadMdButton } from '../AgentFormFields'
import type { StepProps } from './types'

export function Step2Personality({ payload, setField, initialType }: StepProps) {
  const isWorker = initialType !== 'Main'

  return (
    <BehaviorFields
      isWorker={isWorker}
      soul={payload.soul}
      setSoul={(v) => setField('soul', v)}
      voice={payload.voice ?? ''}
      setVoice={(v) => setField('voice', v)}
      soulRequired
      soulTestId="wizard-soul"
      voiceWrapperTestId="wizard-voice"
      renderUploadButton={(_target, onUpload) => (
        <UploadMdButton onUpload={onUpload} testId="wizard-soul-upload" />
      )}
    />
  )
}

export default Step2Personality
