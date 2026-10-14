import { useEffect, useRef, useState } from "react"
import { Download, HardDrive, Loader2, Upload } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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
  const [confirmation, setConfirmation] = useState("")
  const [error, setError] = useState("")
  const [result, setResult] = useState("")
  const fileInput = useRef<HTMLInputElement>(null)
  useEffect(() => { onBusyChange(busy !== null) }, [busy, onBusyChange])
  const blocked = disabled || busy !== null
  const validPassword = (value: string) => [...value].length >= 12 && new TextEncoder().encode(value).length <= 1024

  return <section className="panel settings-section" aria-labelledby="backup-title">
    <div className="settings-section-title"><span className="section-icon"><HardDrive size={21} /></span><div><h2 id="backup-title">备份与迁移</h2><p>导出完整备份，在新实例恢复，无需重新创建 API Key 或配置上游</p></div></div>
    <div className="backup-notes">
      <p><strong>完整保留：</strong>全部用户、API Key 及吊销状态、上游与已保存凭据、模型映射、网关设置、全部历史用量及 Responses 路由绑定。原有 API Key 恢复后仍可使用。</p>
      <p><strong>不覆盖：</strong>目标实例的管理员登录口令和主密钥；不包含部署环境变量、外部 AWS Profile / IAM 角色或内存请求日志。依赖外部 AWS 凭据的上游仍需在新环境配置对应身份。</p>
      <p>备份使用密码加密，忘记密码无法恢复。请妥善保管文件和密码，并通过 HTTPS 或可信本机连接操作。迁移前请暂停 API 流量，等待所有流式请求结束。</p>
    </div>
    <div className="backup-grid">
      <form className="backup-block" onSubmit={async event => {
        event.preventDefault()
        if (blocked || !validPassword(password) || password !== repeat) return
        setBusy("export"); setError(""); setResult("")
        try {
          await exportBackup(password)
          setPassword(""); setRepeat("")
          setResult("已生成加密备份，请确认浏览器已保存下载文件，并妥善保管备份密码。")
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
        <p className="backup-warning">恢复将覆盖当前全部配置、Key 和用量，不会合并。建议先导出当前实例的备份。</p>
        <Button type="button" variant="destructive" disabled={blocked || !file || !validPassword(restorePassword)} onClick={() => { setConfirmation(""); setConfirm(true) }}><Upload />恢复备份</Button>
      </div>
    </div>
    {error && <div className="error-banner" role="alert">{error}</div>}
    {result && <p className="backup-result" role="status">{result}</p>}
    <Dialog open={confirm} onOpenChange={open => { if (!busy) setConfirm(open) }}><DialogContent className="sm:max-w-md">
      <DialogHeader><DialogTitle>确认覆盖并恢复？</DialogTitle><DialogDescription>将用「{file?.name}」替换当前全部用户、API Key、上游、设置和历史用量。当前管理员登录口令保持不变。请确认已备份现有数据并暂停 API 流量。</DialogDescription></DialogHeader>
      <Field label="输入 RESTORE 确认覆盖"><Input autoComplete="off" disabled={blocked} value={confirmation} onChange={e => setConfirmation(e.target.value)} /></Field>
      <DialogFooter><Button variant="outline" disabled={blocked} onClick={() => setConfirm(false)}>取消</Button><Button variant="destructive" disabled={blocked || confirmation !== "RESTORE"} onClick={async () => {
        if (!file) return
        setBusy("restore"); setError(""); setResult("")
        try {
          const restored = await restoreBackup(file, restorePassword)
          setRestorePassword(""); setFile(null)
          if (fileInput.current) fileInput.current.value = ""
          setResult(`恢复成功：${restored.users} 个用户、${restored.api_keys} 个 API Key、${restored.upstreams} 个上游、${restored.usage_requests} 条用量记录。`)
          notify("备份恢复成功，原有 API Key 和上游凭据已保留")
          await onRestored()
        } catch (e) { setError(message(e)) } finally { setBusy(null); setConfirm(false) }
      }}>{busy === "restore" && <Loader2 className="animate-spin" />}覆盖并恢复</Button></DialogFooter>
    </DialogContent></Dialog>
  </section>
}
