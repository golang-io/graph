import {
  forwardRef,
  useEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type ComponentProps,
  type HTMLAttributes,
  type ReactNode,
} from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { cn } from './cn'

type ButtonVariant = 'default' | 'destructive' | 'outline' | 'secondary' | 'ghost' | 'link'
type ButtonSize = 'default' | 'sm' | 'lg' | 'icon'

const buttonVariantClass: Record<ButtonVariant, string> = {
  default: 'bg-primary text-primary-foreground hover:bg-primary/90',
  destructive: 'bg-destructive text-destructive-foreground hover:opacity-90',
  outline: 'border border-border bg-transparent hover:bg-primary/8',
  secondary: 'bg-secondary text-foreground hover:bg-primary/8',
  ghost: 'hover:bg-primary/8',
  link: 'text-primary underline-offset-4 hover:underline',
}

const buttonSizeClass: Record<ButtonSize, string> = {
  default: 'h-[var(--control-height)] px-3.5 py-0',
  sm: 'h-[var(--control-height-sm)] px-2.5 text-xs',
  lg: 'h-[var(--control-height-lg)] px-[18px]',
  icon: 'h-[var(--control-height)] w-[var(--control-height)]',
}

export function Button({
  variant = 'default',
  size = 'default',
  className,
  type = 'button',
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant; size?: ButtonSize }) {
  return (
    <button
      type={type}
      className={cn(
        'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-ui text-sm font-semibold transition-colors disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0',
        buttonVariantClass[variant],
        buttonSizeClass[size],
        className,
      )}
      {...props}
    />
  )
}

export const Input = forwardRef<HTMLInputElement, ComponentProps<'input'>>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        'flex h-[var(--control-height)] w-full rounded-ui border border-input bg-background px-3 py-1 text-sm text-foreground',
        className,
      )}
      {...props}
    />
  ),
)
Input.displayName = 'Input'

export const Textarea = forwardRef<HTMLTextAreaElement, ComponentProps<'textarea'>>(
  ({ className, ...props }, ref) => (
    <textarea
      ref={ref}
      className={cn(
        'flex min-h-16 w-full rounded-ui border border-input bg-background px-3 py-2 text-sm text-foreground',
        className,
      )}
      {...props}
    />
  ),
)
Textarea.displayName = 'Textarea'

export function ScrollArea({
  className,
  children,
  viewportProps,
}: {
  className?: string
  children?: ReactNode
  viewportProps?: HTMLAttributes<HTMLDivElement>
}) {
  return (
    <div className={cn('overflow-auto', className)} {...viewportProps}>
      {children}
    </div>
  )
}

export type SelectOption = {
  value: string
  label: ReactNode
  disabled?: boolean
}

export type SelectProps = {
  value: string
  onChange: (value: string) => void
  options: readonly SelectOption[]
  placeholder?: string
  disabled?: boolean
  className?: string
  buttonClassName?: string
  contentClassName?: string
  'aria-label'?: string
}

export function Select({
  value,
  onChange,
  options,
  placeholder,
  disabled = false,
  className,
  buttonClassName,
  contentClassName,
  'aria-label': ariaLabel,
}: SelectProps) {
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(-1)
  const containerRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)

  const selectedIndex = options.findIndex((o) => o.value === value)
  const selected = options[selectedIndex]

  useEffect(() => {
    if (!open) return
    setActiveIndex(selectedIndex >= 0 ? selectedIndex : 0)
    const onDown = (e: MouseEvent) => {
      if (!containerRef.current?.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    window.addEventListener('mousedown', onDown)
    return () => {
      window.removeEventListener('mousedown', onDown)
    }
  }, [open, selectedIndex])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (disabled) return

    if (!open) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        setOpen(true)
      }
      return
    }

    if (e.key === 'Escape' || e.key === 'Tab') {
      setOpen(false)
      if (e.key === 'Escape') {
        e.preventDefault()
        triggerRef.current?.focus()
      }
      return
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIndex((prev) => (prev + 1 < options.length ? prev + 1 : 0))
      return
    }

    if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIndex((prev) => (prev - 1 >= 0 ? prev - 1 : options.length - 1))
      return
    }

    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      const opt = options[activeIndex]
      if (opt && !opt.disabled) {
        onChange(opt.value)
        setOpen(false)
        triggerRef.current?.focus()
      }
    }
  }

  return (
    <div ref={containerRef} className={cn('relative inline-block text-left', className)}>
      <button
        ref={triggerRef}
        type="button"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={ariaLabel}
        onClick={() => !disabled && setOpen((prev) => !prev)}
        onKeyDown={handleKeyDown}
        className={cn(
          'inline-flex h-8 w-full items-center justify-between gap-1.5 rounded-ui border border-input bg-background px-2.5 py-1 text-xs text-foreground transition-colors hover:bg-primary/8 focus-visible:border-primary focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50',
          buttonClassName,
        )}
      >
        <span className="truncate">{selected ? selected.label : (placeholder ?? '')}</span>
        <ChevronDown
          className={cn(
            'h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform duration-200',
            open && 'rotate-180',
          )}
        />
      </button>

      {open ? (
        <div
          role="listbox"
          tabIndex={-1}
          className={cn(
            'absolute left-0 top-full z-50 mt-1 min-w-full w-max overflow-hidden rounded-ui border border-border bg-card p-1 text-foreground shadow-md animate-in fade-in-0 zoom-in-95',
            contentClassName,
          )}
        >
          {options.map((option, idx) => {
            const isSelected = option.value === value
            const isHighlighted = idx === activeIndex
            return (
              <button
                key={option.value}
                type="button"
                role="option"
                aria-selected={isSelected}
                disabled={option.disabled}
                onMouseEnter={() => setActiveIndex(idx)}
                onClick={() => {
                  onChange(option.value)
                  setOpen(false)
                  triggerRef.current?.focus()
                }}
                className={cn(
                  'relative flex w-full cursor-pointer select-none items-center rounded-sm py-1.5 pl-7 pr-2 text-xs outline-none transition-colors text-foreground disabled:pointer-events-none disabled:opacity-50',
                  isHighlighted ? 'bg-primary/8' : 'hover:bg-primary/8',
                  isSelected && 'font-medium',
                )}
              >
                {isSelected ? (
                  <span className="absolute left-1.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center">
                    <Check className="h-3.5 w-3.5 text-primary" />
                  </span>
                ) : null}
                <span className="truncate">{option.label}</span>
              </button>
            )
          })}
        </div>
      ) : null}
    </div>
  )
}

