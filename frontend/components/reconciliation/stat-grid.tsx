import type { ReactNode } from "react"
import { Card } from "@/components/ui/card"
import { cn } from "@/lib/utils"

export interface StatItem {
  label: string
  value: ReactNode
  hint?: ReactNode
  tone?: "default" | "positive" | "negative" | "muted"
}

const toneClass: Record<NonNullable<StatItem["tone"]>, string> = {
  default: "text-foreground",
  positive: "text-emerald-600 dark:text-emerald-400",
  negative: "text-rose-600 dark:text-rose-400",
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
        <Card key={item.label} className="gap-1 p-3">
          <div className="text-xs text-muted-foreground">{item.label}</div>
          <div className={cn("text-lg font-semibold tabular-nums", toneClass[item.tone ?? "default"])}>
            {item.value}
          </div>
          {item.hint ? <div className="text-xs text-muted-foreground">{item.hint}</div> : null}
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
