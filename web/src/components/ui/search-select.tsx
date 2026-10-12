import { useEffect, useId, useRef, useState } from "react"
import * as Popover from "@radix-ui/react-popover"
import { Check, ChevronDown, Search } from "lucide-react"
import { cn } from "@/lib/utils"
import type { SelectOption } from "@/components/ui/select"

type SearchSelectProps = {
  value: string
  onValueChange: (value: string) => void
  options: SelectOption[]
  "aria-label": string
  placeholder?: string
  searchPlaceholder?: string
  emptyMessage?: string
  allowCustom?: boolean
  disabled?: boolean
  className?: string
}

export function SearchSelect({ value, onValueChange, options, placeholder = "请选择", searchPlaceholder = "搜索…", emptyMessage = "没有匹配的选项", allowCustom = false, disabled, className, "aria-label": label }: SearchSelectProps) {
  const [open, setOpen] = useState(false), [query, setQuery] = useState(""), [active, setActive] = useState(0)
  const id = useId(), input = useRef<HTMLInputElement>(null), list = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null), tabTarget = useRef<HTMLElement | null>(null)
  const filtered = options.filter(option => !option.disabled && option.label.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))
  const custom = allowCustom && query.trim() && !options.some(option => option.value === query.trim())
  const choices = custom ? [...filtered, { value: query.trim(), label: `使用「${query.trim()}」` }] : filtered
  const index = Math.min(active, choices.length - 1)
  const selected = options.find(option => option.value === value)?.label || value || placeholder
  function changeOpen(next: boolean) {
    setOpen(next)
    if (next) { setQuery(""); setActive(0) }
  }
  function choose(next: string) {
    onValueChange(next)
    setOpen(false)
  }
  useEffect(() => {
    if (open) list.current?.children[index]?.scrollIntoView({ block: "nearest" })
  }, [index, open])
  return <Popover.Root open={open} onOpenChange={changeOpen}>
    <Popover.Trigger asChild>
      <button ref={trigger} type="button" role="combobox" aria-label={label} aria-expanded={open} disabled={disabled} className={cn("select-trigger", className)} title={selected}
        onKeyDown={event => { if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); changeOpen(true) } }}>
        <span>{selected}</span><ChevronDown size={14} />
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content className="search-select-popover" align="start" sideOffset={6} collisionPadding={12} aria-label={`${label}选项`}
        onOpenAutoFocus={event => { event.preventDefault(); input.current?.focus() }}
        onCloseAutoFocus={event => {
          if (tabTarget.current) { event.preventDefault(); tabTarget.current.focus(); tabTarget.current = null }
        }}>
        <div className="select-search">
          <Search size={15} aria-hidden="true" />
          <input ref={input} role="combobox" aria-label={`搜索${label}`} aria-expanded="true" aria-controls={id} aria-autocomplete="list"
            aria-activedescendant={index >= 0 ? `${id}-${index}` : undefined} autoComplete="off" spellCheck={false}
            placeholder={searchPlaceholder} value={query} onChange={event => { setQuery(event.target.value); setActive(0) }}
            onKeyDown={event => {
              if (event.nativeEvent.isComposing) return
              if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault()
                setActive(choices.length ? (index + (event.key === "ArrowDown" ? 1 : -1) + choices.length) % choices.length : 0)
              } else if (event.key === "Enter") {
                event.preventDefault()
                if (choices[index]) choose(choices[index].value)
              } else if (event.key === "Tab") {
                // A portalled search field sits after the page in DOM order.
                // Continue from its trigger instead of jumping to the page top.
                event.preventDefault()
                const scope = trigger.current?.closest('[role="dialog"]') || document
                const targets = [...scope.querySelectorAll<HTMLElement>("a[href], button, input, select, textarea, [tabindex]")]
                  .filter(node => node.tabIndex >= 0 && !node.matches(":disabled") && node.getClientRects().length && !node.closest('[aria-hidden="true"], [inert], .search-select-popover'))
                const current = targets.indexOf(trigger.current!)
                tabTarget.current = targets[current + (event.shiftKey ? -1 : 1)] || trigger.current
                setOpen(false)
              }
            }} />
        </div>
        <div id={id} ref={list} role="listbox" aria-label={label} className="search-select-list">
          {choices.map((option, i) => <button type="button" tabIndex={-1} role="option" id={`${id}-${i}`} aria-selected={value === option.value}
            className="select-option" data-highlighted={i === index ? "" : undefined} key={option.value}
            onPointerMove={() => setActive(i)} onMouseDown={event => event.preventDefault()} onClick={() => choose(option.value)}>
            <span>{option.label}</span>{value === option.value && <Check size={14} />}
          </button>)}
          {!choices.length && <p className="select-empty" role="status">{emptyMessage}</p>}
        </div>
        {allowCustom && <p className="select-hint">选择已有模型，或输入完整模型 ID 后确认</p>}
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}
