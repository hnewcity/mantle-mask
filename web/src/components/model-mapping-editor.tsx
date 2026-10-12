import { useId, useRef } from "react"
import { ArrowRight, Plus, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { ModelMapping, ModelMappingError } from "@/lib/model-mappings"

export function ModelMappingEditor({ mode, rows, errors, onModeChange, onChange, onAdd, onRemove }: {
  mode: "all" | "mapped"
  rows: ModelMapping[]
  errors: ModelMappingError[]
  onModeChange: (mode: "all" | "mapped") => void
  onChange: (id: number, field: "alias" | "model", value: string) => void
  onAdd: () => void
  onRemove: (id: number) => void
}) {
  const id = useId()
  const addButton = useRef<HTMLButtonElement>(null)
  return <fieldset className="mapping-editor">
    <legend>模型访问</legend>
    <p className="mapping-description">选择应用可以通过这个端点调用哪些模型。</p>
    <div className="mapping-modes">
      <label className={`mapping-mode ${mode === "all" ? "selected" : ""}`}>
        <input type="radio" name={`${id}-mode`} checked={mode === "all"} onChange={() => onModeChange("all")} />
        <span><strong>全部透传</strong><small>直接使用上游模型 ID，无需映射</small></span>
      </label>
      <label className={`mapping-mode ${mode === "mapped" ? "selected" : ""}`}>
        <input type="radio" name={`${id}-mode`} checked={mode === "mapped"} onChange={() => onModeChange("mapped")} />
        <span><strong>指定模型</strong><small>设置对外名称，只开放下方模型</small></span>
      </label>
    </div>
    {mode === "mapped" && <div className="mapping-list">
      {rows.map((row, index) => {
        const aliasID = `${id}-${row.id}-alias`, modelID = `${id}-${row.id}-model`
        const error = errors[index]
        return <div className="mapping-row" role="group" aria-label={`模型映射 ${index + 1}`} key={row.id}>
          <div className="mapping-row-header"><span>模型 {String(index + 1).padStart(2, "0")}</span><Button type="button" variant="ghost" size="icon" aria-label={`删除模型映射 ${index + 1}`} title="删除这条映射" onClick={() => { onRemove(row.id); addButton.current?.focus() }}><Trash2 size={14} /></Button></div>
          <div className="mapping-fields">
            <div className="mapping-field">
              <label htmlFor={aliasID}>对外名称</label>
              <Input id={aliasID} name={`mapping-alias-${row.id}`} value={row.alias} aria-label={`对外名称 ${index + 1}`} aria-required="true" aria-invalid={!!error?.alias} aria-describedby={error?.alias ? `${aliasID}-error` : undefined} placeholder="例如：my-model" onChange={event => onChange(row.id, "alias", event.target.value)} />
              {error?.alias && <p id={`${aliasID}-error`} className="mapping-error">{error.alias}</p>}
            </div>
            <ArrowRight className="mapping-arrow" size={16} aria-hidden="true" />
            <div className="mapping-field">
              <label htmlFor={modelID}>上游模型 ID</label>
              <Input id={modelID} name={`mapping-model-${row.id}`} value={row.model} aria-label={`上游模型 ID ${index + 1}`} aria-required="true" aria-invalid={!!error?.model} aria-describedby={error?.model ? `${modelID}-error` : undefined} placeholder="例如：openai.gpt-oss-120b" onChange={event => onChange(row.id, "model", event.target.value)} />
              {error?.model && <p id={`${modelID}-error`} className="mapping-error">{error.model}</p>}
            </div>
          </div>
          {row.alias.trim() && row.model.trim() && <p className="mapping-preview">应用调用 <code>{row.alias.trim()}</code><ArrowRight size={12} aria-hidden="true" />上游执行 <code>{row.model.trim()}</code></p>}
        </div>
      })}
      {!rows.length && <p className="mapping-empty">还没有指定模型。添加至少一个模型后即可保存。</p>}
      <Button ref={addButton} type="button" variant="outline" size="sm" onClick={onAdd}><Plus size={14} />添加模型</Button>
      <p className="mapping-note">对外名称可以与上游模型 ID 相同。未列出的模型将不会路由到这个端点。</p>
    </div>}
  </fieldset>
}
