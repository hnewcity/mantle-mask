# 配置与 API 指南

[返回项目首页](../README.md) · [Docker 与版本发布](deployment.md) · [开发与贡献](../CONTRIBUTING.md)

## OpenAI SDK 接入

Python：

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key=os.environ["MANTLE_API_KEY"],
)

# 从聚合列表选取实际上游模型，或使用配置的模型别名
for model in client.models.list().data:
    print(model.id)

stream = client.chat.completions.create(
    model="openai.gpt-oss-120b",  # 替换成当前上游支持的模型
    messages=[{"role": "user", "content": "你好"}],
    stream=True,
)
for chunk in stream:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="")
```

Responses：

```python
first = client.responses.create(
    model="openai.gpt-oss-120b",
    input="解释一下加权轮询",
    store=True,
)
second = client.responses.create(
    previous_response_id=first.id,
    input="给一个例子",
)
print(second.output)
```

cURL：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $MANTLE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai.gpt-oss-120b","messages":[{"role":"user","content":"你好"}],"stream":true}'
```

`model`、工具调用、推理参数、`response_format`、多模态输入等字段透传。实际是否支持由选择的 Mantle 模型决定，代理不会把不支持的能力转换成其他协议。

## API 范围

| 方法 | 地址 | 行为 |
| --- | --- | --- |
| GET | `/v1/models` | 聚合已启用上游的模型／显式别名，去重；远端发现缓存 60 秒 |
| POST | `/v1/chat/completions` | Chat Completions；支持 SSE |
| POST | `/v1/responses` | 创建 Responses；支持 SSE、background 和 previous_response_id 的透传 |
| GET | `/v1/responses/{id}` | 回原上游查询 |
| DELETE | `/v1/responses/{id}` | 回原上游删除 |
| POST | `/v1/responses/{id}/cancel` | 回原上游取消 |
| GET | `/v1/responses/{id}/input_items` | 回原上游查询 input items |
| GET | `/healthz` | 进程存活，不代表 AWS 上游可用 |

Responses 子接口是否可用仍取决于 AWS 对该模型／区域／路径的支持。`/v1/embeddings`、Files、Images、Conversations、Realtime 等未实现，返回 OpenAI 风格的 404 错误；Responses 的 `conversation` 参数明确拒绝，避免错误跨上游路由。

## 多用户与用量

管理员创建用户后，在「API 密钥」指定归属用户。一个用户可以有多个 Key，Key 创建后归属不变。停用用户会拒绝其所有 Key 的新请求和用量查询，已开始的请求继续处理；重新启用会恢复未撤销 Key。撤销单个 Key 不可恢复，历史统计及归属仍保留。

用户打开同一个控制台，选择「用户用量查询」，输入自己的 API Key 即可查看本人名下全部 Key 的统计。无需账号密码，用户 Key 没有管理权限。API 查询范围由鉴权结果限定，传入其他 `user_id` 不能跨用户查询。Responses 对象仍按具体 Key 隔离，同用户不同 Key 不共享历史 Responses。

| 方法 | 地址 | 权限 / 用途 |
| --- | --- | --- |
| GET | `/api/session` | 自动识别管理员令牌或用户 Key |
| GET / POST | `/api/admin/users` | 管理员：列出／创建用户；创建默认启用 |
| PUT | `/api/admin/users/{id}` | 管理员：更新 `name`、`note`、`enabled` |
| GET / POST | `/api/admin/keys` | 管理员：列出／创建 Key；创建体 `{name,user_id}`，省略归属使用默认用户 |
| DELETE | `/api/admin/keys/{id}` | 管理员：撤销 Key，保留归属元数据 |
| GET | `/api/admin/usage` | 管理员：查询统计 |
| GET | `/api/me` | 用户：查看本人及当前 Key 信息 |
| GET | `/api/me/keys` | 用户：查看本人名下 Key 的公开元数据，包括已撤销 Key |
| GET | `/api/me/usage` | 用户：查询本人统计 |

统计参数：`from=YYYY-MM-DD`、`to=YYYY-MM-DD`（含首尾，最多 366 天）、`timezone_offset=480`（UTC 偏移分钟）、`user_id`、`key_id`、`model`，管理端另支持 `upstream_id`。默认最近 30 天；`all=true` 查询全部历史，不能同时指定日期。管理端 Keys 列表可传 `user_id` 和 `include_revoked=true`。

```bash
curl 'http://localhost:8080/api/me/usage?all=true' \
  -H "Authorization: Bearer $MANTLE_API_KEY"
```

统计口径：

- 从启用本版本起记录已鉴权的 `POST /chat/completions` 和 `POST /responses` 请求，包括失败请求；GET Models、读取／删除／取消 Responses 不新增推理请求数。一次网关请求的多次上游尝试仍算一次请求。
- 只累计上游实际返回的 usage，未知值不会估算。缓存 Token 是输入的子集，推理 Token 是输出的子集，总量只算输入 + 输出。
- Chat 流式请求在未指定时自动添加 `stream_options.include_usage=true`，保留其他字段；显式 `false` 会被尊重。上游不支持此选项时可显式关闭，相应请求可能没有用量。
- Responses 的重复 usage 事件和重复读取不会重复计数。后台任务创建时没有 usage，客户端以后读取到最终 usage 时会补记到原请求的用户、Key、模型和日期；网关不主动轮询后台任务。
- 管理员 Playground 单独归入“管理员”。统计里的模型是客户端使用的别名，未指定时继承前序 Response 的模型。
- 上游未返回用量、流中断、失败或后台任务未完成会标记“未返回用量”。网络中断、重试中已发生但未报告的推理无法精确核算，因此这里不是 AWS 账单。
- SQLite 写入发生在发起上游请求以及发送 usage 数据前；无法建立记录时返回 503，不继续推理。最终状态写入错误会记录服务日志并显示存储异常。重启时未完成的记录标记为中断，已保存 Token 保留。

「每日 Token 用量」支持「输入 / 输出」和「按模型」堆叠视图，配色沿用控制台的黑白灰主题。模型图例使用实际的每日模型用量（接口字段 `daily_by_model`），可点击图例或通过搜索框筛选。图表旁可快捷选择今天、近 7／30／90 天、全部历史，或自定义起止日期；模型和日期与页面顶部筛选、总计、明细及 CSV 导出联动。图表最多绘制所选范围内最后 90 天，明细和导出仍覆盖完整范围；超过 6 个模型时图例保留前 5 个并合并其余模型，仍可搜索单独查看。管理端和个人用量页均可使用。

管理端可在「用量统计」选择「按上游」分组或筛选单个上游，查看请求、失败、输入／输出／缓存／推理 Token 和每日趋势，并导出 CSV。管理接口返回 `by_upstream` 分组（无数据时省略）；上游筛选也会作用于总计及其他分组。每次逻辑请求归属最后一次尝试的上游，重试不重复计数。停用或删除上游保留历史记录；已删除上游显示请求时记录的名称。个人用量接口不返回上游分组，也不应用 `upstream_id` 筛选。

升级会自动迁移用量数据库。此前未保存上游信息的历史记录，以及未发往上游的请求，在管理端显示为“未记录上游”，不会猜测或补填归属，原有用量不受影响。

升级时自动把 v1 配置迁移到 v2，原 Key 及摘要保持不变，归入“默认用户”。旧版本只有内存统计，无法补录升级前的历史用量。首次升级前请备份数据目录。

## 路由配置

- **权重**：1–100。相同模型的可用上游按平滑加权轮询分配。
- **模型映射**：每行 `对外模型名 = 上游模型 ID`；配置后只接收这些对外名称。留空表示透传所有模型。不同区域的模型支持不同时，应配置显式映射，避免把不支持的模型发到错误上游。
- **Project**：上游配置非空时覆盖客户端 `OpenAI-Project`；否则转发客户端值。
- **重试**：默认最多额外尝试 2 个上游，每个上游最多一次。仅网络／签名／凭据获取失败与 429、502、503、504 触发候选切换；其他状态直接返回。最大重试设为 0 可以关闭。
- **冷却**：默认 30 秒；同时尊重上游 `Retry-After`，最多计入 10 分钟。没有可用上游时返回 503。
- **Responses 绑定**：按「客户端密钥 ID + 客户端 Project + Response ID」隔离，并持久化 30 天。更换客户端密钥不会自动共享历史 Responses；Playground 使用独立的管理身份。
- **安全重放边界**：流开始后和绑定到历史 Response 的请求不切换。网络错误不能保证 AWS 尚未执行请求，允许重试可能产生重复推理／计费；需要严格避免重复执行时关闭重试。
- **Mantle Chat 流结束兼容**：真实联调发现部分流以 `finish_reason` 后直接 EOF 结束，不带 `[DONE]`。当所有请求的 choices 都已结束且连接正常关闭时，网关补充 `[DONE]`；未完成的流仍返回错误事件。
- 模型别名只改写上游请求里的 `model`，响应保留上游原始模型名与字段。

更改已绑定上游的地址、区域、Project、鉴权模式或 Profile 后，旧 Responses 返回 409。轮换凭据后必须仍属于原 AWS 账户，否则上游可能返回 404。

## AWS SigV4

上游鉴权切换为 AWS SigV4 后可填写 Access Key ID / Secret Access Key / Session Token，也可以留空，使用 AWS SDK for Go v2 的凭据链：

- `AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY`、`AWS_SESSION_TOKEN`
- `~/.aws/config`、`~/.aws/credentials` 和配置的 Profile
- SDK 支持的 Web Identity、ECS／EC2 角色等环境

区域必须明确填写。签名服务默认 `bedrock-mantle`，可选择 `bedrock` 以适配其他 Bedrock 路由。静态短期凭据过期后需要更新；默认凭据链通过 SDK 缓存并按提供方能力刷新。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MANTLE_ADDR` | `127.0.0.1:8080` | 监听地址 |
| `MANTLE_DATA_DIR` | `./data` | 数据目录 |
| `MANTLE_ADMIN_TOKEN` | 自动创建 | 管理令牌，至少 24 字符 |
| `MANTLE_MASTER_KEY` | `data/master.key` | 可选的 base64 编码 32 字节 AES 主密钥 |
| `MANTLE_CORS_ORIGINS` | 空 | 逗号分隔的精确 Origin 白名单；同源控制台不需要配置 |

`data/` 包含配置、加密凭据、主密钥、管理员令牌、Responses 路由索引及 SQLite 用量库 `usage.db`（运行时可能有 `-wal` / `-shm` 文件）。可在控制台导出密码加密的完整迁移备份（见下方「完整备份与迁移」）。若直接备份文件，先停止网关，再复制整个目录，避免复制运行中数据库的一部分；文件级备份使用外部主密钥时还要保留对应环境密钥。已有配置缺少主密钥时会拒绝启动。

## 部署边界

- 当前是单进程、单数据目录实现；不要让多个进程共享写入同一目录。用量采用本地 SQLite；尚未实现分布式路由、配额、限流或金额计费。
- 模型发现是按需探测，不是后台健康检查；控制台区分待检测、连接正常、鉴权异常和冷却。
- 请求体最多 16 MiB；非流式上游正文最多 32 MiB；SSE 单事件观察上限 1 MiB；远端模型列表最多 4 MiB。
- 连接建立 10 秒，响应头 60 秒；每次流写入 30 秒；完整请求时限默认 300 秒，可在界面调整。
- 统计从本版本开始记录；历史数据不会自动清理，请为 SQLite 用量库预留空间并定期备份。
- 日志仅在内存中保留，重启清空；总览累计值从本次启动开始。总览趋势图按仍保留的日志计算；「用量统计」读取 SQLite，不随重启清空。
- Windows 挂载盘上的 Linux 权限位可能由 WSL 挂载设置决定。长期部署优先使用 Linux 文件系统和专用数据卷。
- 已用真实 Bedrock Bearer 凭据验证 `us-west-2` 的 `openai.gpt-oss-20b`；其他模型／区域及真实 SigV4 权限未做穷举验证。


## 完整备份与迁移

管理员控制台的「网关设置 → 备份与迁移」支持**在线导出、在线恢复和用量补齐**，不需要手动停流量，也不会为了备份而等待 SSE 请求结束。

1. 旧实例保持运行，设置至少 12 个字符的备份密码并导出 `.mmbak` 在线快照。文件和密码分别妥善保管。
2. 在新实例选择文件并输入密码，保持「仅补齐用量和路由」关闭，输入 `RESTORE` 确认恢复。全部用户、原 API Key、上游和已保存凭据、模型映射及设置会恢复；**配置是替换，用量是合并**。
3. 将客户端地址、域名或负载均衡入口切换到新实例。旧实例中已接受的请求继续按原配置完成，不要杀掉这些连接。
4. 旧实例的收尾请求完成后，再在线导出一次，在新实例打开「仅补齐用量和路由」导入。此模式不改用户、Key、上游和设置，也不删除新实例已产生的用量。可以重复补齐，同一请求不会重复计数。

在线快照包含捕获时刻已持久化的数据，**不是两个实例间的实时同步通道**，不可能自动包含捕获后才发生的请求或 Token。恢复后原客户端 Key 可继续使用；要保留源实例的尾部用量，请完成上述补齐步骤。继续有客户端连接旧地址时，需要继续同步这些后续数据，不能仅凭首次导出就声称所有历史都已迁走。

### 一致性与进行中请求

配置、Responses 路由与 SQLite WAL 读快照在一个短暂的状态检查点捕获，随后数据读取和加密不占用流式请求生命周期锁。SQLite 使用独立的只读连接，长快照不会占住用量写连接。恢复按小批事务合并历史，再切换完整配置；没有“有活动请求就拒绝备份”的 409，也没有“迁移时所有请求都 503”的全局开关。短暂文件同步和数据库事务仍可能带来正常的 I/O 延迟，不承诺零延迟。

已接受的推理请求持有鉴权时的配置快照与原凭据；新请求使用恢复后的配置。目标上真正进行中的请求行不会被导入覆盖，其后续 Token 和完成状态照常保存。导入的源实例未完成请求标为「待补齐」，与本机「进行中」分开显示；重启不会把它们伪造为失败。再次导入完成后的快照会补齐状态和 Token，并清除待补齐标记。

用量按原请求 ID 合并，累计 Token 取已观察值的最大值，不相加重复记录；已完成记录不会被旧的未完成快照降级。历史用户、Key、模型、上游归属及缓存/推理 Token 都保留。不同用户/Key/时间/路径占用了同一请求 ID 时会拒绝，而不是错误地合并归属。Responses 用量关联和路由保留，发生同 scope 冲突时目标现有绑定优先，避免改路由到其他上游。AWS 侧响应对象本身及其有效期不由备份迁移。

### 安全与恢复边界

备份使用 PBKDF2-HMAC-SHA256（600,000 次迭代、随机盐）和 AES-256-GCM 加密验证完整性。上游凭据用目标实例主密钥重新加密，无需搬运源主密钥。客户端 Key 仍只保存原摘要，无法重新显示已丢失的 Key 明文，但原 Key 鉴权能力保留。

文件最大 256 MiB。不包含或覆盖目标管理员令牌、主密钥、部署环境变量、外部 AWS Profile / IAM 角色；不搬运源实例的内存日志和运行计数，目标实例正在使用的运行状态也不会因导入重置。默认 AWS 凭据链、共享配置或部署角色仍需单独迁移。全量恢复会替换目标配置；建议先备份目标配置，再操作。

密码错误、文件损坏、不兼容版本和预校验失败不会改变数据。恢复前写入私有恢复日志；实际写入失败时已经合并的历史保留，业务请求不会仅因为恢复任务待续而被停用。修复磁盘空间/权限并重启后幂等续做；实际磁盘故障仍可能导致用量写入失败，须先修复。恢复任务执行中或待续时，暂不接受其他备份任务和配置修改，但查询、推理和 SSE 继续处理。不要删除 `restore-pending.json`、更换待续时的主密钥，或让多个进程共享同一数据目录。

新导出的在线备份使用 payload v2，仍可读取旧版离线备份 v1；在线备份需要用支持在线恢复的新版本导入，旧版会拒绝而不是错误解释进行中记录。本版用量库升级到 schema v3，增加待补齐标记。旧版程序不能直接打开升级后的数据库；不要直接回退旧镜像并共享新数据卷。使用 HTTPS 或可信本机连接操作，因为密码也需要通过连接传输。需要备份整个部署环境或文件超过上限时，仍可使用停机后的完整数据目录备份，并保存外部配置和主密钥。

管理员 API：`POST /api/admin/backup/export` 接收 JSON `{ "password": "..." }`；`POST /api/admin/backup/restore` 接收 multipart 的 `file`、`password`、`confirm=RESTORE`，可选 `mode=full`（默认，替换配置并合并用量）或 `mode=usage`（只补齐用量与路由）。密码不得放在 URL。用量报表增加 `pending_sync_requests`，用于区分源实例待补齐记录与本机 `in_flight_requests`。
