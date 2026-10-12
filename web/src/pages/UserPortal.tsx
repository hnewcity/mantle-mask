import { useEffect, useState } from "react"
import { KeyRound, LogOut } from "lucide-react"
import { Brand, CopyButton, Spinner, Toast } from "@/components/common"
import { Button } from "@/components/ui/button"
import { selfApi, message, type APIKey, type SessionInfo } from "@/lib/api"
import { Usage } from "@/pages/Usage"

export function UserPortal({ identity, logout }: { identity: SessionInfo; logout: () => void }) {
  const [keys, setKeys] = useState<APIKey[] | null>(null), [error, setError] = useState("")
  useEffect(() => {
    let active = true
    const load = () => selfApi<{ data: APIKey[] }>("/keys")
      .then(result => { if (active) { setKeys(result.data); setError("") } })
      .catch(e => { if (active) setError(message(e)) })
    void load(); const timer = setInterval(() => void load(), 15000)
    return () => { active = false; clearInterval(timer) }
  }, [])
  return <div className="portal-shell">
    <header className="portal-header"><Brand /><div><span className="portal-identity">{identity.user?.name}</span><Button variant="ghost" size="sm" onClick={logout}><LogOut size={15} />退出登录</Button></div></header>
    <main className="portal-main">
      <div className="portal-key-banner"><div><KeyRound size={19} /><span>当前密钥 <strong>{identity.key?.name}</strong><code>{identity.key?.prefix}••••••</code></span></div><div><code>{location.origin}/v1</code><CopyButton value={`${location.origin}/v1`} label="复制 API 地址" /></div></div>
      {error && <div className="error-banner" role="alert">{error}</div>}
      {keys ? <Usage keys={keys} personal /> : !error && <Spinner />}
    </main><footer className="portal-footer">Mantle Mask · 用自己的 Key 调用模型，查看自己的用量。</footer><Toast />
  </div>
}
