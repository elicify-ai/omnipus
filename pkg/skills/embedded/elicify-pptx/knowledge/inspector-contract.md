# Inspector contract and test plan

`inspect_pptx(path)` uses python-pptx to report `slide_count`, `width_emu`,
`height_emu`, and `out_of_bounds`: ordered `{slide, shape_id}` objects for top-level
shapes whose unrotated bounding boxes extend past the slide rectangle. Slide
numbers start at one. Touching an edge is allowed; one EMU (the Office integer
length unit) beyond any edge is reported. A shape is reported once even when
multiple edges exceed bounds. Input bytes must remain unchanged.

This is a geometry preflight, not a visual validator: rotation, child transforms
inside groups, intentional bleed, inherited master/layout content, text overflow,
contrast, and overlaps need rendered inspection. File-not-found errors stay
`FileNotFoundError`. Non-ZIP data raises `ValueError('not a ZIP-based presentation')`;
a ZIP missing `ppt/presentation.xml` raises
`ValueError('missing ppt/presentation.xml')`. Other loading errors propagate.

Tests use real generated decks with fixed 1,000,000 × 1,000,000 EMU dimensions; independent
geometry expectations follow x >= 0, y >= 0, x + width <= 1000000,
y + height <= 1000000. Test all four edges at -1/0/+1 relative to the boundary,
zero slides, missing file, malformed ZIP, wrong ZIP; assert non-mutation.
No mocks. Mutations make edge comparison inclusive, disable negative-x check,
and suppress negative-y check. Gaps: actual rendered pixels and all visual
limitations above. Two bad-format tests plus missing-file test are negative.
