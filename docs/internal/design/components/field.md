# Field

Field is the structural form composite for associating one caller-owned control with its label, description, required state and validation error.

It preserves an explicit child `id`, `name`, value mode, read-only state and external `aria-describedby` references. When the caller omits an ID, Field creates a stable React ID. Field never owns or mirrors the control value and adds no spacing or layout policy; callers choose layout through `className`.

Errors set `aria-invalid`, join the error ID into `aria-describedby`, and render as a live alert. Required state is additive: either `Field required` or a required child makes the control programmatically required and shows the decorative asterisk, so the label and control cannot disagree. The public theme is dark.

A Field error takes precedence over a child's explicit `aria-invalid` value. Once that error is cleared, the child's own validation state applies again.

## Compound controls

Use the render function for controls whose root manages form state while a separate trigger receives identity and accessibility properties. Do not pass `Select` directly as the cloned child: its root does not render the labelled trigger. Route `required` to the Select root, and the remaining Field properties to `SelectTrigger`; a button trigger must not receive the native `required` attribute.

```tsx
<Field label="Team" description="Choose your team" error={teamError} required>
  {({ required, ...triggerProps }) => (
    <Select name="team" required={required} value={team} onValueChange={setTeam}>
      <SelectTrigger {...triggerProps}>
        <SelectValue placeholder="Choose" />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="design">Design</SelectItem>
      </SelectContent>
    </Select>
  )}
</Field>
```

`Field`'s own `id` identifies its wrapper. A cloned child's explicit `id` identifies the control; render-function callers should pass the supplied identity to the focusable control.

## Optional indication

Pass `optionalLabel="(optional)"`, or the equivalent localized copy, to identify an optional control. Field renders that copy inline in the label and includes it in the accessible name. It supplies no default optional copy, preserving existing labels and form geometry. If either Field or its child is required, the required marker takes precedence and optional copy is hidden. Optional copy never changes the control's required state.
