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
import { dateTime } from "@/lib/format"
import {
  useChannelBindings,
  useChannelBindingHistory,
  useReconciliationChannels,
  useUpstreamChannelBindings,
  useVendors,
} from "@/lib/queries"

function BindingDialog({
  channelId,
  channelName,
  groupName,
  onDone,
}: {
  channelId?: number
  channelName?: string
  groupName?: string
  onDone: () => void
}) {
  const vendors = useVendors()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [selectedChannel, setSelectedChannel] = useState<string>("")
  const [vendorId, setVendorId] = useState<string>("")
  const [note, setNote] = useState("")

  const channels = useReconciliationChannels()
  const effectiveChannel = channelId ?? Number(selectedChannel)
  const effectiveName =
    channelName ?? channels.data?.find((c) => c.channel_id === effectiveChannel)?.channel_name ?? ""

  async function submit() {
    if (!(effectiveChannel > 0) || !vendorId) {
      toast.error("请选择渠道与上游")
      return
    }
    setBusy(true)
    try {
      await apiFetch("/reconciliation/channel-bindings", {
        method: "POST",
        body: JSON.stringify({
          channel_id: effectiveChannel,
          channel_name: effectiveName,
          group_name: groupName ?? "",
          vendor_id: Number(vendorId),
          note,
        }),
      })
      toast.success("已绑定")
      setOpen(false)
      setNote("")
      setVendorId("")
      setSelectedChannel("")
      onDone()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "绑定失败")
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant={channelId ? "outline" : "secondary"}>
          {channelId ? "改绑" : "新增绑定"}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{"渠道 → 上游绑定"}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          {channelId ? (
            <div className="text-sm text-muted-foreground">
              {"渠道："}
              <span className="font-medium text-foreground">
                #{channelId} {channelName}
              </span>
            </div>
          ) : (
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">{"渠道"}</label>
              <Select value={selectedChannel} onValueChange={setSelectedChannel}>
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择渠道" />
                </SelectTrigger>
                <SelectContent>
                  {(channels.data ?? []).map((c) => (
                    <SelectItem key={c.channel_id} value={String(c.channel_id)}>
                      #{c.channel_id} {c.channel_name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">{"归属上游"}</label>
            <Select value={vendorId} onValueChange={setVendorId}>
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
          </div>
          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">{"备注"}</label>
            <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="可选" />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            {"取消"}
          </Button>
          <Button onClick={submit} disabled={busy}>
            {"保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function ChannelsTab() {
  const bindings = useChannelBindings()
  const history = useChannelBindingHistory()
  const upstreamBindings = useUpstreamChannelBindings()
  const vendors = useVendors()

  const vendorName = (id: number) => vendors.data?.find((v) => v.id === id)?.name ?? `#${id}`

  async function removeUpstreamBinding(id: number) {
    try {
      await apiFetch(`/reconciliation/upstream-channel-bindings/${id}/delete`, { method: "POST" })
      toast.success("已解除")
      upstreamBindings.refetch()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "操作失败")
    }
  }

  return (
    <div className="space-y-4">
      <Card className="gap-2">
        <CardHeader className="flex-row items-center justify-between pb-2">
          <CardTitle className="text-sm">{"渠道 → 上游绑定（当前生效）"}</CardTitle>
          <BindingDialog onDone={() => bindings.refetch()} />
        </CardHeader>
        <CardContent className="pt-0">
          {bindings.loading && !bindings.data ? (
            <Skeleton className="h-32 w-full" />
          ) : (bindings.data ?? []).length === 0 ? (
            <Empty>
              <EmptyTitle>{"暂无绑定"}</EmptyTitle>
              <EmptyDescription>{"把站内渠道绑定到上游，消费才能归属到上游。"}</EmptyDescription>
            </Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{"渠道"}</TableHead>
                  <TableHead>{"分组"}</TableHead>
                  <TableHead>{"上游"}</TableHead>
                  <TableHead>{"生效起始"}</TableHead>
                  <TableHead className="text-right">{"操作"}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {bindings.data!.map((b) => (
                  <TableRow key={b.id}>
                    <TableCell>
                      #{b.channel_id} {b.channel_name}
                    </TableCell>
                    <TableCell className="text-muted-foreground">{b.group_name || "—"}</TableCell>
                    <TableCell className="font-medium">{vendorName(b.vendor_id)}</TableCell>
                    <TableCell className="text-muted-foreground">{dateTime(b.effective_from)}</TableCell>
                    <TableCell className="text-right">
                      <BindingDialog
                        channelId={b.channel_id}
                        channelName={b.channel_name}
                        groupName={b.group_name}
                        onDone={() => {
                          bindings.refetch()
                          history.refetch()
                        }}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card className="gap-2">
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">{"上游渠道 → 上游绑定（NewAPI 侧）"}</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          {(upstreamBindings.data ?? []).length === 0 ? (
            <Empty>
              <EmptyTitle>{"暂无上游渠道绑定"}</EmptyTitle>
              <EmptyDescription>{"用于把上游站点里的渠道 id 映射到本地上游。"}</EmptyDescription>
            </Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{"上游渠道 ID"}</TableHead>
                  <TableHead>{"名称"}</TableHead>
                  <TableHead>{"上游"}</TableHead>
                  <TableHead>{"备注"}</TableHead>
                  <TableHead className="text-right">{"操作"}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {upstreamBindings.data!.map((b) => (
                  <TableRow key={b.id}>
                    <TableCell className="font-mono text-xs">{b.upstream_channel_id}</TableCell>
                    <TableCell>{b.upstream_channel_name || "—"}</TableCell>
                    <TableCell className="font-medium">{vendorName(b.vendor_id)}</TableCell>
                    <TableCell className="text-muted-foreground">{b.note || "—"}</TableCell>
                    <TableCell className="text-right">
                      <Button size="sm" variant="ghost" onClick={() => removeUpstreamBinding(b.id)}>
                        {"解除"}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card className="gap-2">
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">{"绑定变更历史"}</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          {(history.data ?? []).length === 0 ? (
            <Empty>
              <EmptyTitle>{"暂无历史"}</EmptyTitle>
            </Empty>
          ) : (
            <div className="max-h-80 overflow-y-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{"时间"}</TableHead>
                    <TableHead>{"渠道"}</TableHead>
                    <TableHead>{"上游"}</TableHead>
                    <TableHead>{"状态"}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {history.data!.map((h) => (
                    <TableRow key={h.id}>
                      <TableCell className="text-muted-foreground">{dateTime(h.effective_from)}</TableCell>
                      <TableCell>
                        #{h.channel_id} {h.channel_name}
                      </TableCell>
                      <TableCell>{vendorName(h.vendor_id)}</TableCell>
                      <TableCell>
                        <Badge variant={h.effective_to ? "secondary" : "default"}>
                          {h.effective_to ? "已失效" : "生效中"}
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
    </div>
  )
}
