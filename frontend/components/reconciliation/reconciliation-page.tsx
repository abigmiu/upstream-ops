import { useState } from "react"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { Button } from "@/components/ui/button"
import { RefreshCw } from "lucide-react"
import { DashboardTab } from "./dashboard-tab"
import { ChannelsTab } from "./channels-tab"
import { LedgerTab } from "./ledger-tab"
import { JobsTab } from "./jobs-tab"
import { ConfigTab } from "./config-tab"

export function ReconciliationPage() {
  const refresh = useTriggerRefresh()
  const [refreshing, setRefreshing] = useState(false)
  return (
    <section className="space-y-3">
      <header className="flex items-baseline justify-between">
        <div>
          <h1 className="text-lg font-semibold text-foreground">{"对账中心"}</h1>
          <p className="text-xs text-muted-foreground">
            {"上游充值/消费台账、渠道归属、站内充值、第三方与运营支出，一键生成对账与利润。"}
          </p>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            setRefreshing(true)
            refresh()
            window.setTimeout(() => setRefreshing(false), 600)
          }}
          disabled={refreshing}
        >
          <RefreshCw className={refreshing ? "animate-spin" : ""} />
          {"刷新"}
        </Button>
      </header>

      <Tabs defaultValue="dashboard" className="space-y-3">
        <TabsList className="flex-wrap">
          <TabsTrigger value="dashboard">{"总览"}</TabsTrigger>
          <TabsTrigger value="channels">{"渠道绑定"}</TabsTrigger>
          <TabsTrigger value="ledger">{"台账"}</TabsTrigger>
          <TabsTrigger value="jobs">{"同步任务"}</TabsTrigger>
          <TabsTrigger value="config">{"配置"}</TabsTrigger>
        </TabsList>

        <TabsContent value="dashboard">
          <DashboardTab />
        </TabsContent>
        <TabsContent value="channels">
          <ChannelsTab />
        </TabsContent>
        <TabsContent value="ledger">
          <LedgerTab />
        </TabsContent>
        <TabsContent value="jobs">
          <JobsTab />
        </TabsContent>
        <TabsContent value="config">
          <ConfigTab />
        </TabsContent>
      </Tabs>
    </section>
  )
}
