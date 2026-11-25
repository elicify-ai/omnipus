# Vendored PDF parser: provenance

This package is a vendored, hardened copy of the PDF reader
`github.com/ledongthuc/pdf`. It parses PDFs that users and agents supply, so
every parse runs under a work, text and wall-clock budget, with structural
bounds on what is allocated before work is spent (issue #1968, security
finding F1).

| What | Value |
|---|---|
| Upstream module | `github.com/ledongthuc/pdf` |
| Upstream version | `v0.0.0-20250511090121-5959a4027728` |
| License | BSD 3-clause (copyright The Go Authors), kept unchanged in `LICENSE` |
| Verbatim copy | commit `a3a977ba78` (`553fe8eeb4` before the branch was rebased), "chore(docextract): vendor ledongthuc/pdf source verbatim for hardening (#1968)": the upstream `.go` sources and `LICENSE`, byte for byte |
| Hardening | commit `7b808aeb0e` (`46ac345979` before the rebase), "fix(docextract): bound untrusted PDF parsing work, memory and text (#1968)" |

Only the reading and plain-text path the engine uses was kept. The upstream
test files, examples and the page layout APIs were not vendored or were
removed (listed below).

## Modifications after the verbatim copy

The changes below were made in `7b808aeb0e` and in the #1968 fix rounds 2
(overflow-safe admission, metered CPU work, text-budget truncation), 3
(metered xref rows, bounded filter chains, indexed CMap lookup, rejected CMaps
as errors, wall-clock ceiling), 4 (CMap entries admitted and charged before
they are stored, duplicates and unusable codes kept out of the index), 5
(reuse of already-parsed values charged, `/Differences` decoded through a
table built once, dictionary-key sorts charged, error messages bounded), 6
(fonts shared between pages only by object identity, cut error-message values
marked) and 8 (the three-operand `"` text operator shows its text).
To list every top-level declaration changed since the verbatim copy,
compare `a3a977ba78` with the current tree.

The budgets are parser safety bounds against hostile files, not provider
limits. Each bound reached ends the parse with a typed error:
`ErrWorkLimit`, `ErrTextLimit` or an `ErrStructureLimit` error. The bounds
raise a panic, and the callers in `pkg/docextract` recover it.

### New file

- `limits.go`:
  - the structural bounds:
    - cross-reference table entries and sections, and field width;
    - indirect-object resolve depth;
    - object nesting and elements per object;
    - token size;
    - operand and dictionary stacks;
    - predictor columns;
    - inherited-attribute depth;
    - per-resolve work cost;
    - filter-chain stages (`maxFilterStages`, 16) and the work charged to
      build each stage (`filterStageWork`);
    - entries one cmap keeps (`maxCmapEntries`, 1,048,576) and the work
      charged for keeping each (`cmapEntryWork`, 64, about the bytes it
      occupies), so the memory a cmap retains stays within the work budget.
  - `Limits` (the work and text budgets, and the optional wall-clock `Time`)
    and the internal `limiter` (`newLimiter`);
  - `Reader.charge`, `Reader.chargeText`;
  - `Reader.chargeReuse`: charges for handing out an already-parsed value
    again without lexing it (a name bound with `def`, a `currentdict`, an
    operand `defineresource` pushes back): one unit per string or name byte
    and per array or dictionary entry, at least one. The lexer charged the
    value once; every later use is walked or hashed again by its consumer
    (`TJ`, `def`, the dictionary lookups, a decoder);
  - `Reader.checkClock`: the wall-clock ceiling. Every 1,024th charge reads
    the clock, and the parse ends with an `ErrWorkLimit` error once
    `Limits.Time` has passed since `NewReader` (`defaultParseTime`, 10
    seconds, when zero). It is sampled, not preemptive: it is defence in
    depth behind the work budget for a loop whose meter is too cheap, and is
    checked wherever work is charged, which includes every xref row, cmap
    entry, interpreter reuse and `TJ` element. A loop that charges nothing at
    all is not covered by it. Loops either charge as they run, or run over
    something whose size was already charged (lexed bytes, admitted entries,
    a fixed domain such as 256 byte values); fix round 5 found loops of the
    second kind whose input could be reused without being lexed again, and
    charged their reuse. This is an accounting rule the code follows, not a
    proof that no uncharged path remains. Ordinary documents parse in well under a
    second; ten seconds leaves room for the largest accepted file on a loaded
    host and stays under the callers' own test deadlines.
  - `Reader.textRoom`, `Reader.noteTextStop`, `Reader.takeTextStop`: the text
    budget left, and a latch that carries a decode's text-budget stop to the
    caller after the decoded prefix is written;
  - `Reader.growXref`: grows the cross-reference table to an entry number,
    which must lie below the entry bound, and charges one unit of work per new
    slot;
  - `structuralPanicf`;
  - `chargedReader`, which charges every decoded stream byte to the work budget.

### `read.go`

- `NewReader`, `NewReaderEncrypted`:
  - take a `Limits` argument; both budgets must be positive and `Time` must
    not be negative;
  - reject files under 10 bytes;
  - compare errors with `errors.Is`.
- `Reader`: gains the `lim` field.
- `Reader.section` (new): a section reader over the file.
- `checkPrevSection` (new): a `/Prev` offset must lie inside the file and appear only once, and the chain of sections is capped.
- `readXrefStream`:
  - bounds `/Size`;
  - detects `/Prev` loops;
  - type-checks the previous stream instead of a forced type assertion;
  - wraps errors with `%w`.
- `readXrefStreamData`:
  - bounds the `/W` field widths;
  - keeps every `/Index` section inside `/Size`, compared without adding
    start and count, so a start near the integer maximum cannot wrap past
    the check;
  - rejects `/W` fields that are all zero (a zero-byte row reads nothing, so
    no byte meter sees it) and `/Index` counts whose sum exceeds `/Size`
    (repeated sections would multiply the rows);
  - charges one unit of work per row;
  - grows the table through `Reader.growXref`.
- `readXrefTable`: detects `/Prev` loops and renames the shadowed variables.
- `readXrefTableData`: bounds each section's start and count, compared
  without adding them (no wrap), charges one unit of work per row, and grows
  the table through `Reader.growXref`.
- `Reader.resolve`:
  - charges work for every indirect resolution;
  - bounds the resolve depth, so cycles of object streams or `/Length` objects end as an error;
  - stops reading an object-stream header that ends early.
- `Value.Reader`:
  - clamps a negative `/Length` to 0;
  - wraps the raw stream and the output of every filter stage in
    `chargedReader`, so decompression stops at the budget even when a later
    filter (for example ASCII85, which skips whitespace) discards most of an
    earlier stage's output;
  - refuses a `/Filter` array of more than `maxFilterStages` stages before
    building any decoder, and charges `filterStageWork` for each stage
    before it is built (each decoder is allocated, and retained by the next,
    before the first byte is read).
- `applyFilter`: bounds the predictor `/Columns`; an unknown filter name in
  its panic message is cut with `clipText`.
- `Value.Keys`: charges about the comparisons its sort makes (the key bytes
  times log2 n) before sorting, since a page's resource dictionary is sorted
  again for every page.
- `objfmt`: renders into one buffer through `writeObjfmt` (new) and stops at
  `maxObjfmtBytes` (1,024 bytes, cut on a rune boundary by `clipText`, new);
  a dictionary of more than `maxObjfmtKeys` (64) entries is shown as its
  entry count. Upstream copied each nested level into its parent, so a
  deeply nested object holding a long string made a message of the whole
  string after hundreds of copies. A string is cut to `maxObjfmtBytes`
  source bytes before it is decoded; that cut, and any element left out
  because the message is full, is marked with `…` (a decoded UTF-16 string
  can render shorter than the bound, so the final cut alone would not show
  it). Error messages that printed a raw token (`readXref`, `/Prev` values)
  use `objfmt` too.
- Removed: `Open` (file-path API), `DebugOn` and its debug prints, and `Reader.errorf`.
- Lint-only edits, with no behaviour change:
  - `interface{}` changed to `any` (`Value`, `objfmt`, `resolve`);
  - errors wrapped with `%w` (`initEncrypt`);
  - `ioutil` changed to `io`;
  - unused results dropped (`okayV4`);
  - a builtin-shadowing name renamed (`findLastLine`);
  - comment quote style.

### `lex.go`

- `buffer`: gains `owner` (the work budget) and `depth` (nesting).
- `newBuffer`: takes the owning `Reader`.
- `buffer.reload`: charges the consumed bytes. `buffer.seekForward` charges every skipped chunk.
- `buffer.appendToken` (new): caps token length. It is used by:
  - `readHexString`, which also rejects an unterminated hex string at end of input;
  - `readLiteralString`;
  - `readName`;
  - `readKeyword`.
- `buffer.enter` and `buffer.leave` (new) bound nesting. They are used by:
  - `readArray`, which also rejects an unterminated array and caps element count;
  - `readDict`, which also caps entry count and drops a debug print;
  - nested `obj` in `readObject`.
- `isEOFToken` (new): an end-of-input check that works with wrapped errors.
- Error messages that quoted a whole token (a non-name dictionary key, an
  invalid integer or real, an unexpected keyword) cut it with `clipText`.
- Removed: `buffer.seek` (unused).
- Lint-only edits: `token`, `object` and `errorf` switched from `interface{}` to `any`.

### `ps.go`

- `Stack.Push`: caps the operand stack.
- `Stack.popRequired` (new): pops, but ends the parse with a structure error
  when the stack is empty (upstream's `Pop` returns a null value, so a loop
  over a forged count kept running).
- `Interpret`:
  - charges its lexer to the stream's reader;
  - caps the dictionary stack;
  - checks `begin`'s operand type;
  - uses `isEOFToken`;
  - moves the `default` case of the operator switch last, with the same behaviour;
  - charges every operator's dictionary probes (keyword bytes times open
    dictionaries; the lexer paid for the bytes once), and charges
    `Reader.chargeReuse` for a value found there and for `currentdict`. A
    short name bound to a long array, name or string can therefore no
    longer be pushed, shown or redefined over and over for the cost of the
    short name.
- Removed: `seqReader` (unused).

### `page.go`

- `Reader.GetPlainText`:
  - one walk of the page tree (`Reader.plainTextWalk`, new), capped by the tree's `/Count` and depth and charged per node;
  - upstream looked up each page by number, which made a huge `/Count` or a `/Kids` cycle take very long;
  - returns partial text when the text budget runs out.
- `Page.GetPlainText`:
  - charges the text budget for every emitted byte;
  - turns a recovered panic into an error;
  - keeps the partial text on `ErrTextLimit`.
- `Page.findInherited`: bounds the `/Parent` chain, which upstream followed
  into cycles, and charges one unit per step (a direct `/Parent` is not
  resolved, so nothing else charges it).
- `FontCache`, `NewFontCache`, `FontCache.PageFonts` (new): each page gets
  its own map from its resource font names to fonts; a font is shared with
  another page only when both name the same indirect object. Upstream kept
  one name-keyed map for the whole document and never replaced an entry, so
  a later page whose `/F1` was a different font decoded its text with the
  earlier page's `/F1`. `Reader.plainTextWalk` takes the cache instead of
  the map.
- `Font.Encoder`: pointer receiver, so the cached encoder is actually kept. Upstream re-parsed the cmap at every font change.
- `Font.getEncoder`: debug prints removed.
- `cmap`:
  - gains `owner` and `entries`; loses the `bfchar` slice (the `bfchar` type
    is removed), whose entries now go straight into the index;
  - `cmap.admitEntry` (new) refuses an entry past `maxCmapEntries` and
    charges `cmapEntryWork` before the entry is stored; `usableCode` (new)
    tells the source codes Decode can look up (1 to 4 bytes);
  - gains `idx`, a `cmapIndex` (new): the codespace ranges as bitmaps for
    one- and two-byte codes and as sorted, merged spans for three- and
    four-byte codes; the bfchar entries as a map, filled by `readCmap` as it
    parses (the first entry for a code wins, as in upstream's scan); the
    bfranges as disjoint sorted spans per code length, each naming the first
    bfrange that holds it (`cmap.rangeSpans`, a sweep with the `idxHeap`
    min-heap). `cmap.buildIndex` (new) builds the codespace and bfrange
    parts once; every entry was charged when stored, the sorts are charged
    about n log n before they run, and the sweep charges every step. A `hi`
    of another length than `lo` is turned into the equal integer bound, so
    the results match upstream's string comparisons.
  - `cmap.Decode` looks codes up through the index (`cmapIndex.inCodespace`,
    `cmap.mapCode`, `cmapIndex.bfrangeFor`; all new) instead of scanning
    every range for every code, and charges the comparisons it actually
    makes: one per bitmap probe, about log2 n per binary search.
    `cmap.mapCode` also charges the bytes of a replacement before decoding
    it, since a shared dictionary value can make one long. A code that
    matches nothing is therefore cheap, and a large table no longer
    multiplies the cost of every glyph. Upstream's results and their order of
    precedence (codespace, then bfchar, then bfrange) are unchanged.
  - `cmap.Decode` collects its output in `cmapDecode` (new), which charges
    work for every rune. `cmapDecode.emit` checks the exact UTF-8 size of
    each piece against the text budget left, keeps the prefix that fits, and
    records a stop with `Reader.noteTextStop` instead of discarding the
    string.
- `Page.GetPlainText`'s `showEncodedText` closure: charges the bytes of the
  string before decoding it (every encoder reads them all, and a shared
  string can be shown repeatedly); writes the decoded prefix, charging the
  exact UTF-8 size of each rune, then ends the page with `ErrTextLimit` when
  the decode stopped, so the stop is never lost.
- `Page.GetPlainText`'s `TJ` case charges one unit per array element,
  numbers included, before visiting it, so a long array shown repeatedly
  pays for every visit and samples the clock.
- `Page.GetPlainText`'s `"` and `'` cases (fix round 8, gate-8 finding
  G8-C1): upstream's `"` case accepted its three operands and then fell
  through into the `'` case, which requires exactly one, so every valid
  `"` (aw ac string, PDF 1.7 §9.4.3) ended the page as malformed and its
  text was lost. `"` now requires three operands of which the first two are
  numbers, and shows its string. Both `"` and `'` now first write a line
  break, as each is defined as `T*` followed by showing the string;
  upstream's `'` showed the string with no line break. The line break is a
  plain newline charged to the text budget, as `BT`'s is (`T*` itself still
  passes its newline through the font's encoder, as upstream did); the
  string goes through `showEncodedText`, so it is charged as every other
  shown string is.
- `isNumber` (new): reports a PDF integer or real.
- `readCmap`:
  - sets the owner;
  - checks the operand counts of `endcodespacerange`, `endbfchar` and
    `endbfrange` by division against the stack depth (a forged count cannot
    overflow `n*2` or `n*3`), charges the count, and pops with
    `Stack.popRequired`;
  - ends the parse with an `ErrStructureLimit` error when a count cannot be
    honoured, instead of returning no cmap: the font would otherwise fall
    back to showing its raw character codes as text. A codespace range whose
    two ends differ in length still returns no cmap, as upstream did;
  - charges one unit per bfchar pair and bfrange triple read, and admits
    (`cmap.admitEntry`) each entry it keeps before storing it. A bfchar or
    bfrange whose source code is not 1 to 4 bytes is skipped without being
    hashed, as upstream's scan never matched it; a bfchar code already in
    the map is a duplicate, costs one lookup of at most four bytes and keeps
    nothing. A long source string shared through a dictionary name can
    therefore no longer be hashed once per use, and duplicates no longer
    reserve index storage;
  - charges `Reader.chargeReuse` for the value `defineresource` pushes back;
  - builds the lookup index;
  - debug prints removed.
- `dictEncoder`: decodes through a 256-entry table built once by
  `newDictEncoder` (new) from the `/Differences` array, charging one unit
  per array element and the bytes of each glyph name it hashes. Byte c
  becomes the first name coded c that `nameToRune` knows, else `rune(c)`,
  as upstream's scan chose; upstream re-scanned the array and re-hashed its
  names for every decoded byte, so one long name was hashed once per glyph.
- Removed:
  - the layout and outline APIs the engine does not use: `Reader.Page`, `Reader.NumPage`, `Reader.GetStyledTexts`, `Reader.Outline` and `buildOutline`;
  - the Page methods `MediaBox`, `CropBox`, `Content`, `walkTextBlocks`, `GetTextByRow` and `GetTextByColumn`;
  - the types `Text`, `TextHorizontal`, `TextVertical`, `Rect`, `Point`, `Content`, `Outline`, `Row`, `Rows`, `Column`, `Columns`, `gstate` and `matrix`, plus `ident` and `matrix.mul` (receiver `x`).

### Caller in `pkg/docextract` (outside this package)

`extract.go::extractPDF` treats `ErrTextLimit` from `GetPlainText` as a
truncation: the kept text is marked with the truncation marker even when it is
far below the rune cap, and a stop before any text is reported as exceeding the
safety limits, not as a document without text.

### `text.go`

- Removed `IsSameSentence`, which depended on the removed `Text` type, together with its `math` import.

### `ascii85.go`

- Removed an empty `if err == io.EOF {}` block (lint only).

### `name.go` and `name_more.go`

The glyph-name table (`nameToRune`) was split in two to keep each file under
the repository's file-size budget:

- `nameTableA` in `name.go` holds the first 2,140 entries;
- `nameTableB` in `name_more.go` holds the remaining 2,140;
- `nameToRune` is built from both by `mergeNameTables` (new).

The split is content-neutral, checked by script. In order, the 4,280
`(name, rune)` entries of the original literal at `a3a977ba78` equal
`nameTableA`'s entries followed by `nameTableB`'s. No other lines sit inside
the literals, and no name appears in both tables. The only other differences
are the file header comment, `mergeNameTables`, and gofmt's column
re-alignment of the entries.
