# PSC=C regression replacement — plan

Authority: architect Section P and spec-only commit `a1cd8e484dce4436c41268470b32de707cf3f910`, read in full and normally merged alone at `dcea90507ba83ec5c19bddbd73aa771444d4458e`. No GREEN implementation was merged. This explicit technical ruling resolves the earlier held assertion; it is not permission to add a wire field or delete stored provenance.

Real boundary: unchanged seedParentAndChildTranscript writes both real canonical entries; actual REST messages/detail, generated carrier, raw nested JSON and real internal ReadTranscript are all exercised. No fake projection, archive or schema.

| Existing oracle | Exact replacement/addition | Source |
|---|---|---|
| Both parent/tagged entries remain visible | Exactly parent-msg-1,child-msg-1 in acceptance order, two rows; preserve all narration, agent, model and turn values | Existing unfiltered regressions; P2/D15 |
| Old raw wire ParentSpawnCallID=delegate-call-1 | Move this provenance equality to separate actual store.ReadTranscript child lookup; wire now explicitly omits the key on EVERY raw entry | PSC=C, P2 |
| No privacy check after generated decode | Check raw actual JSON objects for parent_spawn_call_id absence, not just typed decoding (which ignores unknown keys) | P2 explicit requirement |
| Stable source identity | Valid distinct history_position values on both generated public entries | D2/P2 |
| Archive untouched | Canonical transcript bytes captured before and compared after API/internal reads | P2 / FR-036 retention |

Both sibling tests retain their existing parent/child presence/content assertions while adding the precise generated metadata/raw-key/internal boundary checks. No alias in model/turn/span/session fields; existing legitimately recorded public values are compared exactly. No visibility filter or archive tag/writer change. Before editing, manual caller sweep is used because GitNexus is disconnected; impact is Inferred test-only, not graph output.

RED must be an exact named handler run on the pre-GREEN base; expected first failure is old bare-array/missing detail history_state, not compile/no-match. Later raw privacy/provenance and canonical preservation assertions remain unproved until GREEN. Fresh CHECK must mutate by leaking the raw key, deleting the stored tag/value, filtering the child or blanking its public content. Author does not run implementation mutations or self-CHECK. User/agent reachability and UAT are not claimed.
