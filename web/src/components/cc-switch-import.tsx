import { useEffect, useState } from "react"
import { ArrowUpRight } from "lucide-react"
import { Field, notify } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { SearchSelect } from "@/components/ui/search-select"
import { Select } from "@/components/ui/select"
import { buildCCSwitchLink, type CCSwitchApp } from "@/lib/cc-switch"
import type { Model } from "@/lib/api"

export function CCSwitchImport({ apiKey, keyName }: { apiKey: string; keyName: string }) {
  const [app, setApp] = useState<CCSwitchApp>("codex")
  const [name, setName] = useState(`Mantle Mask · ${keyName}`)
  const [model, setModel] = useState(""), [models, setModels] = useState<Model[]>([])
  const [modelHint, setModelHint] = useState("正在读取网关模型，也可直接输入模型 ID。")
  const endpoint = `${location.origin}/v1`
  useEffect(() => {
    const controller = new AbortController()
    async function loadModels() {
      try {
        // Use the key being exported, including immediately after admin creation.
        const response = await fetch("/v1/models", {
          headers: { Authorization: `Bearer ${apiKey}` },
          signal: controller.signal, cache: "no-store", credentials: "omit",
        })
        if (!response.ok) throw new Error("Model discovery failed")
        const result: { data: Model[] } = await response.json()
        if (!Array.isArray(result.data)) throw new Error("Invalid model list")
        if (controller.signal.aborted) return
        setModels(result.data)
        setModel(current => current || result.data[0]?.id || "")
        setModelHint(result.data.length ? "选择网关模型，或输入完整模型 ID / 对外别名。" : "暂无可用模型，请填写模型 ID / 对外别名。")
      } catch {
        if (!controller.signal.aborted) setModelHint("无法读取模型列表，请手动填写模型 ID / 对外别名。")
      }
    }
    void loadModels()
    return () => controller.abort()
  }, [apiKey])
  const ready = !!apiKey.trim() && !!name.trim() && !!model.trim()
  function link() { return buildCCSwitchLink({ app, name, endpoint, apiKey, model }) }
  return <section className="cc-switch-import" aria-label="CC Switch 接入配置">
    <div className="cc-switch-heading"><strong>CC Switch 一键接入</strong><code>{endpoint}</code></div>
    <Field label="目标客户端"><Select aria-label="目标客户端" value={app} onValueChange={value => setApp(value as CCSwitchApp)} options={[{ value: "codex", label: "Codex" }, { value: "claude", label: "Claude Code" }]} /></Field>
    <Field label="供应商名称"><Input value={name} onChange={event => setName(event.target.value)} maxLength={160} /></Field>
    <Field label="接入模型" hint={modelHint}><SearchSelect aria-label="接入模型" value={model} onValueChange={setModel} options={models.map(item => ({ value: item.id, label: item.id }))} placeholder="选择或输入模型 ID" searchPlaceholder="搜索或输入模型 ID…" allowCustom /></Field>
    {app === "claude" && <p className="cc-switch-hint">Claude Code 需要 CC Switch v4.0.10 或更新版本，并在 CC Switch 中开启本地路由，将请求转换为 OpenAI Chat Completions。三个模型档位均使用所选模型。</p>}
    <div className="cc-switch-actions"><Button type="button" disabled={!ready} onClick={() => {
      // Generate only on click; never persist the credential-bearing URI.
      const anchor = document.createElement("a")
      anchor.href = link()
      anchor.click()
    }}><ArrowUpRight />一键导入 CC Switch</Button><Button type="button" variant="ghost" size="sm" disabled={!ready} onClick={async () => {
      try { await navigator.clipboard.writeText(link()); notify("导入链接已复制，其中包含完整密钥，请妥善保管") }
      catch { notify("复制失败，请使用一键导入按钮", true) }
    }}>复制导入链接</Button></div>
    <p className="cc-switch-hint">浏览器会请求打开 CC Switch，请在应用中确认导入。没有响应？先<a className="text-link" href="https://github.com/farion1231/cc-switch/releases" target="_blank" rel="noopener noreferrer">安装 CC Switch</a>，再重试。导入链接包含当前完整密钥，请勿公开分享。</p>
  </section>
}
