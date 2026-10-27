# WU-4 P0-a: configured credential damage — RED plan

Oracle: the founder's P0-a publication brief (2026-10-17): a damaged configured provider/mailbox/channel entry must produce a clear scoped error, never panic or refuse an otherwise readable gateway; healthy siblings stay fully usable; logs and errors contain no plaintext or nonce material. Locked, wrong-key and whole-file failures remain fatal. Error implementation types are deliberately not an oracle.

| Cases | Input | Required observable result |
|---|---|---|
| Provider × boot/reload/REST refresh | Configured API key entry with an 11-byte nonce (one below AES-GCM's required 12) | Operation succeeds; ERROR names ref; provider Settings row reports error; healthy provider executes HTTP-backed chat and healthy channel sends |
| Mailbox × boot/reload/REST refresh | Configured password entry with the same wrong-length nonce | Operation succeeds; ERROR names ref; mailbox stays configured/enabled; healthy password still resolves exactly; healthy provider/channel still execute |
| Channel × boot/reload/REST refresh | Enabled LINE secret ref points to an entry with malformed Base64 nonce | Operation succeeds; ERROR names ref; boot/reload channel is visibly degraded; healthy same-type channel sends and provider executes |
| Healthy control | Same configured provider, mailbox and channel fixtures without tampering | Boot, reload and REST refresh succeed; exact original credentials resolve; healthy channel/provider execute |
| Fatal controls (existing, unchanged) | Locked store, genuinely different key, malformed whole-file JSON | Existing refusal tests still reject; no broadening of the per-entry exception |

Boundary: real encrypted store, file fault, config loading, boot, reload, REST-refresh function, provider factory, channel manager and Settings handlers. Only external provider/LINE HTTP is redirected by the existing loopback fixture. No production code or error classifier is mocked. The REST-refresh seam is not a claim that it reconstructs channels; visible channel reconstruction is checked at boot/reload.

Every fault row first proves all entries decrypt and healthy peers execute. It then changes only the selected nonce, proves the selected ref fails with an empty value while siblings still decrypt, and checks that the credential file is not rewritten. Captured credential diagnostics and returned/Settings errors are scanned for all fixture plaintexts, original stored nonces/ciphertexts and the damaged nonce marker. The ERROR record is a positive capture control, so an empty logger cannot satisfy visibility.

Mutation targets for independent CHECK: restore early fatal classification of configured encoding errors; suppress the ref-naming ERROR; drop a healthy sibling from the resolved bundle; leak a nonce/plaintext in an error. GREEN, production mutations and the Proof-of-failability checklist are deferred to a fresh CHECK instance. This RED increment does not claim full-suite, live UAT or email-server delivery coverage.

## RED publication receipt

Production base: `f8a088adc9958a9910883d1918df9227a256baf3`, unchanged by this pack. Final test source was exercised through serial slotrun with `CGO_ENABLED=0`, tags `goolm,stdjson`, `-count=1 -v -p 1` and selector `^TestWU4P0A(ConfiguredDamageDegradesOnlyItsConsumer|HealthyConfiguredConsumersApplyAllBoundaries)$` in the gateway package. All nine damaged-entry leaves fail at the required continue-vs-reject assertion; the three healthy-consumer leaves pass. No compilation failure, panic or empty collection is used as RED evidence. The remaining post-acceptance assertions await GREEN/CHECK; their presence is not claimed as proof they ran on this RED source.

Raw final-source receipt: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/p0a-configured-damage/published-source-red-and-healthy.log` (exit 1). Independent unchanged refusal controls `TestIssue1277_RefreshLockedStoreRejectsAndKeepsOldConfig`, `TestIssue1277_WrongWholeStoreKeyRemainsFatal` and `TestIssue1277_CorruptStoreRemainsFatal` each pass (exit 0), with exact commands and logs in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/p0a-configured-damage/controls-receipts.json`. These are fatality controls, not the defect's RED evidence.

Both structured credential diagnostics and the native application-log file are captured. A real config-save record proves the native capture is active; an ERROR naming the configured ref is required after acceptance. Leak/file-preservation assertions execute even on early refusal. Impact analysis used the documented direct-search fallback because no GitNexus tools or checkout index are available: only new test helpers and the acceptance map are added; no existing symbol or production file is edited.
