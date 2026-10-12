import { useState } from "react"
import { BarChart3, KeyRound, Loader2, Pencil, Plus, Power, UsersRound } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Confirm, Empty, Field, notify, PageTitle } from "@/components/common"
import { api, message, type APIKey, type User, type UsageReport } from "@/lib/api"
import { number } from "@/lib/utils"

export function Users({ users, keys, usage, reload }: { users: User[]; keys: APIKey[]; usage: UsageReport | null; reload: () => Promise<void> }) {
  const [editing, setEditing] = useState<User | "new" | null>(null)
  const [name, setName] = useState(""), [note, setNote] = useState(""), [busy, setBusy] = useState(false)
  function edit(user: User | "new") {
    setName(user === "new" ? "" : user.name); setNote(user === "new" ? "" : user.note); setEditing(user)
  }
  const enabled = users.filter(u => u.enabled).length
  return <>
    <PageTitle eyebrow="TEAM ACCESS" title="用户管理" description="为每个人分配独立密钥，按用户汇总每一次模型调用。" action={<Button onClick={() => edit("new")}><Plus />创建用户</Button>} />
    <div className="info-strip"><UsersRound size={20} /><div><strong>{enabled} 位用户已启用 · {keys.filter(k => !k.revoked_at).length} 个有效 Key</strong><span>用户使用自己的 API Key 进入用量页，无需账号密码。停用用户后，其所有 Key 会停止接受新请求。</span></div></div>
    <section className="panel">
      <div className="panel-heading"><div><h2>用户 <span className="count-label">{users.length}</span></h2><p>用量为历史累计，撤销密钥和停用用户都会保留统计</p></div></div>
      {users.length ? <div className="table-wrap"><table className="users-table"><thead><tr><th>用户</th><th>状态</th><th>有效密钥</th><th>累计 Token / 请求</th><th className="text-right">操作</th></tr></thead><tbody>
        {users.map(user => {
          const totals = usage?.by_user.find(g => g.id === user.id)
          return <tr key={user.id}>
            <td><div className="person-cell"><span className="person-avatar">{user.name.slice(0, 1)}</span><div><strong>{user.name}</strong><small title={user.note}>{user.note || "暂无备注"}</small></div></div></td>
            <td><span className={`status ${user.enabled ? "green" : "neutral"}`}><i />{user.enabled ? "已启用" : "已停用"}</span></td>
            <td>{keys.filter(k => k.user_id === user.id && !k.revoked_at).length}</td>
            <td><span className="mono">{usage ? number(totals?.total_tokens || 0) : "—"}</span><small>{usage ? `${number(totals?.requests || 0)} 次请求` : "暂时无法读取"}{totals?.missing_usage_requests ? ` · ${totals.missing_usage_requests} 次未返回用量` : ""}</small></td>
            <td><div className="row-actions">
              <Button variant="ghost" size="sm" asChild><a href={`#/usage?user_id=${encodeURIComponent(user.id)}`}><BarChart3 size={14} />用量</a></Button>
              <Button variant="ghost" size="sm" disabled={!user.enabled} asChild={user.enabled}>{user.enabled ? <a href={`#/keys?user_id=${encodeURIComponent(user.id)}`}><KeyRound size={14} />分配 Key</a> : <span><KeyRound size={14} />分配 Key</span>}</Button>
              <Button variant="ghost" size="icon" aria-label={`编辑 ${user.name}`} onClick={() => edit(user)}><Pencil size={14} /></Button>
              <Confirm title={`${user.enabled ? "停用" : "启用"}「${user.name}」？`} description={user.enabled ? "该用户的全部 API Key 将无法发起新请求或查询用量；历史记录保留，之后可重新启用。" : "该用户未撤销的 API Key 将恢复访问。"} trigger={<Button variant="ghost" size="icon" aria-label={`${user.enabled ? "停用" : "启用"} ${user.name}`}><Power size={14} /></Button>} onConfirm={async () => {
                try { await api(`/users/${user.id}`, { method: "PUT", body: JSON.stringify({ name: user.name, note: user.note, enabled: !user.enabled }) }); await reload(); notify(user.enabled ? "用户已停用" : "用户已启用") }
                catch (e) { notify(message(e), true) }
              }} />
            </div></td>
          </tr>
        })}
      </tbody></table></div> : <Empty title="创建第一个用户" description="按人或团队组织密钥与用量。" />}
    </section>
    <p className="usage-footnote">“默认用户”接收升级前的 Key；新 Key 的归属在创建时确定。管理员 Playground 的调用单独计入“管理员”。</p>
    <Dialog open={editing !== null} onOpenChange={v => { if (!v && !busy) setEditing(null) }}><DialogContent>
      <DialogHeader><DialogTitle>{editing === "new" ? "创建用户" : "编辑用户"}</DialogTitle><DialogDescription>用户以 API Key 访问网关。名称和备注仅用于辨认与统计。</DialogDescription></DialogHeader>
      <form onSubmit={async e => {
        e.preventDefault(); setBusy(true)
        try {
          await api(editing === "new" ? "/users" : `/users/${editing?.id}`, { method: editing === "new" ? "POST" : "PUT", body: JSON.stringify({ name, note, enabled: editing === "new" ? true : editing?.enabled }) })
          setEditing(null); await reload(); notify("用户已保存")
        } catch (err) { notify(message(err), true) } finally { setBusy(false) }
      }}>
        <Field label="用户名称"><Input autoFocus value={name} onChange={e => setName(e.target.value)} maxLength={100} required placeholder="例如：设计团队 / 小陈" /></Field>
        <Field label="备注"><Input value={note} onChange={e => setNote(e.target.value)} maxLength={500} placeholder="可选，例如团队或用途" /></Field>
        <DialogFooter><Button variant="outline" type="button" onClick={() => setEditing(null)}>取消</Button><Button disabled={busy || !name.trim()}>{busy && <Loader2 className="animate-spin" />}保存用户</Button></DialogFooter>
      </form>
    </DialogContent></Dialog>
  </>
}
