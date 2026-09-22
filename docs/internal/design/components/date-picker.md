# DatePicker

DatePicker is a controlled date-only composite. Its named button opens Calendar, preserves the caller value, returns a selected local date or null, closes after selection and restores trigger focus. Disabled triggers are removed from the tab order and reject activation. Read-only triggers remain focusable for inspection, expose `aria-disabled`, and do not open. If either state becomes active while the popover is open, it closes immediately and stale selection handlers reject changes. A non-null value must be a valid `Date`; invalid dates throw `RangeError` rather than entering corrupted calendar state. The trigger keeps its existing subtle shadow and shared Button focus treatment; consolidation does not change its geometry.

The popover is bounded to the viewport. At narrow coarse-pointer widths, Calendar owns any necessary horizontal scrolling inside its date grid rather than allowing the popover or page to overflow.

Field control metadata (`id`, `aria-describedby`, and `aria-invalid`) is forwarded to the trigger. Because `aria-required` is not valid on a button, a required picker merges a visually hidden “Required” description into `aria-describedby`. Read-only remains focusable and exposes `aria-disabled`, but a local read-only override keeps its text fully opaque so it is visually distinct from native disabled state.
