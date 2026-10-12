import * as SelectPrimitive from "@radix-ui/react-select"
import { Check, ChevronDown, ChevronUp } from "lucide-react"
import { cn } from "@/lib/utils"

export type SelectOption = { value: string; label: string; disabled?: boolean }

type SelectProps = {
  value: string
  onValueChange: (value: string) => void
  options: SelectOption[]
  "aria-label": string
  placeholder?: string
  disabled?: boolean
  required?: boolean
  className?: string
}

export function Select({ value, onValueChange, options, placeholder = "请选择", disabled, required, className, "aria-label": label }: SelectProps) {
  // Prefix values so an empty "all" option is distinct from Radix's placeholder.
  const selected = options.some(option => option.value === value) ? `option:${value}` : ""
  return <div className="select-control"><SelectPrimitive.Root value={selected} onValueChange={next => onValueChange(next.slice(7))} disabled={disabled} required={required}>
    <SelectPrimitive.Trigger aria-label={label} className={cn("select-trigger", className)}>
      <SelectPrimitive.Value placeholder={placeholder} />
      <SelectPrimitive.Icon asChild><ChevronDown size={14} /></SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content className="select-popover" position="popper" sideOffset={6} collisionPadding={12}>
        <SelectPrimitive.ScrollUpButton className="select-scroll"><ChevronUp size={14} /></SelectPrimitive.ScrollUpButton>
        <SelectPrimitive.Viewport className="select-viewport">
          {options.map(option => <SelectPrimitive.Item key={option.value} value={`option:${option.value}`} disabled={option.disabled} className="select-option">
            <SelectPrimitive.ItemText>{option.label}</SelectPrimitive.ItemText>
            <SelectPrimitive.ItemIndicator><Check size={14} /></SelectPrimitive.ItemIndicator>
          </SelectPrimitive.Item>)}
        </SelectPrimitive.Viewport>
        <SelectPrimitive.ScrollDownButton className="select-scroll"><ChevronDown size={14} /></SelectPrimitive.ScrollDownButton>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  </SelectPrimitive.Root></div>
}
