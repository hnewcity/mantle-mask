import { useEffect, useRef, useState } from "react"
import { Download, HardDrive, Loader2, Upload } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, notify } from "@/components/common"
import { exportBackup, message, restoreBackup } from "@/lib/api"

export function BackupSettings({ onRestored, onBusyChange, disabled = false }: { onRestored: () => Promise<void>; onBusyChange: (busy: boolean) => void; disabled?: boolean }) {
  const [password, setPassword] = useState("")
  const [repeat, setRepeat] = useState("")
  const [restorePassword, setRestorePassword] = useState("")
  const [file, setFile] = useState<File | null>(null)
  const [busy, setBusy] = useState<"export" | "restore" | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [usageOnly, setUsageOnly] = useState(false)
  const [confirmation, setConfirmation] = useState("")
  const [error, setError] = useState("")
  const [result, setResult] = useState("")
  const fileInput = useRef<HTMLInputElement>(null)
  useEffect(() => { onBusyChange(busy !== null) }, [busy, onBusyChange])
  const blocked = disabled || busy !== null
  const validPassword = (value: string) => [...value].length >= 12 && new TextEncoder().encode(value).length <= 1024

  return <section className="panel settings-section" aria-labelledby="backup-title">
    <div className="settings-section-title"><span className="section-icon"><HardDrive size={21} /></span><div><h2 id="backup-title">备份与迁移</h2><p>在线导出与恢复，不用停止 API 流量；支持切换后的用量补齐</p></div></div>
    <div className="backup-notes">
      <p><strong>完整保留：</strong>全部用户、API Key 及吊销状态、上游与已保存凭据、模型映射、网关设置、全部历史用量及 Responses 路由绑定。原有 API Key 恢复后仍可使用。</p>
      <p><strong>不覆盖：</strong>目标实例的管理员登录口令和主密钥；不包含部署环境变量、外部 AWS Profile / IAM 角色或内存请求日志。依赖外部 AWS 凭据的上游仍需在新环境配置对应身份。</p>
      <p>备份使用密码加密，忘记密码无法恢复。请妥善保管文件和密码，并通过 HTTPS 或可信本机连接操作。导出不会中断正在执行的请求或 SSE。备份记录快照时刻的数据，不包含快照之后才产生的用量。</p>
    <p><strong>无停机迁移：</strong>先在线导出并恢复到新实例，再切换客户端流量；旧实例的请求自然完成后，再导出一次并选择「仅补齐用量和路由」。按请求 ID 合并，重复导入不会重复计数，也不会覆盖新实例的配置。</p>
    </div>
    <div className="backup-grid">
      <form className="backup-block" onSubmit={async event => {
        event.preventDefault()
        if (blocked || !validPassword(password) || password !== repeat) return
        setBusy("export"); setError(""); setResult("")
        try {
          await exportBackup(password)
          setPassword(""); setRepeat("")
          setResult("已生成加密在线快照，业务流量未停止。切换流量后，可再次导出并补齐快照之后的用量。")
        } catch (e) { setError(message(e)) } finally { setBusy(null) }
      }}>
        <h3>导出完整备份</h3>
        <Field label="备份密码" hint="至少 12 个字符；请使用独立的强密码"><Input type="password" autoComplete="new-password" required disabled={blocked} value={password} onChange={e => setPassword(e.target.value)} /></Field>
        <Field label="再次输入备份密码"><Input type="password" autoComplete="new-password" required disabled={blocked} value={repeat} onChange={e => setRepeat(e.target.value)} /></Field>
        {repeat && password !== repeat && <p role="alert">两次输入的密码不一致</p>}
        <Button disabled={blocked || !validPassword(password) || password !== repeat}>{busy === "export" ? <Loader2 className="animate-spin" /> : <Download />}导出加密备份</Button>
      </form>
      <div className="backup-block">
        <h3>恢复完整备份</h3>
        <Field label="备份文件" hint="选择 .mmbak 文件，最大 256 MiB"><Input ref={fileInput} type="file" accept=".mmbak" disabled={blocked} onChange={e => {
          const selected = e.target.files?.[0] || null
          setError(""); setResult("")
          if (selected && selected.size > 256 * 1024 * 1024) { setError("备份文件不能超过 256 MiB"); setFile(null); e.target.value = ""; return }
          setFile(selected)
        }} /></Field>
        <Field label="解密密码"><Input type="password" autoComplete="off" disabled={blocked} value={restorePassword} onChange={e => setRestorePassword(e.target.value)} /></Field>
        <div className="toggle-row"><div><strong>仅补齐用量和路由</strong><p>保留当前用户、Key、上游和设置，只合并旧实例的收尾数据。</p></div><Switch aria-label="仅补齐用量和路由" disabled={blocked} checked={usageOnly} onCheckedChange={setUsageOnly} /></div>
        <p className="backup-warning">{usageOnly ? "按请求 ID 去重补齐，不累加重复记录，不删除目标已有用量。" : "恢复会覆盖用户、Key、上游和设置；用量按请求 ID 合并，保留目标已有数据和正在执行的请求。建议先备份当前配置。"}</p>
        <Button type="button" variant="destructive" disabled={blocked || !file || !validPassword(restorePassword)} onClick={() => { setConfirmation(""); setConfirm(true) }}><Upload />恢复备份</Button>
      </div>
    </div>
    {error && <div className="error-banner" role="alert">{error}</div>}
    {result && <p className="backup-result" role="status">{result}</p>}
    <Dialog open={confirm} onOpenChange={open => { if (!busy) setConfirm(open) }}><DialogContent className="sm:max-w-md">
      <DialogHeader><DialogTitle>{usageOnly ? "确认补齐用量？" : "确认恢复配置并合并用量？"}</DialogTitle><DialogDescription>将读取「{file?.name}」。{usageOnly ? "只合并用量与缺失的 Responses 路由，不修改当前配置。" : "替换当前用户、API Key、上游和设置；已有用量保留，按请求 ID 合并。"}无需暂停流量，已接受的请求按原配置完成，新请求使用恢复后的配置。当前管理员登录口令保持不变。</DialogDescription></DialogHeader>
      <Field label="输入 RESTORE 确认操作"><Input autoComplete="off" disabled={blocked} value={confirmation} onChange={e => setConfirmation(e.target.value)} /></Field>
      <DialogFooter><Button variant="outline" disabled={blocked} onClick={() => setConfirm(false)}>取消</Button><Button variant="destructive" disabled={blocked || confirmation !== "RESTORE"} onClick={async () => {
        if (!file) return
        setBusy("restore"); setError(""); setResult("")
        try {
          const restored = await restoreBackup(file, restorePassword, usageOnly)
          setRestorePassword(""); setFile(null)
          if (fileInput.current) fileInput.current.value = ""
          setResult(restored.usage_only ? `补齐成功：已按请求 ID 合并 ${restored.usage_requests} 条用量记录，当前配置未改动。` : `恢复成功：${restored.users} 个用户、${restored.api_keys} 个 API Key、${restored.upstreams} 个上游；合并 ${restored.usage_requests} 条用量记录，目标已有用量保留。`)
          notify(restored.usage_only ? "用量补齐成功，重复请求不会重复计数" : "在线恢复成功，已有请求继续处理")
          await onRestored()
        } catch (e) { setError(message(e)) } finally { setBusy(null); setConfirm(false) }
      }}>{busy === "restore" && <Loader2 className="animate-spin" />}确认恢复</Button></DialogFooter>
    </DialogContent></Dialog>
  </section>
}
