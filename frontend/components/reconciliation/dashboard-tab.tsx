import { useMemo, useState } from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Activity,
  ArrowDownToLine,
  Banknote,
  Calculator,
  Coins,
  Receipt,
  TrendingUp,
  Users,
  Wallet,
} from "lucide-react"
import { Skeleton } from "@/components/ui/skeleton"
import { Empty, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { money, dateTime } from "@/lib/format"
import { useReconDashboard } from "@/lib/queries"
import { StatGrid, signedTone, type StatItem } from "./stat-grid"

/** 今天（上海时区）的 YYYY-MM-DD。 */
function todayStr(offsetDays = 0) {
  const d = new Date(Date.now() + offsetDays * 86_400_000)
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(d)
  return parts
}

export function DashboardTab() {
  const [start, setStart] = useState(() => todayStr(-7))
  const [end, setEnd] = useState(() => todayStr())
  const [range, setRange] = useState<{ start?: string; end?: string }>({
    start: todayStr(-7),
    end: todayStr(),
  })

  const query = useReconDashboard(range.start, range.end)
  const data = query.data

  const stats = useMemo<StatItem[]>(() => {
    if (!data) return []
    const s = data.summary
    const ub = data.userBalance
    return [
      { label: "站内充值（现金）", value: money(s.total_topup), hint: `自助 ${money(s.total_self_topup)} · 外部 ${money(s.total_external_topup)}`, icon: Wallet, iconTone: "bg-brand/10 text-brand" },
      { label: "提现", value: money(s.total_withdrawal), icon: Banknote, iconTone: "bg-brand/10 text-brand" },
      { label: "站内归属消费", value: money(s.total_usage), icon: Activity, iconTone: "bg-warning/10 text-warning" },
      { label: "运营支出", value: money(s.total_operating_expense), icon: Receipt, iconTone: "bg-muted text-muted-foreground" },
      { label: "上游余额合计", value: money(s.total_vendor_balance), icon: Coins, iconTone: "bg-brand/10 text-brand" },
      { label: "上游已用合计", value: money(s.total_vendor_used), icon: TrendingUp, iconTone: "bg-warning/10 text-warning" },
      { label: "上游实际净充值", value: money(s.total_actual_recharge_net), icon: ArrowDownToLine, iconTone: "bg-brand/10 text-brand" },
      { label: "上游实际利润", value: money(s.total_actual_profit), tone: signedTone(s.total_actual_profit), icon: TrendingUp, iconTone: "bg-success/10 text-success" },
      { label: "用户余额合计", value: money(s.total_user_remaining), hint: `自助 ${money(s.total_self_remaining)} · 外部 ${money(s.total_external_remaining)}`, icon: Users, iconTone: "bg-brand/10 text-brand" },
      { label: "用户已用合计", value: money(s.total_user_used), hint: `自助 ${money(s.total_self_used)} · 外部 ${money(s.total_external_used)}`, icon: Users, iconTone: "bg-warning/10 text-warning" },
      { label: "预计已实现利润", value: money(s.estimated_realized_profit), tone: signedTone(s.estimated_realized_profit), hint: "提现 − 净充值 − 运营支出", icon: Calculator, iconTone: "bg-success/10 text-success" },
      { label: "预计结算利润", value: money(s.estimated_settlement_profit), tone: signedTone(s.estimated_settlement_profit), hint: `再扣外部未消费余额 ${money(ub.externalRemaining)}`, icon: Calculator, iconTone: "bg-success/10 text-success" },
    ]
  }, [data])

  return (
    <div className="space-y-4">
      <Card className="border border-border p-3 shadow-none">
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-muted-foreground">开始日期</label>
            <Input type="date" value={start} onChange={(e) => setStart(e.target.value)} className="h-8 w-36" />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-muted-foreground">结束日期</label>
            <Input type="date" value={end} onChange={(e) => setEnd(e.target.value)} className="h-8 w-36" />
          </div>
          <Button size="sm" variant="outline" className="gap-1.5 text-xs" onClick={() => setRange({ start, end })}>
            {"应用"}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              const s = todayStr(-7)
              const e = todayStr()
              setStart(s)
              setEnd(e)
              setRange({ start: s, end: e })
            }}
          >
            {"近 7 天"}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setRange({})
              setStart("")
              setEnd("")
            }}
          >
            {"全量（含最新快照）"}
          </Button>
          {range.start || range.end ? (
            <span className="text-xs text-muted-foreground">
              {"区间："}
              {range.start || "—"} {" → "} {range.end || "—"}
            </span>
          ) : (
            <span className="text-xs text-muted-foreground">{"区间：全量"}</span>
          )}
        </div>
      </Card>

      {query.loading && !data ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-20 w-full" />
          ))}
        </div>
      ) : data ? (
        <StatGrid items={stats} />
      ) : null}

      {data ? (
        <Card className="border border-border py-4 shadow-none sm:py-6">
          <CardHeader className="flex shrink-0 flex-row items-center justify-between px-4 pb-2 sm:px-6">
            <CardTitle className="text-base font-semibold">{"用户余额快照"}</CardTitle>
          </CardHeader>
          <CardContent className="px-4 sm:px-6">
            <div className="mb-3 grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
              <div>
                <div className="text-muted-foreground">{"采集时间"}</div>
                <div className="font-medium text-foreground">
                  {data.userBalance.capturedAt ? dateTime(data.userBalance.capturedAt) : "—"}
                </div>
              </div>
              <div>
                <div className="text-muted-foreground">{"用户数"}</div>
                <div className="font-medium tabular-nums text-foreground">{data.userBalance.userCount}</div>
              </div>
              <div>
                <div className="text-muted-foreground">{"自助用户数"}</div>
                <div className="font-medium tabular-nums text-foreground">{data.userBalance.selfUserCount}</div>
              </div>
              <div>
                <div className="text-muted-foreground">{"外部未消费余额"}</div>
                <div className="font-medium tabular-nums text-foreground">{money(data.userBalance.externalRemaining)}</div>
              </div>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <Card className="border border-border py-4 shadow-none sm:py-6">
        <CardHeader className="flex shrink-0 flex-row items-center justify-between px-4 pb-2 sm:px-6">
          <CardTitle className="text-base font-semibold">{"上游对账明细"}</CardTitle>
        </CardHeader>
        <CardContent className="px-4 sm:px-6">
          {!data ? (
            <Empty>
              <EmptyTitle>{"暂无数据"}</EmptyTitle>
              <EmptyDescription>{"先在同名「渠道」页绑定上游，再执行同步。"}</EmptyDescription>
            </Empty>
          ) : data.vendors.length === 0 ? (
            <Empty>
              <EmptyTitle>{"暂无上游"}</EmptyTitle>
              <EmptyDescription>{"在「上游流水」页新增上游并录入充值。"}</EmptyDescription>
            </Empty>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{"上游"}</TableHead>
                    <TableHead className="text-right">{"充值"}</TableHead>
                    <TableHead className="text-right">{"退款"}</TableHead>
                    <TableHead className="text-right">{"扣减"}</TableHead>
                    <TableHead className="text-right">{"净充值"}</TableHead>
                    <TableHead className="text-right">{"已用"}</TableHead>
                    <TableHead className="text-right">{"当前余额"}</TableHead>
                    <TableHead className="text-right">{"理论余额"}</TableHead>
                    <TableHead className="text-right">{"账账差异"}</TableHead>
                    <TableHead className="text-right">{"站内消费"}</TableHead>
                    <TableHead className="text-right">{"消耗差异"}</TableHead>
                    <TableHead className="text-right">{"实际利润"}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.vendors.map((v) => (
                    <TableRow key={v.id}>
                      <TableCell className="font-medium">{v.name}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(v.recharge)}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(v.refund)}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(v.deduction)}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(v.rechargeNet)}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(v.used_amount)}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(v.current_balance)}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(v.theoreticalBalance)}</TableCell>
                      <TableCell
                        className={
                          "text-right tabular-nums " +
                          (Math.abs(v.ledgerDrift) > 0.01 ? "text-danger" : "")
                        }
                      >
                        {money(v.ledgerDrift)}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">{money(v.channel_usage)}</TableCell>
                      <TableCell
                        className={
                          "text-right tabular-nums " +
                          (Math.abs(v.theoreticalSpread) > 0.01 ? "text-warning" : "")
                        }
                      >
                        {money(v.theoreticalSpread)}
                      </TableCell>
                      <TableCell
                        className={
                          "text-right tabular-nums " +
                          (v.actualProfit >= 0 ? "text-success" : "text-danger")
                        }
                      >
                        {money(v.actualProfit)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
