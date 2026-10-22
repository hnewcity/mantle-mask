# 通过命令更新上游 API Key

临时 Bedrock API Key 可以通过管理接口在线轮换，无需重启网关：

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/admin/upstreams` | 查询上游 ID、名称和鉴权模式，不返回凭据 |
| PUT | `/api/admin/upstreams/{id}/api-key` | 只更新指定上游的 API Key，请求体为 `{"api_key":"新 key"}` |

两个接口都使用 `Authorization: Bearer <管理员令牌>`，即 `MANTLE_ADMIN_TOKEN` 或首次启动生成的 `data/admin-token` 内容。普通用户的网关 Key 无权调用。

## Bash / curl

需要 `curl` 和 `jq`。先设置管理员令牌，列出上游并选取 `auth_mode` 为 `api_key` 的 ID：

```bash
export MANTLE_URL="http://127.0.0.1:8080"
read -rsp '管理员令牌: ' MANTLE_ADMIN_TOKEN; printf '\n'
export MANTLE_ADMIN_TOKEN
curl --fail-with-body -sS "$MANTLE_URL/api/admin/upstreams" \
  -H "Authorization: Bearer $MANTLE_ADMIN_TOKEN" \
  | jq '.data[] | {id, name, auth_mode}'

export UPSTREAM_ID="up_替换为实际ID"
read -rsp '新的上游 API Key: ' UPSTREAM_API_KEY; printf '\n'
export UPSTREAM_API_KEY
jq -n '{api_key: env.UPSTREAM_API_KEY}' \
  | curl --fail-with-body -sS -X PUT \
      "$MANTLE_URL/api/admin/upstreams/$UPSTREAM_ID/api-key" \
      -H "Authorization: Bearer $MANTLE_ADMIN_TOKEN" \
      -H 'Content-Type: application/json' --data-binary @-
unset UPSTREAM_API_KEY
```

自动刷新脚本取得新 key 后，设置 `UPSTREAM_API_KEY` 环境变量并执行上面的 `jq | curl` 命令即可。新 key 通过 JSON 请求体传入，不放在 URL 中；`curl --fail-with-body` 会在 HTTP 错误时返回非零退出码。

## PowerShell

先在当前会话或调用脚本中设置 `$env:MANTLE_ADMIN_TOKEN` 和 `$env:UPSTREAM_API_KEY`，然后执行：

```powershell
$mantleUrl = "http://127.0.0.1:8080"
$headers = @{ Authorization = "Bearer $env:MANTLE_ADMIN_TOKEN" }
(Invoke-RestMethod -Uri "$mantleUrl/api/admin/upstreams" -Headers $headers).data |
  Select-Object id, name, auth_mode

$upstreamId = "up_替换为实际ID"
$body = @{ api_key = $env:UPSTREAM_API_KEY } | ConvertTo-Json -Compress
Invoke-RestMethod -Method Put -Uri "$mantleUrl/api/admin/upstreams/$upstreamId/api-key" `
  -Headers $headers -ContentType "application/json" -Body $body
```

成功返回 `{"id":"up_...","updated":true}`，不回显 key。接口只接受 `api_key` 字段，去除首尾空白后须为非空、无内部空白或控制字符的 ASCII token。名称、地址、权重、启用状态和模型映射保持不变；停用上游不会因换 key 而启用。

新 key 加密持久化后，新请求立即使用它；已经开始的请求和 SSE 流继续使用原凭据。旧凭据的模型缓存及冷却状态不会阻止新凭据生效，Responses 绑定保留（新 key 仍应属于原账户）。成功表示保存完成，不代表已验证上游权限；可调用 `POST /api/admin/upstreams/{id}/test` 测试模型列表连通性。

错误返回：`401` 管理员鉴权失败；`404` 上游不存在；`400` 请求体或 key 无效、上游不是 `api_key` 鉴权；`503` 凭据保存失败，此时保留旧 key。SigV4 的 Access Key / Secret / Session Token 不使用此接口。
