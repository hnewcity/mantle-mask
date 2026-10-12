import { ArrowRight, ArrowUpRight, CheckCircle2, Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { CopyButton, Empty, HealthBadge, LinkButton, LogsTable, PageTitle } from "@/components/common"
import { LiquidBackdrop } from "@/components/liquid-backdrop"
import { number, time } from "@/lib/utils"
import type { APIKey, Metrics, Upstream } from "@/lib/api"

function Traffic({ metrics }: { metrics: Metrics }) {
  const max = Math.max(4, ...metrics.timeline.map(p => p.requests))
  const points = metrics.timeline.map((p, i) => `${i / Math.max(1, metrics.timeline.length - 1) * 680},${130 - p.requests / max * 112}`).join(" ")
  return <div className="traffic-chart"><div className="chart-y"><span>{max}</span><span>{Math.round(max / 2)}</span><span>0</span></div><svg viewBox="0 0 680 150" role="img" aria-label={`最近 30 分钟流量，最高每分钟 ${max} 个请求`} preserveAspectRatio="none">{[18, 74, 130].map(y => <line key={y} x1="0" x2="680" y1={y} y2={y} stroke="#e5e5e5" strokeDasharray="3 5" />)}<polygon points={`0,130 ${points} 680,130`} fill="#000" fillOpacity=".035" /><polyline points={points} fill="none" stroke="#181818" strokeWidth="1.8" vectorEffect="non-scaling-stroke" /></svg><div className="chart-x">{[0, 7, 14, 21, 29].map(i => <span key={i}>{metrics.timeline[i] ? time(metrics.timeline[i].time).slice(0, 5) : "—"}</span>)}</div></div>
}

export function Overview({ upstreams, keys, metrics }: { upstreams: Upstream[]; keys: APIKey[]; metrics: Metrics }) {
  const enabled = upstreams.filter(u => u.enabled).length
  const success = metrics.requests ? `${((metrics.requests - metrics.errors) / metrics.requests * 100).toFixed(1)}%` : "—"
  const baseURL = `${location.origin}/v1`
  return <><PageTitle eyebrow="01 / GATEWAY OVERVIEW" title="网关总览" description="连接、观察、掌控。你的模型基础设施，尽在此处。" action={<Button asChild><a href="#/upstreams"><Plus size={16} />添加上游</a></Button>} />
    <section className="overview-hero" aria-label="Mantle Mask 统一模型入口">
      <LiquidBackdrop />
      <div className="hero-topline"><span>ONE API. EVERY ENDPOINT.</span><span>AMAZON BEDROCK GATEWAY</span></div>
      <h2>All connected.</h2>
      <div className="hero-bottomline"><p>多个端点。一个入口。无限可能。</p><a href="#/playground">打开 Playground<span><ArrowUpRight size={21} strokeWidth={1.3} /></span></a></div>
    </section>
    <div className="endpoint-strip"><span className="endpoint-label"><span className="small-cross" aria-hidden="true">+</span>你的统一入口 <span>BASE URL</span></span><code>{baseURL}</code><CopyButton value={baseURL} label="复制地址" /></div>
    <div className="stats-grid">{[
      { title: "请求总数", value: number(metrics.requests), label: "本次启动以来", en: "TOTAL REQUESTS" },
      { title: "请求成功率", value: success, label: metrics.requests ? `${number(metrics.errors)} 个失败请求` : "等待首次调用", en: "SUCCESS RATE" },
      { title: "平均响应耗时", value: metrics.requests ? number(metrics.avg_latency_ms) : "—", unit: metrics.requests ? "ms" : "", label: "完整请求耗时 · 包含流式输出", en: "AVG. LATENCY" },
      { title: "Token 用量", value: number(metrics.tokens), label: "本次启动以来 · 历史用量见统计页", en: "TOKENS PROCESSED" },
    ].map(item => <div className="stat-card" key={item.title}><div className="stat-heading"><span>{item.title}</span><span className="stat-english">{item.en}</span></div><div className="stat-value">{item.value}<small>{item.unit}</small></div><div className="stat-label"><span>{item.label}</span></div></div>)}</div>
    {(!upstreams.length || !keys.length) && <div className="setup-strip"><div><span className="setup-tag">QUICK START</span><strong>只需三步，让网关开始工作</strong></div><a href="#/upstreams" className={upstreams.length ? "done" : ""}><span>{upstreams.length ? <CheckCircle2 size={15} /> : "1"}</span>添加上游<ArrowRight size={14} /></a><a href="#/keys" className={keys.length ? "done" : ""}><span>{keys.length ? <CheckCircle2 size={15} /> : "2"}</span>创建 API 密钥<ArrowRight size={14} /></a><a href="#/playground"><span>3</span>发起测试请求<ArrowUpRight size={14} /></a></div>}
    <div className="overview-middle"><section className="panel traffic-panel"><div className="panel-heading"><div><h2>请求流量 <span className="section-english">TRAFFIC</span></h2><p>最近 30 分钟 · 每分钟请求数</p></div><span className="live-label"><i />实时</span></div><Traffic metrics={metrics} /><div className="chart-legend"><span><i />请求数</span><span>基于内存中保留的请求记录</span></div></section><section className="panel upstream-panel"><div className="panel-heading"><div><h2>上游状态 <span className="count-label">{upstreams.length}</span></h2><p>{enabled} 个已启用 · 加权轮询</p></div><LinkButton href="#/upstreams">管理</LinkButton></div>{upstreams.length ? <div className="health-list">{upstreams.slice(0, 4).map((u, index) => <div key={u.id}><span className="endpoint-index">{String(index + 1).padStart(2, "0")}</span><div><strong>{u.name}</strong><p>{u.region || new URL(u.base_url).hostname}</p></div><HealthBadge upstream={u} health={metrics.health[u.id]} /></div>)}</div> : <Empty title="还没有连接上游" description="添加你的第一个 Bedrock Mantle 端点" action={<Button size="sm" variant="outline" asChild><a href="#/upstreams"><Plus size={14} />添加端点</a></Button>} />}</section></div>
    <section className="panel"><div className="panel-heading"><div><h2>最近请求</h2><p>跟踪流经网关的每一次调用</p></div><LinkButton href="#/logs">查看全部</LinkButton></div><LogsTable logs={metrics.recent} compact /></section>
    <div className="overview-bottom"><span>BUILT FOR YOUR NEXT POSSIBILITY.</span><LinkButton href="#/usage">查看历史用量</LinkButton></div>
  </>
}
