import { useState } from "react"
import { toast } from "sonner"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { Empty, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { apiFetch } from "@/lib/api"
import { dateTime, money, shortTime } from "@/lib/format"
import {
  useChannelUsageStats,
  useDailyTopups,
  useOperatingExpenses,
  useThirdPartyEntries,
  useTopupLogs,
  useUserBalanceSnapshots,
  useVendorEntries,
  useVendors,
  useVendorSnapshots,
} from "@/lib/queries"

const LEDGER_TYPES = ["充值", "补款", "退款", "扣减"]

function VendorSelect({
  value,
  onChange,
}: {
  value: string
  onChange: (v: string) => void
}) {
  const vendors = useVendors()
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className="w-full">
        <SelectValue placeholder="选择上游" />
      </SelectTrigger>
      <SelectContent>
        {(vendors.data ?? []).map((v) => (
          <SelectItem key={v.id} value={String(v.id)}>
            {v.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

/** 通用「表单弹窗」封装，避免每个子表重复。 */
function AddDialog({
  title,
  label,
  onDone,
  children,
  onSubmit,
}: {
  title: string
  label: string
  onDone: () => void
  children: (state: {
    set: (patch: Record<string, string>) => void
  }) => React.ReactNode
  onSubmit: (state: Record<string, string>) => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [state, setState] = useState<Record<string, string>>({})
  const set = (patch: Record<string, string>) => setState((s) => ({ ...s, ...patch }))
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="secondary">
          {label}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">{children({ set })}</div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            {"取消"}
          </Button>
          <Button
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              try {
                await onSubmit(state)
                toast.success("已保存")
                setOpen(false)
                setState({})
                onDone()
              } catch (e) {
                toast.error(e instanceof Error ? e.message : "保存失败")
              } finally {
                setBusy(false)
              }
            }}
          >
            {"保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Field({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="space-y-1">
      <label className="text-xs text-muted-foreground">{label}</label>
      {children}
    </div>
  )
}

export function LedgerTab() {
  return (
    <Tabs defaultValue="entries" className="space-y-3">
      <TabsList className="flex-wrap">
        <TabsTrigger value="entries">{"上游流水"}</TabsTrigger>
        <TabsTrigger value="snapshots">{"上游快照"}</TabsTrigger>
        <TabsTrigger value="topups">{"站内充值"}</TabsTrigger>
        <TabsTrigger value="usage">{"归属消费"}</TabsTrigger>
        <TabsTrigger value="thirdparty">{"第三方"}</TabsTrigger>
        <TabsTrigger value="expenses">{"运营支出"}</TabsTrigger>
        <TabsTrigger value="users">{"用户余额"}</TabsTrigger>
      </TabsList>

      <TabsContent value="entries">
        <EntriesPanel />
      </TabsContent>
      <TabsContent value="snapshots">
        <SnapshotsPanel />
      </TabsContent>
      <TabsContent value="topups">
        <TopupsPanel />
      </TabsContent>
      <TabsContent value="usage">
        <UsagePanel />
      </TabsContent>
      <TabsContent value="thirdparty">
        <ThirdPartyPanel />
      </TabsContent>
      <TabsContent value="expenses">
        <ExpensesPanel />
      </TabsContent>
      <TabsContent value="users">
        <UsersPanel />
      </TabsContent>
    </Tabs>
  )
}

function EntriesPanel() {
  const entries = useVendorEntries()
  return (
    <Card className="border border-border py-4 shadow-none sm:py-6">
      <CardHeader className="flex-row items-center justify-between pb-2">
        <CardTitle className="text-base font-semibold">{"上游流水（追加式，余额自动推导）"}</CardTitle>
        <AddDialog
          title="新增上游流水"
          label="新增流水"
          onDone={() => entries.refetch()}
          onSubmit={async (s) => {
            await apiFetch("/reconciliation/vendor-entries", {
              method: "POST",
              body: JSON.stringify({
                vendor_id: Number(s.vendor_id),
                entry_type: s.entry_type || "充值",
                amount: Number(s.amount),
                occurred_at: s.occurred_at ? new Date(s.occurred_at).toISOString() : undefined,
                note: s.note || "",
              }),
            })
          }}
        >
          {({ set }) => (
            <>
              <Field label="上游">
                <VendorSelect value="" onChange={(v) => set({ vendor_id: v })} />
              </Field>
              <Field label="类型">
                <Select value="充值" onValueChange={(v) => set({ entry_type: v })}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {LEDGER_TYPES.map((t) => (
                      <SelectItem key={t} value={t}>
                        {t}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label="金额">
                <Input type="number" step="0.000001" onChange={(e) => set({ amount: e.target.value })} />
              </Field>
              <Field label="发生时间">
                <Input type="datetime-local" onChange={(e) => set({ occurred_at: e.target.value })} />
              </Field>
              <Field label="备注">
                <Input onChange={(e) => set({ note: e.target.value })} />
              </Field>
            </>
          )}
        </AddDialog>
      </CardHeader>
      <CardContent className="pt-0">
        {entries.loading && !entries.data ? (
          <Skeleton className="h-32 w-full" />
        ) : (entries.data ?? []).length === 0 ? (
          <Empty>
            <EmptyTitle>{"暂无流水"}</EmptyTitle>
            <EmptyDescription>{"记录上游充值、退款、扣减，余额会按流水顺序自动推导。"}</EmptyDescription>
          </Empty>
        ) : (
          <div className="max-h-[32rem] overflow-y-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{"时间"}</TableHead>
                  <TableHead>{"上游"}</TableHead>
                  <TableHead>{"类型"}</TableHead>
                  <TableHead className="text-right">{"金额"}</TableHead>
                  <TableHead className="text-right">{"计入余额"}</TableHead>
                  <TableHead className="text-right">{"变动前"}</TableHead>
                  <TableHead className="text-right">{"变动后"}</TableHead>
                  <TableHead>{"备注"}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.data!.map((e) => (
                  <TableRow key={e.id}>
                    <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(e.occurred_at)}</TableCell>
                    <TableCell className="font-medium">{e.vendor_name}</TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          e.entry_type === "充值" || e.entry_type === "补款"
                            ? "default"
                            : "destructive"
                        }
                      >
                        {e.entry_type}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{money(e.amount)}</TableCell>
                    <TableCell className="text-right tabular-nums">{money(e.actual_amount)}</TableCell>
                    <TableCell className="text-right tabular-nums text-muted-foreground">{money(e.balance_before)}</TableCell>
                    <TableCell className="text-right tabular-nums">{money(e.balance_after)}</TableCell>
                    <TableCell className="max-w-[16rem] truncate text-muted-foreground" title={e.note}>
                      {e.note || "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function SnapshotsPanel() {
  const snapshots = useVendorSnapshots()
  return (
    <Card className="border border-border py-4 shadow-none sm:py-6">
      <CardHeader className="flex-row items-center justify-between pb-2">
        <CardTitle className="text-base font-semibold">{"上游余额快照"}</CardTitle>
        <AddDialog
          title="手动补录上游快照"
          label="手动补录"
          onDone={() => snapshots.refetch()}
          onSubmit={async (s) => {
            await apiFetch("/reconciliation/vendor-snapshots", {
              method: "POST",
              body: JSON.stringify({
                vendor_id: Number(s.vendor_id),
                current_balance: Number(s.current_balance),
                note: s.note || "",
              }),
            })
          }}
        >
          {({ set }) => (
            <>
              <Field label="上游">
                <VendorSelect value="" onChange={(v) => set({ vendor_id: v })} />
              </Field>
              <Field label="当前余额">
                <Input type="number" step="0.000001" onChange={(e) => set({ current_balance: e.target.value })} />
              </Field>
              <Field label="备注">
                <Input onChange={(e) => set({ note: e.target.value })} />
              </Field>
            </>
          )}
        </AddDialog>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        <p className="text-xs text-muted-foreground">
          {"余额由监控自动采集，累计已用按「累计充值 − 当前余额」自动算出，无需手工记录。仅对没有监控渠道的上游才需手动补录。"}
        </p>
        {(snapshots.data ?? []).length === 0 ? (
          <Empty>
            <EmptyTitle>{"暂无快照"}</EmptyTitle>
            <EmptyDescription>{"绑定监控渠道后，余额会随监控自动落成每日快照。"}</EmptyDescription>
          </Empty>
        ) : (
          <div className="max-h-[32rem] overflow-y-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{"日期"}</TableHead>
                  <TableHead>{"上游"}</TableHead>
                  <TableHead className="text-right">{"已用金额"}</TableHead>
                  <TableHead className="text-right">{"当前余额"}</TableHead>
                  <TableHead className="text-right">{"较上次余额"}</TableHead>
                  <TableHead>{"备注"}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {snapshots.data!.map((s) => {
                  const delta =
                    s.previous_balance == null ? null : s.current_balance - s.previous_balance
                  return (
                    <TableRow key={s.id}>
                      <TableCell className="font-mono text-xs">{s.day}</TableCell>
                      <TableCell className="font-medium">{s.vendor_name}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(s.used_amount)}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(s.current_balance)}</TableCell>
                      <TableCell
                        className={
                          "text-right tabular-nums " +
                          (delta == null
                            ? "text-muted-foreground"
                            : delta >= 0
                              ? "text-success"
                              : "text-danger")
                        }
                      >
                        {delta == null ? "—" : money(delta)}
                      </TableCell>
                      <TableCell className="text-muted-foreground">{s.note || "—"}</TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function TopupsPanel() {
  const topups = useTopupLogs()
  const daily = useDailyTopups()

  async function toggleCash(id: number, inCash: boolean) {
    try {
      await apiFetch(`/reconciliation/topup-logs/${id}/cash`, {
        method: "POST",
        body: JSON.stringify({ included_in_cash: inCash }),
      })
      toast.success("已更新口径")
      topups.refetch()
      daily.refetch()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "更新失败")
    }
  }

  return (
    <div className="space-y-4">
      <Card className="border border-border py-4 shadow-none sm:py-6">
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-semibold">{"按天现金充值汇总"}</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          {(daily.data ?? []).length === 0 ? (
            <Empty>
              <EmptyTitle>{"暂无数据"}</EmptyTitle>
            </Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{"日期"}</TableHead>
                  <TableHead className="text-right">{"现金充值"}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {daily.data!.map((d) => (
                  <TableRow key={d.day}>
                    <TableCell className="font-mono text-xs">{d.day}</TableCell>
                    <TableCell className="text-right tabular-nums">{money(d.amount)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card className="border border-border py-4 shadow-none sm:py-6">
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-semibold">{"站内充值日志（可标记非现金）"}</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          {(topups.data ?? []).length === 0 ? (
            <Empty>
              <EmptyTitle>{"暂无充值记录"}</EmptyTitle>
              <EmptyDescription>{"执行同步后从 NewAPI 拉取。"}</EmptyDescription>
            </Empty>
          ) : (
            <div className="max-h-[32rem] overflow-y-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{"时间"}</TableHead>
                    <TableHead>{"用户"}</TableHead>
                    <TableHead>{"类型"}</TableHead>
                    <TableHead className="text-right">{"金额"}</TableHead>
                    <TableHead>{"现金口径"}</TableHead>
                    <TableHead>{"内容"}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {topups.data!.map((t) => (
                    <TableRow key={t.id}>
                      <TableCell className="whitespace-nowrap text-muted-foreground">{shortTime(t.occurred_at)}</TableCell>
                      <TableCell className="font-medium">{t.username}</TableCell>
                      <TableCell>{t.log_type}</TableCell>
                      <TableCell className="text-right tabular-nums">{money(t.amount)}</TableCell>
                      <TableCell>
                        <Button
                          size="sm"
                          variant={t.included_in_cash ? "default" : "secondary"}
                          onClick={() => toggleCash(t.id, !t.included_in_cash)}
                        >
                          {t.included_in_cash ? "计入" : "不计入"}
                          {t.included_in_cash_manual ? " · 手动" : ""}
                        </Button>
                      </TableCell>
                      <TableCell className="max-w-[20rem] truncate text-muted-foreground" title={t.content}>
                        {t.content}
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

function UsagePanel() {
  const usage = useChannelUsageStats()
  return (
    <Card className="border border-border py-4 shadow-none sm:py-6">
      <CardHeader className="pb-2">
        <CardTitle className="text-base font-semibold">{"渠道归属消费（站内消费归属到上游，每天一行）"}</CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        {(usage.data ?? []).length === 0 ? (
          <Empty>
            <EmptyTitle>{"暂无消费数据"}</EmptyTitle>
            <EmptyDescription>{"绑定渠道后执行同步，会按天记录渠道消费。"}</EmptyDescription>
          </Empty>
        ) : (
          <div className="max-h-[32rem] overflow-y-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{"日期"}</TableHead>
                  <TableHead>{"上游"}</TableHead>
                  <TableHead>{"渠道"}</TableHead>
                  <TableHead className="text-right">{"消费"}</TableHead>
                  <TableHead>{"窗口"}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {usage.data!.map((u) => (
                  <TableRow key={u.id}>
                    <TableCell className="font-mono text-xs">{u.day}</TableCell>
                    <TableCell className="font-medium">{u.vendor_name}</TableCell>
                    <TableCell>
                      #{u.channel_id} {u.channel_name}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{money(u.amount)}</TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      {shortTime(u.start_at)} → {shortTime(u.end_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

const TP_TYPES = [
  { value: "topup", label: "充值" },
  { value: "withdrawal", label: "提现" },
]

function ThirdPartyPanel() {
  const entries = useThirdPartyEntries()
  return (
    <Card className="border border-border py-4 shadow-none sm:py-6">
      <CardHeader className="flex-row items-center justify-between pb-2">
        <CardTitle className="text-base font-semibold">{"第三方平台充值 / 提现"}</CardTitle>
        <AddDialog
          title="新增第三方记录"
          label="新增记录"
          onDone={() => entries.refetch()}
          onSubmit={async (s) => {
            await apiFetch("/reconciliation/third-party-entries", {
              method: "POST",
              body: JSON.stringify({
                platform_name: s.platform_name || "",
                entry_type: s.entry_type || "topup",
                amount: Number(s.amount),
                occurred_at: s.occurred_at ? new Date(s.occurred_at).toISOString() : undefined,
                note: s.note || "",
              }),
            })
          }}
        >
          {({ set }) => (
            <>
              <Field label="平台">
                <Input onChange={(e) => set({ platform_name: e.target.value })} placeholder="如：支付宝 / 微信" />
              </Field>
              <Field label="类型">
                <Select value="topup" onValueChange={(v) => set({ entry_type: v })}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {TP_TYPES.map((t) => (
                      <SelectItem key={t.value} value={t.value}>
                        {t.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label="金额">
                <Input type="number" step="0.000001" onChange={(e) => set({ amount: e.target.value })} />
              </Field>
              <Field label="发生时间">
                <Input type="datetime-local" onChange={(e) => set({ occurred_at: e.target.value })} />
              </Field>
              <Field label="备注">
                <Input onChange={(e) => set({ note: e.target.value })} />
              </Field>
            </>
          )}
        </AddDialog>
      </CardHeader>
      <CardContent className="pt-0">
        {(entries.data ?? []).length === 0 ? (
          <Empty>
            <EmptyTitle>{"暂无第三方记录"}</EmptyTitle>
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{"时间"}</TableHead>
                <TableHead>{"平台"}</TableHead>
                <TableHead>{"类型"}</TableHead>
                <TableHead className="text-right">{"金额"}</TableHead>
                <TableHead>{"备注"}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.data!.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(e.occurred_at)}</TableCell>
                  <TableCell className="font-medium">{e.platform_name || "—"}</TableCell>
                  <TableCell>
                    <Badge variant={e.entry_type === "提现" ? "destructive" : "default"}>
                      {e.entry_type}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{money(e.amount)}</TableCell>
                  <TableCell className="text-muted-foreground">{e.note || "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}

function ExpensesPanel() {
  const expenses = useOperatingExpenses()
  return (
    <Card className="border border-border py-4 shadow-none sm:py-6">
      <CardHeader className="flex-row items-center justify-between pb-2">
        <CardTitle className="text-base font-semibold">{"运营支出"}</CardTitle>
        <AddDialog
          title="新增运营支出"
          label="新增支出"
          onDone={() => expenses.refetch()}
          onSubmit={async (s) => {
            await apiFetch("/reconciliation/operating-expenses", {
              method: "POST",
              body: JSON.stringify({
                category: s.category || "",
                amount: Number(s.amount),
                occurred_at: s.occurred_at ? new Date(s.occurred_at).toISOString() : undefined,
                note: s.note || "",
              }),
            })
          }}
        >
          {({ set }) => (
            <>
              <Field label="类别">
                <Input onChange={(e) => set({ category: e.target.value })} placeholder="如：服务器 / 域名" />
              </Field>
              <Field label="金额">
                <Input type="number" step="0.000001" onChange={(e) => set({ amount: e.target.value })} />
              </Field>
              <Field label="发生时间">
                <Input type="datetime-local" onChange={(e) => set({ occurred_at: e.target.value })} />
              </Field>
              <Field label="备注">
                <Input onChange={(e) => set({ note: e.target.value })} />
              </Field>
            </>
          )}
        </AddDialog>
      </CardHeader>
      <CardContent className="pt-0">
        {(expenses.data ?? []).length === 0 ? (
          <Empty>
            <EmptyTitle>{"暂无运营支出"}</EmptyTitle>
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{"时间"}</TableHead>
                <TableHead>{"类别"}</TableHead>
                <TableHead className="text-right">{"金额"}</TableHead>
                <TableHead>{"备注"}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {expenses.data!.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(e.occurred_at)}</TableCell>
                  <TableCell className="font-medium">{e.category || "—"}</TableCell>
                  <TableCell className="text-right tabular-nums">{money(e.amount)}</TableCell>
                  <TableCell className="text-muted-foreground">{e.note || "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}

function UsersPanel() {
  const users = useUserBalanceSnapshots()
  return (
    <Card className="border border-border py-4 shadow-none sm:py-6">
      <CardHeader className="pb-2">
        <CardTitle className="text-base font-semibold">{"用户余额快照（自助用户标记影响外部口径）"}</CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        {(users.data ?? []).length === 0 ? (
          <Empty>
            <EmptyTitle>{"暂无用户余额快照"}</EmptyTitle>
            <EmptyDescription>{"同步用户余额后自动记录；在「同步任务」页设置自助用户名单。"}</EmptyDescription>
          </Empty>
        ) : (
          <div className="max-h-[32rem] overflow-y-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{"日期"}</TableHead>
                  <TableHead>{"用户"}</TableHead>
                  <TableHead className="text-right">{"剩余额度"}</TableHead>
                  <TableHead className="text-right">{"已用额度"}</TableHead>
                  <TableHead className="text-right">{"请求数"}</TableHead>
                  <TableHead>{"口径"}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {users.data!.map((u) => (
                  <TableRow key={u.id}>
                    <TableCell className="font-mono text-xs">{u.day}</TableCell>
                    <TableCell className="font-medium">{u.username}</TableCell>
                    <TableCell className="text-right tabular-nums">{money(u.remaining_amount)}</TableCell>
                    <TableCell className="text-right tabular-nums">{money(u.used_amount)}</TableCell>
                    <TableCell className="text-right tabular-nums">{u.request_count}</TableCell>
                    <TableCell>
                      <Badge variant={u.is_self ? "default" : "secondary"}>
                        {u.is_self ? "自助" : "外部"}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
