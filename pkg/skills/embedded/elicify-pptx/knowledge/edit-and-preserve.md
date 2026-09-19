# Existing decks, templates, and preservation

## Inventory the deck

Keep the original. Record slide order, dimensions, master/layout names and
relationships, placeholders, shape IDs, notes, charts and embedded workbooks,
media, links, and special features. A before/after render is necessary when
layout or branding matters. Also compare untouched package parts: visually
identical slides can still lose editability or speaker notes.

Use `Presentation(source_path)` and modify the smallest necessary object.
For a risky source, save a no-op copy first and compare it before making edits.
Distinguish irrelevant ZIP serialization differences from changes to XML,
relationships, or binary assets. Do not promise preservation of an unsupported
feature based only on a no-op save succeeding.

## Templates and masters

A supplied template owns typography, dimensions, theme colors, and branding.
Inspect available layouts and placeholder indices; never assume index 1 is a
body placeholder or layout 6 is blank in an arbitrary template. This listing
helps select the actual layout:

```python
from pptx import Presentation
prs = Presentation(source_path)
for index, layout in enumerate(prs.slide_layouts):
    print(index, layout.name,
          [(p.placeholder_format.idx, p.name) for p in layout.placeholders])
```

Create slides from the correct existing layout, then fill its placeholders.
Inherited master artwork need not appear in the slide's own shape collection.
Avoid changing a master to fix one slide: that can alter the entire deck.
Arbitrary master editing and cloning slides across decks do not have a complete
high-level python-pptx workflow. Do not improvise shallow XML copying without
correctly remapping relationships to images, charts, notes, layouts, and media.
Use a template's approved layouts or an application-native operation instead.

A `.potx` uses a template content type; use an approved office application to
create a `.pptx` from it when the library rejects it. Extension renaming does
not convert the package. For `.pptm`, legacy `.ppt`, encryption, embedded
objects, or unsupported features, preserve the original and use a capable
approved application or explicitly narrow the deliverable.

## Focused edits

When replacing text, inspect run boundaries, hyperlinks, and inherited styles.
Change plain runs where possible. Replacing a whole text frame can discard
bullets, mixed formatting, and links. For placeholders use their supported APIs,
and inspect whether replacement changes geometry or inheritance.

Native supported chart data can be updated using `chart.replace_data(data)`.
Check series/category ordering and embedded workbook values after save. Do not
recreate a complex existing combo chart as a simpler type without disclosing
that tradeoff. Chart images cannot be edited as native chart data; either retain
the image or reconstruct a chart from reliable source data and identify the
replacement.

Existing notes may be rich text. Read `has_notes_slide` before inspecting notes
so an inspection does not create a notes slide unintentionally. Accessing
`notes_slide` can create one; replacing `notes_text_frame.text` replaces notes
content and its local formatting. Preserve existing narration unless asked to
change it, and append deliberately rather than overwriting it wholesale.

## Compatibility checklist

Check preservation of every requested feature, especially:

- slide order, aspect ratio, masters/layouts, inherited artwork, and theme fonts;
- hyperlinks, comments, speaker notes, and author metadata;
- chart values and embedded workbooks; editable text/tables/shapes;
- images, cropping, videos/audio, animations, transitions, and SmartArt.

The last group often requires PowerPoint-native verification. Inspecting package
parts can detect losses but cannot prove playback or animation behavior. Say
which application checks were performed and which remain unverified. Never
quietly flatten a deck to images to meet a visual target when editability was
requested.

Sources: [python-pptx capability overview](https://python-pptx.readthedocs.io/en/latest/),
[placeholders](https://python-pptx.readthedocs.io/en/latest/user/placeholders-using.html),
[notes behavior](https://python-pptx.readthedocs.io/en/latest/user/notes.html).
