import { useEffect, useState } from "react"
import { ArrowUpRight, KeyRound, LogOut } from "lucide-react"
import { Brand, CopyButton, Spinner, Toast } from "@/components/common"
import { Button } from "@/components/ui/button"
import { selfApi, message, session, type APIKey, type SessionInfo } from "@/lib/api"
import { startPolling } from "@/lib/polling"
import { Usage } from "@/pages/Usage"
import { CCSwitchImport } from "@/components/cc-switch-import"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"

export function UserPortal({ identity, logout }: { identity: SessionInfo; logout: () => void }) {
  const [keys, setKeys] = useState<APIKey[] | null>(null), [error, setError] = useState("")
  useEffect(() => {
    const polling = startPolling(async signal => {
      try {
        const result = await selfApi<{ data: APIKey[] }>("/keys", { signal })
        if (!signal.aborted) { setKeys(result.data); setError("") }
      } catch (e) { if (!signal.aborted) setError(message(e)) }
    })
    return () => polling.stop()
  }, [])
  return <div className="portal-shell">
    <header className="portal-header"><Brand /><div><span className="portal-identity">{identity.user?.name}</span><Button variant="ghost" size="sm" onClick={logout}><LogOut size={15} />退出登录</Button></div></header>
    <main className="portal-main">
      <div className="portal-key-banner"><div><KeyRound size={19} /><span>当前密钥 <strong>{identity.key?.name}</strong><code>{identity.key?.prefix}••••••</code></span></div><div><code>{location.origin}/v1</code><CopyButton value={`${location.origin}/v1`} label="复制 API 地址" /></div><Dialog><DialogTrigger asChild><Button variant="outline" size="sm"><ArrowUpRight />导入 CC Switch</Button></DialogTrigger><DialogContent><DialogHeader><DialogTitle>导入 CC Switch</DialogTitle><DialogDescription>使用当前登录密钥，将网关配置添加到本机客户端。</DialogDescription></DialogHeader><CCSwitchImport apiKey={session.get()} keyName={identity.key?.name || "当前密钥"} /></DialogContent></Dialog></div>
      {error && <div className="error-banner" role="alert">{error}</div>}
      {keys ? <Usage keys={keys} personal /> : !error && <Spinner />}
    </main><footer className="portal-footer">Mantle Mask · 用自己的 Key 调用模型，查看自己的用量。</footer><Toast />
  </div>
}
