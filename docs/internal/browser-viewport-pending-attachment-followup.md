# Pending attachment viewport ordering

The first viewport request can arrive while its browser attachment is still pending. Retaining the incomplete route discarded that request even when the preceding attachment job subsequently succeeded. Dispatch now retains the original attachment request (epoch and cancellation context), then resolves only that request inside the ordered worker. Replacement attachments cannot inherit it.

The regression uses the real work queue, attachment admission and viewport controller policy; only browser startup is absent. Original completion must produce the expected scoped policy refusal. A replacement must produce no result.

- Red: retained process 14794, exit 1; original completion silently discarded the viewport, replacement control passed (4.266 s).
- Green: retained process 12113, exit 0; focused race/shuffle selection includes the pending pair and existing queued-replacement/current-refusal checks. No broad suite was run.
- Deliberate original-identity bypass was already caught in viewport closeout 51834 by moving attachment capture inside queued execution; the same strengthened replacement test remains in this selection. That fault run was not repeated.
- GitNexus dispatchViewport impact: LOW, one direct caller, two affected symbols, one gateway ServeHTTP flow. No other production function changed.
