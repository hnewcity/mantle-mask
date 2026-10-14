# Docker 部署与版本发布

[返回首页](../README.md) · [配置与 API](guide.md)

## 镜像部署

镜像：`ghcr.io/hnewcity/mantle-mask`，支持 `linux/amd64` 与 `linux/arm64`。`latest` 是最新稳定标签，`edge` 是主分支构建；生产环境建议锁定版本或镜像 digest。

```bash
git clone https://github.com/hnewcity/mantle-mask.git
cd mantle-mask
cp .env.example .env
# 编辑 .env 设置随机 MANTLE_ADMIN_TOKEN，至少 24 字符
docker compose pull
docker compose up -d --no-build
docker compose logs -f
```

不设置管理员令牌时，程序在数据卷内生成 `/app/data/admin-token`。镜像不带 shell，可以复制出来查看；读取后不要提交或共享：

```bash
container=$(docker compose ps -q mantle-mask)
docker cp "$container:/app/data/admin-token" ./admin-token.txt
cat ./admin-token.txt
rm ./admin-token.txt
```

## 本地源码构建

```bash
docker compose up -d --build --pull never
# 或独立构建并验证
docker build -t mantle-mask:local .
bash scripts/docker-smoke.sh mantle-mask:local
# 多架构构建推送到自己的镜像仓库
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg SOURCE_URL=https://github.com/YOUR-ACCOUNT/mantle-mask \
  -t YOUR-REGISTRY/mantle-mask:YOUR-TAG --push .
```

构建前端与 Go 编译器运行在构建机架构，后端交叉编译；不用模拟器执行目标架构编译器。镜像最终使用 distroless static 的 UID/GID `65532:65532`。`.dockerignore` 使用允许列表，不把本地凭据、Git 历史、运行数据或截图传入构建上下文。

## 数据、凭据与备份

- Compose 使用 named volume `mantle-data`，Docker 会为名称加当前 Compose 项目前缀。不要在需要保留数据时执行 `docker compose down -v`。
- 推荐从「网关设置 → 备份与迁移」导出密码加密的 `.mmbak` 文件，在目标实例恢复，保留全部 API Key、上游凭据与用量。操作前暂停客户端流量并等待流式请求结束；恢复会覆盖而非合并，目标管理员令牌及主密钥保持不变。具体范围、限制和步骤见 [使用指南](guide.md#完整备份与迁移)。
- 若使用文件级备份，先停止网关，完整备份数据卷，再启动；不要只复制运行中的 `usage.db`，它可能有 WAL/SHM 配套文件。
- 外部配置 `MANTLE_MASTER_KEY` 时须另外保管该密钥；只备份数据库不足以恢复上游凭据。
- 换用 bind mount 时保证宿主机目录对 UID 65532 可写，不要用 root 运行网关来绕过权限配置。
- `.env` 的 `MANTLE_ADDR` / `MANTLE_DATA_DIR` 用于原生程序参考；Compose 明确固定容器地址 `0.0.0.0:8080` 和数据目录 `/app/data`。
- 使用 SigV4 默认凭据链时，在自己维护的 Compose override 中显式传入所需 AWS 变量或只读挂载共享配置。仓库默认 Compose 不隐式转发宿主机 AWS 凭据。
- 公网服务前置 HTTPS 反向代理，关闭 SSE 缓冲并设置足够长的超时。不要让多个副本共享同一 SQLite 数据目录。

## CI / GHCR / Release

工作流为 `.github/workflows/ci.yml`：

| 触发 | 验证 | 发布 |
| --- | --- | --- |
| Pull Request | Go race、vet、前端单测与构建、E2E、二进制打包、Docker smoke、多架构构建 | 不登录 registry、不推送镜像 |
| main push / main 手动运行 | 同上 | GHCR `edge`、`sha-*` |
| `vX.Y.Z` 标签 | 同上 | 版本、主次版本、`latest`、`sha-*`；GitHub Release |
| `vX.Y.Z-rc.N` 等预发布标签 | 同上 | 预发布版本及 `sha-*`；预发布 Release，不覆盖 `latest` |

镜像发布必须等待完整测试与容器 smoke 成功。Release 必须等待镜像发布成功，附件含两种 Linux 架构的 `.tar.gz` 和 SHA-256 校验文件。CI 同时保留短期测试截图和 trace 供排障；产物不包括生产数据。

维护者首次发布：

1. 确认仓库启用 Actions，组织策略允许工作流的 `packages: write` / `contents: write` 权限。
2. 主分支通过 CI 后，创建 `vX.Y.Z` 标签并推送；发布示例：

```bash
git tag v0.2.0
git push origin v0.2.0
```

3. 等待 CI。认证使用仓库内置 `GITHUB_TOKEN`，无需自行保存 PAT、Docker Hub 密码或 AWS Key。
4. GHCR 首次创建的包可能是 Private。到 Package settings 将 visibility 设为 Public，确保 README 中的匿名拉取命令可用；源码仓库 Public 不替代此检查。
5. 在未登录 GHCR 的环境验证 `docker pull ghcr.io/hnewcity/mantle-mask:0.2.0`。

Fork 后将 README / Compose 镜像名改为自己的命名空间。CI 自动使用小写的 `github.repository` 作为 GHCR 路径，并将对应仓库写入 OCI source 标签。手动运行请选择主分支；不要把生产 secrets 加到本工作流。

## 二进制校验与运行

下载 Release 的对应架构压缩包和 checksums 文件，确认文件名后执行：

```bash
sha256sum --check --ignore-missing mantle-mask_v0.2.0_checksums.txt
tar -xzf mantle-mask_v0.2.0_linux_amd64.tar.gz
cd mantle-mask_v0.2.0_linux_amd64
./mantle-mask
```

本地生成发布包：构建前端后运行 `bash scripts/package-release.sh v0.2.0`。脚本输出到被忽略的 `dist/`，不会创建 Git tag 或推送内容。
