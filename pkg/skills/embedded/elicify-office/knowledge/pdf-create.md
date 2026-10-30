# Create a PDF

## Create a paginated report

ReportLab's flow-based layout avoids manual line-position arithmetic. Use
Paragraph, Spacer and Table inside a document template. Escape untrusted text
before handing it to Paragraph: its markup parser is not a plain-text sink.

```python
from html import escape
from reportlab.lib import colors
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import getSampleStyleSheet
from reportlab.platypus import SimpleDocTemplate, Paragraph, Spacer, Table, TableStyle

styles = getSampleStyleSheet()
doc = SimpleDocTemplate(output_path, pagesize=A4, rightMargin=42, leftMargin=42,
                        topMargin=42, bottomMargin=42)
story = [Paragraph(escape(report_title), styles['Title']), Spacer(1, 14)]
story.append(Paragraph(escape(summary), styles['BodyText']))
rows = [['Measure', 'Value'], ['Completed requests', '42']]
table = Table(rows, colWidths=[330, 120], repeatRows=1)
table.setStyle(TableStyle([
    ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#204C60')),
    ('TEXTCOLOR', (0, 0), (-1, 0), colors.white),
    ('VALIGN', (0, 0), (-1, -1), 'TOP'),
    ('BOTTOMPADDING', (0, 0), (-1, -1), 8),
]))
story.extend([Spacer(1, 14), table])
doc.build(story)
```

Replace example values with sourced content. Wrap long table strings in
Paragraph objects, keep header repetition enabled and avoid one row taller
than the available page. Add page numbering via template callbacks. Register
and embed a suitable font for Unicode text; basic built-in fonts do not cover
every script. Complex scripts may require shaping support or a different
renderer. Preview the pages ([verify](verify.md)) and look at the glyphs rather than trusting text extraction.


## References

Original workflows based on public APIs. Checked 2026-09-17:
[ReportLab platypus](https://docs.reportlab.com/reportlab/userguide/ch5_platypus/).
