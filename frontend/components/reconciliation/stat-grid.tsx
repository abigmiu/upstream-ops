import type { ComponentType, ReactNode } from "react"
import { Card } from "@/components/ui/card"
import { cn } from "@/lib/utils"

export interface StatItem {
  label: string
  value: ReactNode
  hint?: ReactNode
  tone?: "default" | "positive" | "negative" | "muted"
  icon?: ComponentType<{ className?: string }>
  /** 图标底色，如 "bg-brand/10 text-brand"。缺省用中性底。 */
  iconTone?: string
}

const toneClass: Record<NonNullable<StatItem["tone"]>, string> = {
  default: "text-foreground",
  positive: "text-success",
  negative: "text-danger",
  muted: "text-muted-foreground",
}

export function StatGrid({ items, columns = 4 }: { items: StatItem[]; columns?: 2 | 3 | 4 }) {
  const cols =
    columns === 2
      ? "sm:grid-cols-2"
      : columns === 3
        ? "sm:grid-cols-2 lg:grid-cols-3"
        : "sm:grid-cols-2 lg:grid-cols-4"
  return (
    <div className={cn("grid grid-cols-1 gap-3", cols)}>
      {items.map((item) => (
        <Card
          key={item.label}
          className="flex flex-row items-start justify-between gap-2 border border-border p-3 shadow-none sm:p-4"
        >
          <div className="flex min-w-0 flex-col">
            <span className="text-xs text-muted-foreground">{item.label}</span>
            <p className={cn("mt-1 text-xl font-bold tracking-tight tabular-nums sm:text-2xl", toneClass[item.tone ?? "default"])}>
              {item.value}
            </p>
            {item.hint ? (
              <p className="mt-1 min-w-0 truncate text-xs text-muted-foreground">{item.hint}</p>
            ) : null}
          </div>
          {item.icon ? (
            <span
              className={cn(
                "flex size-9 shrink-0 items-center justify-center rounded-xl sm:size-10",
                item.iconTone ?? "bg-muted text-muted-foreground",
              )}
            >
              <item.icon className="size-5" />
            </span>
          ) : null}
        </Card>
      ))}
    </div>
  )
}

/** 把可能为负的金额染成红/绿两色。 */
export function signedTone(value: number): StatItem["tone"] {
  if (!Number.isFinite(value) || value === 0) return "muted"
  return value > 0 ? "positive" : "negative"
}
