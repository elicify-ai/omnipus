# T18 complete gateway/carrier allocation receipt

Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md`::D-A1 / Content-free observability / T18; sections G/H/O of `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md`. Section O is a squad interface decision, not a founder ruling. No E2E consumer edit.

Exact producer: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/chat_history_measurement_test.go`::TestHistoryPageMeasurement_SectionHReceipt.

| Case | Independent expectation |
|---|---|
| Small real operation before expensive setup | Actual newHistoryPageRequest/Read returns its one controlled ordinary entry and encoded generated carrier. Missing implementation fails here, never creates a bogus zero allocation receipt. |
| short_prefix |1,000 actual prefix groups over90 append days plus the identical200-row suffix; real limit40/min_rows20 selects suffix160..199, has_earlier=true/has_later=false. |
| long_prefix |100,000 prefix groups, same90 append days and≥100MiB long canonical fixture, same suffix/selection. |
| Isolated measurement | Fresh request factory plus actual shared reader, projection, carrier preparation/validation/encoding on every benchmark operation; positive samples and actual B/op. Fixture construction/child process/index preparation and positive checks outside measured interval. |
| Failure instrument | Deliberately compose actual full ReadTranscript with that same real operation in a separate negative benchmark. Its prefix allocation growth must violate the≤10% comparator. This is a test instrument control, not an implementation mutation or CHECK claim. |
| Receipt env unset | Both measured records and bounds still asserted in memory; no file/skip/missing-receipt-path failure. |
| Receipt env set | Absolute path; first line exactly `HEAD: <sha>` with ONE space, then exactly two JSON records in short_prefix/long_prefix order. `message=history_page_measurement`, positive integer sample_count, limit40/min_rows20, nonnegative allocated_bytes. Candidate from OMNIPUS_CANDIDATE_SHA if set, otherwise actual git rev-parse HEAD. Write actual measurements BEFORE performance assertions so a RED run retains honest evidence; a file is never a PASS. |

Use the existing Section-G prebuilt native fixture bridge (`OMNIPUS_CHAT_HISTORY_FIXTURE_BINARY`, or `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/build/tools/chat-history-fixture.test`, with the native `.exe` suffix on Windows); no production clock/testing API, unsafe/linkname/reflection, fabricated partition files, test-only index or private-field mutation from gateway. The child driver runs the real writer/rollover and returns its exact count receipt; it exits and closes its store before the reader is opened. CI builds that native driver at this exact candidate before the measurement. Missing binary is a loud BLOCKED setup failure, never a skip. This prerequisite is independent of the optional output receipt path.

All owned stores call public Close before timing: UnifiedStore.Close stops/joins the periodic stats flusher; its archive backend closes no read handles, and the unchanged real read-only page path is positively checked afterward. No AgentLoop/server/unrelated background task is started. No production heap polling or concurrent-request serialization. Go benchmark allocation accounting is used only in this isolated test process.

Squad-lead follow-up: accumulate the unchanged absolute allocation-variance10% failure and the unchanged full-read-control failure as a failing test via t.Errorf at the END. Do not abort at the ordinary bound before later controls can run. The real full-read negative benchmark runs, and an unset-receipt subtest invokes the same writer with the SAME actual measured records, checks they are unchanged and checks an existing receipt is untouched. Setup/measurement errors still fail loudly. CI remains fail-closed on the Go exit regardless of receipt presence; this is never warning demotion or threshold widening.

Heavy1k/100k execution is Fly-only under the assigned080d lock, one active test operation at a time. Local checks are compile-only. The native driver is used only for serial fixture construction, not measured work or a concurrent suite. Preserve child stdout, named result/exit and real rolled-file postconditions.

Source-compatibility correction: the initial producer used the GREEN-only private encodedResponse field, absent from the agreed historical preparation declaration. The probe now independently encodes and schema-validates the actual generated Response outside the interval and checks actual prepared replay frames; the timed factory/Read still executes its unchanged complete production carrier preparation/validation/encoding. The allocation/sample/≤10% oracles are unchanged, and the exact same test source can compile on the historical RED base.

Known gates: this producer does not certify the real E2E I/O summaries, genuine long-session acceptance, native Windows or UAT. Fresh independent CHECK owns production full-read mutations, GREEN re-verification/restored GREEN and Proof-of-failability. Do not claim any of those from this source or an allocation receipt.
