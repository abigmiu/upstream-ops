import { useState } from "react"
import { toast } from "sonner"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { Empty, EmptyTitle, EmptyDescription } from "@/components/ui/empty"
import { apiFetch } from "@/lib/api"
import { dateTime, shortTime } from "@/lib/format"
import { useSelfUsers, useSyncTaskLogs } from "@/lib/queries"

export function JobsTab() {
  const logs = useSyncTaskLogs()
  const selfUsers = useSelfUsers()
  const [busy, setBusy] = useState<string | null>(null)
  const [rangeStart, setRangeStart] = useState("")
  const [rangeEnd, setRangeEnd] = useState("")
  const [selfText, setSelfText] = useState<string | null>(null)

  const selfList = selfText ?? (selfUsers.data ?? []).join("\n")

  async function run(kind: "channels" | "incremental" | "user-balance" | "range" | "backfill-usage" | "test") {
    setBusy(kind)
    try {
      let path = "/reconciliation/sync/incremental"
      let body: Record<string, unknown> = {}
      if (kind === "channels") path = "/reconciliation/sync/channels"
      if (kind === "test") path = "/reconciliation/sync/test"
      if (kind === "user-balance") path = "/reconciliation/sync/user-balance"
      if (kind === "range") {
        path = "/reconciliation/sync/range"
        body = { start_date: rangeStart, end_date: rangeEnd }
      }
      if (kind === "backfill-usage") {
        path = "/reconciliation/sync/backfill-usage"
        body = { start_date: rangeStart, end_date: rangeEnd }
      }
      const res = await apiFetch<{ data?: unknown }>(path, {
        method: "POST",
        body: JSON.stringify(body),
      })
      toast.success("执行完成" + (res?.data ? `：${JSON.stringify(res.data)}` : ""))
      logs.refetch()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "执行失败")
      logs.refetch()
    } finally {
      setBusy(null)
    }
  }

  async function saveSelfUsers() {
    const list = selfList
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean)
    try {
      await apiFetch("/reconciliation/settings/self-users", {
        method: "POST",
        body: JSON.stringify({ usernames: list }),
      })
      toast.success("已保存")
      selfUsers.refetch()
      setSelfText(null)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "保存失败")
    }
  }

  return (
    <div className="space-y-4">
      <Card className="border border-border py-4 shadow-none sm:py-6">
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-semibold">{"同步任务"}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 pt-0">
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="secondary" disabled={busy !== null} onClick={() => run("test")}>
              {"测试连接"}
            </Button>
            <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => run("channels")}>
              {"同步渠道列表"}
            </Button>
            <Button size="sm" disabled={busy !== null} onClick={() => run("incremental")}>
              {"增量同步"}
            </Button>
            <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => run("user-balance")}>
              {"刷新用户余额"}
            </Button>
          </div>
          <div className="flex flex-wrap items-end gap-2">
            <div className="flex flex-col gap-1">
              <label className="text-xs text-muted-foreground">{"补数开始"}</label>
              <Input type="date" value={rangeStart} onChange={(e) => setRangeStart(e.target.value)} className="h-8 w-36" />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs text-muted-foreground">{"补数结束"}</label>
              <Input type="date" value={rangeEnd} onChange={(e) => setRangeEnd(e.target.value)} className="h-8 w-36" />
            </div>
            <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => run("range")}>
              {"按区间补数"}
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={busy !== null}
              title="直连 new-api 的 PostgreSQL，一条 SQL 按天重建渠道消费；可重复执行（整天 replace）"
              onClick={() => run("backfill-usage")}
            >
              {"从库回填消费(PG)"}
            </Button>
            <span className="text-xs text-muted-foreground">{"留空默认补昨天的数据（整天 replace）"}</span>
          </div>
        </CardContent>
      </Card>

      <Card className="border border-border py-4 shadow-none sm:py-6">
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-semibold">{"自助用户名单（每行一个用户名）"}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 pt-0">
          <Textarea
            value={selfList}
            onChange={(e) => setSelfText(e.target.value)}
            rows={5}
            placeholder="站长自己的账号，用于区分自助/外部口径"
            className="font-mono text-xs"
          />
          <div>
            <Button size="sm" onClick={saveSelfUsers}>
              {"保存名单"}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="border border-border py-4 shadow-none sm:py-6">
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-semibold">{"同步日志（每天一行）"}</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          {logs.loading && !logs.data ? (
            <Skeleton className="h-32 w-full" />
          ) : (logs.data ?? []).length === 0 ? (
            <Empty>
              <EmptyTitle>{"暂无同步日志"}</EmptyTitle>
              <EmptyDescription>{"执行一次同步后会在此显示。"}</EmptyDescription>
            </Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{"完成时间"}</TableHead>
                  <TableHead>{"任务"}</TableHead>
                  <TableHead>{"来源"}</TableHead>
                  <TableHead>{"状态"}</TableHead>
                  <TableHead className="text-right">{"充值"}</TableHead>
                  <TableHead className="text-right">{"消费"}</TableHead>
                  <TableHead className="text-right">{"用户"}</TableHead>
                  <TableHead>{"窗口"}</TableHead>
                  <TableHead>{"消息"}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {logs.data!.map((l) => (
                  <TableRow key={l.id}>
                    <TableCell className="whitespace-nowrap text-muted-foreground">{dateTime(l.completed_at)}</TableCell>
                    <TableCell>{l.job_name}</TableCell>
                    <TableCell className="text-muted-foreground">{l.source}</TableCell>
                    <TableCell>
                      <Badge variant={l.status === "failed" ? "destructive" : "default"}>
                        {l.status === "failed" ? "失败" : "成功"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{l.topup_count}</TableCell>
                    <TableCell className="text-right tabular-nums">{l.usage_count}</TableCell>
                    <TableCell className="text-right tabular-nums">{l.user_balance_count}</TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      {l.window_start && l.window_end ? `${shortTime(l.window_start)} → ${shortTime(l.window_end)}` : "—"}
                    </TableCell>
                    <TableCell className="max-w-[24rem] truncate text-muted-foreground" title={l.message}>
                      {l.message || "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
