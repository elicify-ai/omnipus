import * as React from 'react'
import { cn } from '@/lib/utils'
import { Label } from './label'

export type FieldControlProps = {
  id?: string
  required?: boolean
  'aria-describedby'?: string
  'aria-invalid'?: React.AriaAttributes['aria-invalid']
}

export interface FieldProps extends Omit<React.HTMLAttributes<HTMLDivElement>, 'children'> {
  label: React.ReactNode
  description?: React.ReactNode
  error?: React.ReactNode
  required?: boolean
  optionalLabel?: React.ReactNode
  children: React.ReactElement<FieldControlProps> | ((controlProps: FieldControlProps) => React.ReactNode)
}

const Field = React.forwardRef<HTMLDivElement, FieldProps>(function Field(
  { label, description, error, required = false, optionalLabel, className, children, ...props },
  ref,
) {
  const generatedId = React.useId()
  const childProps: FieldControlProps = typeof children === 'function' ? {} : children.props
  if (typeof children !== 'function' && typeof children.type === 'string' && !['input', 'textarea', 'select', 'button'].includes(children.type)) {
    throw new Error('Field children must be a control element or render function; intrinsic wrappers cannot receive control identity.')
  }
  if (typeof children !== 'function' && children.type === React.Fragment) {
    throw new Error('Field children must be a control element or render function; fragments cannot receive control identity.')
  }
  const controlId = childProps.id ?? `${generatedId}-control`
  const descriptionId = `${generatedId}-description`
  const errorId = `${generatedId}-error`
  const describedBy = [childProps['aria-describedby'], description ? descriptionId : null, error ? errorId : null]
    .filter(Boolean)
    .join(' ') || undefined
  const resolvedRequired = required || childProps.required === true
  const controlProps: FieldControlProps = {
    id: controlId,
    required: resolvedRequired,
    'aria-invalid': error ? true : childProps['aria-invalid'],
    'aria-describedby': describedBy,
  }
  const control = typeof children === 'function' ? children(controlProps) : React.cloneElement(children, controlProps)

  return (
    // space-y: Field previously stacked label, control, description and error
    // with NO vertical rhythm at all, and Label carries `leading-none`, so the
    // label sat flush on top of its input in every form using this component.
    // One gap token here fixes every call site at once rather than each form
    // re-deriving its own margin.
    <div ref={ref} className={cn('space-y-[var(--space-2)]', className)} {...props}>
      <Label htmlFor={controlId}>
        {label}
        {resolvedRequired && <span aria-hidden="true"> *</span>}
        {!resolvedRequired && optionalLabel != null && <> <span>{optionalLabel}</span></>}
      </Label>
      {control}
      {description && <div id={descriptionId} className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{description}</div>}
      {error && <div id={errorId} role="alert" className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">{error}</div>}
    </div>
  )
})
Field.displayName = 'Field'

export { Field }
