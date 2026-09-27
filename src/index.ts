// @omnipus/ui — app-independent Sovereign Deep foundations and controls.
// Domain widgets, stores, and application shells are intentionally private.
import './styles/library.css'
export { tokens, resolvedTokens } from './design-system/tokens'
export type { TokenId } from './design-system/tokens'
export { statusContract } from './design-system/status'
export type { StatusPresentation } from './design-system/status'
export { cn } from './lib/utils'

export { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from './components/ui/accordion'
export { Avatar, AvatarImage, AvatarFallback } from './components/ui/avatar'
export { Badge, badgeVariants } from './components/ui/badge'
export type { BadgeProps } from './components/ui/badge'
export { Button, buttonVariants } from './components/ui/button'
export type { ButtonProps } from './components/ui/button'
export { IconButton } from './components/ui/icon-button'
export type { IconButtonProps } from './components/ui/icon-button'
export { Calendar } from './components/ui/calendar'
export type { CalendarProps } from './components/ui/calendar'
export { Card, CardHeader, CardFooter, CardTitle, CardDescription, CardContent, cardVariants } from './components/ui/card'
export type { CardProps } from './components/ui/card'
export { Checkbox } from './components/ui/checkbox'
export { Command, CommandInput, CommandList, CommandEmpty, CommandGroup, CommandItem, CommandSeparator } from './components/ui/command'
export { ConfirmDialog } from './components/ui/confirm-dialog'
export type { ConfirmDialogProps } from './components/ui/confirm-dialog'
export { DatePicker } from './components/ui/date-picker'
export type { DatePickerProps } from './components/ui/date-picker'
export { DateTimePicker } from './components/ui/date-time-picker'
export type { DateTimePickerProps } from './components/ui/date-time-picker'
export { Dialog, DialogPortal, DialogOverlay, DialogTrigger, DialogClose, DialogContent, DialogHeader, DialogFooter, DialogTitle, DialogDescription } from './components/ui/dialog'
export { DisclosureRow } from './components/ui/disclosure-row'
export type { DisclosureRowProps } from './components/ui/disclosure-row'
export { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuCheckboxItem, DropdownMenuRadioItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuShortcut, DropdownMenuGroup, DropdownMenuPortal, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuRadioGroup } from './components/ui/dropdown-menu'
export { Input } from './components/ui/input'
export { Label } from './components/ui/label'
export { Popover, PopoverTrigger, PopoverContent, PopoverAnchor } from './components/ui/popover'
export { Progress } from './components/ui/progress'
export { RadioGroup, RadioGroupItem } from './components/ui/radio-group'
export type { RadioGroupProps, RadioGroupItemProps } from './components/ui/radio-group'
export { SegmentedControl, SegmentedControlItem } from './components/ui/segmented-control'
export type { SegmentedControlProps, SegmentedControlItemProps } from './components/ui/segmented-control'
export { Select, SelectGroup, SelectValue, SelectTrigger, SelectContent, SelectLabel, SelectItem, SelectSeparator, SelectScrollUpButton, SelectScrollDownButton } from './components/ui/select'
export { Separator } from './components/ui/separator'
export { Sheet, SheetPortal, SheetOverlay, SheetTrigger, SheetClose, SheetContent, SheetHeader, SheetFooter, SheetTitle, SheetDescription } from './components/ui/sheet'
export { Slider } from './components/ui/slider'
export type { SliderProps } from './components/ui/slider'
export { SmartSelect } from './components/ui/smart-select'
export { Switch } from './components/ui/switch'
export { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from './components/ui/table'
export { Tabs, TabsList, TabsTrigger, TabsContent } from './components/ui/tabs'
export { Textarea } from './components/ui/textarea'
export { Tooltip } from './components/ui/tooltip'
export type { TooltipProps } from './components/ui/tooltip'
export { Field } from './components/ui/field'
export type { FieldProps, FieldControlProps } from './components/ui/field'
export { Skeleton } from './components/ui/skeleton'
export type { SkeletonProps } from './components/ui/skeleton'
export { CollectionState } from './components/ui/collection-state'
export type { CollectionStatus, CollectionStateProps } from './components/ui/collection-state'
export { JobStatus } from './components/ui/job-status'
export type { JobState, JobStatusProps } from './components/ui/job-status'
export { EmptyState } from './components/ui/empty-state'
export type { EmptyStateProps } from './components/ui/empty-state'
export { ErrorState } from './components/ui/error-state'
export type { ErrorStateProps } from './components/ui/error-state'
export { QueryErrorState } from './components/ui/query-error-state'
export type { QueryErrorStateProps } from './components/ui/query-error-state'
export type { AvatarImageProps } from './components/ui/avatar'
export type { TableProps } from './components/ui/table'
export type { ProgressProps } from './components/ui/progress'
export { useLoadingVisibility } from './design-system/use-loading-visibility'
export {
  ZoomPill,
  ZoomableMediaSurface,
  clampZoomScale,
  effectiveMinScale,
  computeFittedScale,
  computeOpeningScale,
  resolveSvgIntrinsicSize,
  useZoomableViewKeyboard,
  useZoomableMedia,
  ZOOMABLE_VIEW_MIN_SCALE,
  ZOOMABLE_VIEW_MAX_SCALE,
  ZOOMABLE_VIEW_LABEL_FLOOR_PX,
} from './components/ui/zoomable-view'
export type {
  ZoomPillProps,
  ZoomableSize,
  OpeningScaleParams,
  ZoomableViewKeyboardHandlers,
  ZoomableMediaController,
  UseZoomableMediaOptions,
  ZoomableMediaSurfaceProps,
} from './components/ui/zoomable-view'
