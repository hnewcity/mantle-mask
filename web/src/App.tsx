import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react"
import { ArrowRight, ArrowUpRight, CircleHelp, Loader2, LogOut, RefreshCw, ShieldCheck } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { api, getSession, message, session, type APIKey, type Metrics, type Settings, type Upstream, type User, type UsageReport, type SessionInfo } from "@/lib/api"
import { Brand, Spinner, Toast } from "@/components/common"
import { LiquidBackdrop } from "@/components/liquid-backdrop"
import { startPolling } from "@/lib/polling"
import { Overview } from "@/pages/Overview"
import { Upstreams } from "@/pages/Upstreams"
import { Keys } from "@/pages/Keys"
import { Users } from "@/pages/Users"
import { Logs } from "@/pages/Logs"

// Keep Markdown, date-picker and migration tooling off the login/overview path.
const Usage = lazy(() => import("@/pages/Usage").then(module => ({ default: module.Usage })))
const UserPortal = lazy(() => import("@/pages/UserPortal").then(module => ({ default: module.UserPortal })))
const Playground = lazy(() => import("@/pages/Playground").then(module => ({ default: module.Playground })))
const SettingsPage = lazy(() => import("@/pages/Settings").then(module => ({ default: module.SettingsPage })))

const pages = [
  { id: "overview", name: "总览", en: "Overview" },
  { id: "upstreams", name: "上游端点", en: "Upstreams" },
  { id: "users", name: "用户管理", en: "Users" },
  { id: "usage", name: "用量统计", en: "Usage" },
  { id: "keys", name: "API 密钥", en: "API keys" },
  { id: "playground", name: "在线测试", en: "Playground" },
  { id: "logs", name: "请求日志", en: "Request logs" },
  { id: "settings", name: "网关设置", en: "Settings" },
]
type Data = { upstreams: Upstream[]; keys: APIKey[]; allKeys: APIKey[]; metrics: Metrics; settings: Settings; users: User[]; usage: UsageReport | null; usageFailed: boolean }

function Login({ onLogin }: { onLogin: (identity: SessionInfo) => void }) {
  const [userMode, setUserMode] = useState(false)
  const [token, setToken] = useState(""), [busy, setBusy] = useState(false), [error, setError] = useState("")
  return <div className="login-page"><div className="login-art"><LiquidBackdrop /><Brand /><div className="login-intro"><span className="login-pill">INDEPENDENT INFRASTRUCTURE, CONNECTED.</span><h1>Many models.<br />One entrance.</h1><p>多个端点，一个统一的入口。<br />让每一种可能，保持连接。</p></div><div className="login-art-footer"><span>OPENAI COMPATIBLE<br />POWERED BY AMAZON BEDROCK</span><ArrowUpRight size={38} strokeWidth={1} /></div></div>
    <div className="login-form-wrap"><div className="login-form-top"><span>MANTLE MASK / CONSOLE</span><span>01 — ACCESS</span></div><div className="login-form"><p className="eyebrow">YOUR GATEWAY STARTS HERE</p><h2>{userMode ? "查看我的用量" : "登录管理控制台"}</h2><p className="muted">{userMode ? "输入分配给你的 API Key，查看调用与 Token 用量。" : "欢迎回来。连接你的模型，掌控每一次请求。"}</p><div className="login-mode"><button type="button" aria-pressed={!userMode} className={!userMode ? "active" : ""} onClick={() => { setUserMode(false); setError("") }}>管理员</button><button type="button" aria-pressed={userMode} className={userMode ? "active" : ""} onClick={() => { setUserMode(true); setError("") }}>用户用量查询</button></div>
      <form onSubmit={async event => { event.preventDefault(); setBusy(true); setError(""); session.set(token.trim()); try { const identity = await getSession(); onLogin(identity) } catch (e) { session.clear(); setError(message(e)) } finally { setBusy(false) } }}>
        <label className="login-label" htmlFor="admin-token">{userMode ? "API Key" : "管理员令牌"}</label><Input id="admin-token" autoComplete="current-password" type="password" placeholder={userMode ? "sk-mm-…" : "mm-admin-…"} value={token} onChange={e => setToken(e.target.value)} required />
        {error && <p className="form-error" role="alert">{error}</p>}<Button className="login-submit" disabled={busy || !token.trim()}>{busy ? <Loader2 className="animate-spin" /> : <>{userMode ? "查看用量" : "进入控制台"}<ArrowRight size={16} /></>}</Button>
      </form>
      {userMode && <div className="login-help"><CircleHelp size={17} /><div><strong>使用管理员分配的 Key</strong><p>你可以查看本人名下所有 Key 的用量。需要创建或撤销密钥时，请联系网关管理员。</p></div></div>}
    </div><p className="login-security"><ShieldCheck size={14} />令牌仅保存在当前浏览器会话中<span>v0.2.0</span></p></div></div>
}

export default function App() {
  const [authenticated, setAuthenticated] = useState<SessionInfo | null>(null), [checking, setChecking] = useState(true)
  const [page, setPage] = useState(location.hash.replace("#/", "") || "overview")
  const [data, setData] = useState<Data | null>(null), [error, setError] = useState(""), [refreshing, setRefreshing] = useState(false)
  const logout = useCallback(() => { session.clear(); setAuthenticated(null); setData(null) }, [])
  useEffect(() => { window.scrollTo(0, 0) }, [page])
  useEffect(() => {
    const handleHash = () => setPage(location.hash.replace("#/", "") || "overview")
    window.addEventListener("hashchange", handleHash)
    window.addEventListener("mantle:unauthorized", logout)
    if (session.get()) getSession().then(setAuthenticated).catch(() => logout()).finally(() => setChecking(false))
    else setChecking(false)
    return () => { window.removeEventListener("hashchange", handleHash); window.removeEventListener("mantle:unauthorized", logout) }
  }, [logout])
  const poller = useRef<ReturnType<typeof startPolling> | null>(null)
  const needsUsage = page.split("?")[0] === "users"
  const reload = useCallback(async () => { await poller.current?.refresh() }, [])
  useEffect(() => {
    if (authenticated?.role !== "admin") return
    const polling = startPolling(async signal => {
      try {
        const options = { signal }
        const [upstreams, keys, metrics, settings, users, usage] = await Promise.all([
          api<{ data: Upstream[] }>("/upstreams", options), api<{ data: APIKey[] }>("/keys?include_revoked=true", options),
          api<Metrics>("/overview", options), api<Settings>("/settings", options), api<{ data: User[] }>("/users", options),
          needsUsage ? api<UsageReport>("/usage?all=true", options).catch(() => null) : Promise.resolve(null),
        ])
        if (!signal.aborted) {
          setData({ upstreams: upstreams.data, keys: keys.data.filter(k => !k.revoked_at), allKeys: keys.data, metrics, settings, users: users.data, usage, usageFailed: needsUsage && !usage })
          setError("")
        }
      } catch (e) { if (!signal.aborted) setError(message(e)) }
    })
    poller.current = polling
    return () => { polling.stop(); poller.current = null }
  }, [authenticated, needsUsage])
  if (checking) return <Spinner label="正在连接网关…" />
  if (!authenticated) return <><Login onLogin={setAuthenticated} /><Toast /></>
  if (authenticated.role === "user") return <Suspense fallback={<Spinner />}><UserPortal identity={authenticated} logout={logout} /></Suspense>
  const current = pages.find(p => p.id === page.split("?")[0]) || pages[0]
  const enabled = data?.upstreams.filter(u => u.enabled).length || 0
  return <div className="app-shell">
    <a className="skip-link" href="#main-content" onClick={event => { event.preventDefault(); document.getElementById("main-content")?.focus() }}>跳转到内容</a>
    <header className="console-header"><div className="topbar"><a href="#/overview" aria-label="Mantle Mask 首页"><Brand /></a><div className="workspace-label"><span>CONTROL ROOM</span><span>默认工作空间 <ArrowUpRight size={12} /></span></div><div className="topbar-actions"><span className={`connection-label ${error ? "offline" : ""}`}><i />{error ? "连接中断" : data ? "网关运行中" : "正在连接"}</span><span className="topbar-divider" /><Button variant="ghost" size="icon" aria-label="刷新数据" title="刷新数据" disabled={refreshing} onClick={async () => { setRefreshing(true); await reload(); setRefreshing(false) }}><RefreshCw size={15} className={refreshing ? "animate-spin" : ""} /></Button><Button variant="ghost" size="icon" aria-label="退出登录" title="退出登录" onClick={logout}><LogOut size={15} /></Button><span className="user-avatar" aria-label="管理员">M</span></div></div>
      <div className="nav-wrap"><nav className="console-nav" aria-label="管理端导航">{pages.map((item, index) => <a key={item.id} href={`#/${item.id}`} aria-label={item.name} aria-current={current.id === item.id ? "page" : undefined} className={`nav-item ${current.id === item.id ? "active" : ""}`}><span className="nav-index" aria-hidden="true">{String(index + 1).padStart(2, "0")}</span><span>{item.name}</span><span className="nav-english" aria-hidden="true">{item.en}</span></a>)}</nav><span className="nav-edition">SELF-HOSTED / V.02</span></div>
    </header>
    <div className="app-main">
      <main className="content" id="main-content" tabIndex={-1}>{error && <div className="error-banner" role="alert">{error}<Button size="sm" variant="outline" onClick={() => void reload()}>重试</Button></div>}{!data ? <Spinner /> : <Suspense fallback={<Spinner />}>
        {!data.metrics.usage_storage_healthy && <div className="error-banner" role="alert">用量存储发生错误，请检查磁盘空间和数据目录权限。存储无法写入时，新推理请求会被阻止。</div>}
        {current.id === "users" && data.usageFailed && <div className="error-banner" role="alert">历史用量暂时无法读取，请检查服务日志后刷新。</div>}
        {current.id === "overview" && <Overview {...data} />}
        {current.id === "upstreams" && <Upstreams upstreams={data.upstreams} health={data.metrics.health} reload={reload} />}
        {current.id === "users" && <Users users={data.users} keys={data.allKeys} usage={data.usage} reload={reload} />}
        {current.id === "usage" && <Usage key={page} users={data.users} keys={data.allKeys} upstreams={data.upstreams} />}
        {current.id === "keys" && <Keys key={page} keys={data.keys} users={data.users} reload={reload} />}
        {current.id === "playground" && <Playground upstreams={data.upstreams} onComplete={reload} />}
        {current.id === "logs" && <Logs />}
        {current.id === "settings" && <SettingsPage settings={data.settings} reload={reload} />}
      </Suspense>}</main><footer className="footer"><span>MANTLE MASK · YOUR MODELS. YOUR INFRASTRUCTURE.</span><span><i className="footer-dot" />{enabled} 个上游已启用 <span>/</span> OpenAI compatible <span>/</span> v0.2.0</span></footer>
    </div><Toast /></div>
}
