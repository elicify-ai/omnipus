# Story, layout, and executable construction

## Story before shapes

Write each slide's main point as a sentence. A useful sequence often answers:
what changed, why it matters, what the evidence says, and what happens next.
Use the user's purpose to choose the sequence; do not force every presentation
into a sales narrative. Record source/units/date for each chart and metric.

Use a small layout vocabulary: title, key message plus evidence, comparison,
chart, and decision. Keep alignment and margins consistent. Set the aspect ratio
before placing shapes. Widescreen is a sensible new-deck default; a supplied
template's dimensions take precedence.

Prefer body text around 20–24 points for a presentation viewed at distance;
this is a starting point, not a guarantee. If a slide needs a dense paragraph,
shorten it, split the slide, or move supporting detail to notes. Use a visible
hierarchy, adequate contrast, and meaningful labels rather than decorative
shapes that compete with the argument.

## Executable starting point

Run in the approved project Python environment and pass an absolute output path.
This example produces editable text, a native chart, and speaker notes. Its data
is illustrative; replace it with verified user data before delivery.

```python
from pathlib import Path
import sys
from pptx import Presentation
from pptx.chart.data import CategoryChartData
from pptx.enum.chart import XL_CHART_TYPE
from pptx.util import Inches, Pt

out = Path(sys.argv[1])
out.parent.mkdir(parents=True, exist_ok=True)
deck = Presentation()
deck.slide_width, deck.slide_height = Inches(13.333), Inches(7.5)
slide = deck.slides.add_slide(deck.slide_layouts[6])
title = slide.shapes.add_textbox(Inches(0.7), Inches(0.4), Inches(12), Inches(0.7))
run = title.text_frame.paragraphs[0].add_run()
run.text = 'Delivery improved across three periods'
run.font.size, run.font.bold = Pt(30), True
run.font.name = 'Arial'
data = CategoryChartData()
data.categories = ['Period 1', 'Period 2', 'Period 3']
data.add_series('On-time deliveries (%)', [72, 81, 88])
chart = slide.shapes.add_chart(
    XL_CHART_TYPE.COLUMN_CLUSTERED,
    Inches(0.8), Inches(1.5), Inches(11.7), Inches(4.8), data,
).chart
chart.has_legend = False
chart.value_axis.minimum_scale, chart.value_axis.maximum_scale = 0, 100
chart.value_axis.tick_labels.number_format = '0"%"'  # Values are 72, not 0.72.
notes = slide.notes_slide.notes_text_frame
if notes is None:
    raise RuntimeError('notes placeholder is absent from this template')
notes.text = 'Illustrative data. Replace with verified source and explain the measurement.'
deck.save(out)
check = Presentation(out)
assert len(check.slides) == 1
assert list(check.slides[0].shapes[1].chart.series[0].values) == [72.0, 81.0, 88.0]
```

The checks establish the example's slide count and chart values only. They do
not check chart labels, fonts, notes layout, or visual appearance.

## Text, charts, tables, and images

Use text frames with explicit box sizes, margins, and wrapping. Paragraphs and
runs control local formatting; broad `.text` replacement discards fine-grained
formatting. `fit_text()` may be useful when suitable font files are available,
but its result still needs rendering. If a font is missing, request it or choose
an approved installed substitute and rerender; do not assume identical metrics.

For supported charts use `CategoryChartData`, `XyChartData`, or `BubbleChartData`
as appropriate. Native charts preserve data editability; screenshots do not.
Inspect axis range, unit labels, series mapping, legend order, missing values,
and data labels. A bar/column chart generally needs a zero baseline unless an
explicitly explained exception is justified. Do not confuse a percentage value
of 88 with a fractional value of 0.88 and apply the wrong format.

For tables use real cells; keep a clear header and sufficient row height.
A dense spreadsheet screenshot is rarely readable on a presentation slide.
For pictures preserve aspect ratio. Crop deliberately when filling a frame;
check faces, labels, and meaningful content after cropping. Set useful alternative
text with supported tooling and verify reading order/accessibility in the target
application if required. A successful save is not accessibility certification.

Notes carry explanations, references, and delivery cues. Keep slide text concise
and check notes for accidental internal instructions or sensitive draft material.

## Official references

Checked 2026-09-17: [text](https://python-pptx.readthedocs.io/en/latest/user/text.html),
[charts](https://python-pptx.readthedocs.io/en/latest/user/charts.html),
[notes](https://python-pptx.readthedocs.io/en/latest/user/notes.html), and
[presentation API](https://python-pptx.readthedocs.io/en/latest/api/presentation.html).
