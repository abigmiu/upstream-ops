import { useEffect, useState } from "react"
import { toast } from "sonner"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch } from "@/lib/api"
import { useSystemConfig } from "@/lib/queries"
import type { SystemReconciliationConfig } from "@/lib/api-types"

const EMPTY: SystemReconciliationConfig = {
  enabled: false,
  sync_cron: "23 */10 * * * *",
  sync_hour: 3,
  site_url: "",
  token: "",
  user_id: "1",
  quota_per_usd: 500000,
  non_cash_topup_log_ids: [],
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <label className="text-xs text-muted-foreground">{label}</label>
      {children}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  )
}

export function ConfigTab() {
  const cfg = useSystemConfig()
  const [form, setForm] = useState<SystemReconciliationConfig>(EMPTY)
  const [busy, setBusy] = useState(false)
  const [nonCashText, setNonCashText] = useState("")

  useEffect(() => {
    const r = cfg.data?.config?.reconciliation
    if (r) {
      setForm({ ...EMPTY, ...r })
      setNonCashText((r.non_cash_topup_log_ids ?? []).join(", "))
    }
  }, [cfg.data])

  function set(patch: Partial<SystemReconciliationConfig>) {
    setForm((f) => ({ ...f, ...patch }))
  }

  async function save(apply: boolean) {
    if (!cfg.data) return
    setBusy(true)
    try {
      const nextConfig = {
        ...cfg.data.config,
        reconciliation: {
          ...form,
          non_cash_topup_log_ids: nonCashText
            .split(/[,\s]+/)
            .map((s) => Number(s.trim()))
            .filter((n) => Number.isFinite(n) && n > 0),
        },
      }
      await apiFetch("/settings/config", {
        method: "PUT",
        body: JSON.stringify(nextConfig),
      })
      if (apply) {
        await apiFetch("/settings/apply", { method: "POST" })
      }
      toast.success(apply ? "已保存并应用" : "已保存")
      cfg.refetch()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "保存失败")
    } finally {
      setBusy(false)
    }
  }

  if (cfg.loading && !cfg.data) {
    return <Skeleton className="h-64 w-full" />
  }

  return (
    <Card className="gap-3">
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">{"对账站点（NewAPI）配置"}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4 pt-0">
        <div className="flex items-center justify-between rounded-md border p-3">
          <div>
            <div className="text-sm font-medium">{"启用定时同步"}</div>
            <p className="text-xs text-muted-foreground">
              {"开启后按下方 Cron 表达式自动执行增量同步。"}
            </p>
          </div>
          <Switch checked={form.enabled} onCheckedChange={(v) => set({ enabled: v })} />
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="站点地址" hint="如 https://ai.example.com（不加结尾斜杠）">
            <Input
              value={form.site_url}
              onChange={(e) => set({ site_url: e.target.value })}
              placeholder="https://ai.wewont.top"
            />
          </Field>
          <Field label="访问令牌 Token" hint="NewAPI 系统令牌（Authorization）">
            <Input
              type="password"
              value={form.token}
              onChange={(e) => set({ token: e.target.value })}
              placeholder="NewAPI Token"
            />
          </Field>
          <Field label="用户 ID" hint="New-Api-User 头，通常为 1">
            <Input
              value={form.user_id}
              onChange={(e) => set({ user_id: e.target.value })}
            />
          </Field>
          <Field label="每美元额度" hint="NewAPI QUOTA_PER_USD，默认 500000">
            <Input
              type="number"
              value={form.quota_per_usd}
              onChange={(e) => set({ quota_per_usd: Number(e.target.value) })}
            />
          </Field>
          <Field label="定时同步 Cron" hint="6 字段 cron（含秒），默认 23 */10 * * * *">
            <Input
              value={form.sync_cron}
              onChange={(e) => set({ sync_cron: e.target.value })}
              className="font-mono"
            />
          </Field>
          <Field label="每日补数小时" hint="0–23，默认 3 点执行整天 replace 补数">
            <Input
              type="number"
              value={form.sync_hour}
              onChange={(e) => set({ sync_hour: Number(e.target.value) })}
            />
          </Field>
        </div>

        <Field
          label="非现金充值日志 ID"
          hint="逗号分隔。这些充值不计入现金口径（如管理员补单）。"
        >
          <Input
            value={nonCashText}
            onChange={(e) => setNonCashText(e.target.value)}
            placeholder="如 1024, 2048"
            className="font-mono"
          />
        </Field>

        <div className="flex gap-2">
          <Button disabled={busy} onClick={() => save(false)}>
            {"保存"}
          </Button>
          <Button variant="secondary" disabled={busy} onClick={() => save(true)}>
            {"保存并应用"}
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          {"配置文件："}
          {cfg.data?.config_path ?? "—"}
        </p>
      </CardContent>
    </Card>
  )
}
