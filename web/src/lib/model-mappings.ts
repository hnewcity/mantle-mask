export type ModelMapping = { id: number; alias: string; model: string }
export type ModelMappingError = { alias?: string; model?: string }

export function compileModelMappings(rows: ModelMapping[]): {
  models: Record<string, string> | null
  errors: ModelMappingError[]
  message: string
} {
  if (!rows.length) return { models: null, errors: [], message: "请至少添加一个模型，或选择全部透传。" }
  const aliases = new Map<string, number>()
  for (const row of rows) {
    const alias = row.alias.trim()
    aliases.set(alias, (aliases.get(alias) || 0) + 1)
  }
  const errors = rows.map(row => {
    const error: ModelMappingError = {}
    const alias = row.alias.trim()
    if (!alias) error.alias = "请输入应用调用时使用的名称"
    else if (aliases.get(alias)! > 1) error.alias = "对外名称重复，请使用不同的名称"
    if (!row.model.trim()) error.model = "请输入上游模型 ID"
    return error
  })
  if (errors.some(error => error.alias || error.model)) {
    return { models: null, errors, message: "请检查标记的模型配置。" }
  }
  return {
    models: Object.fromEntries(rows.map(row => [row.alias.trim(), row.model.trim()])),
    errors,
    message: "",
  }
}
