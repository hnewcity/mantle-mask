# 贡献指南

感谢帮助改进 Mantle Mask。提交的贡献应允许按本仓库 AGPL-3.0-only 许可证分发；请保留第三方版权声明。

## 开发环境

使用 Linux / WSL2、Go 1.27、Node.js 24、npm、GCC 和 libc 开发依赖。首次准备环境可以运行 `python3 scripts/bootstrap-dev.py`，它会下载校验过的工具链到用户目录，不覆盖系统工具。

```bash
git clone https://github.com/hnewcity/mantle-mask.git
cd mantle-mask
bash scripts/build.sh
bash scripts/dev.sh
```

开发脚本在 8080 启动 Go，在 5173 启动 Vite。原生程序不会自动读取 `.env`，请提前 export 所需环境变量。测试不要使用真实 `data/` 或生产凭据。

## 提交前检查

```bash
make test
make build
cd web
npx playwright install --with-deps chromium
npm run test:e2e
cd ..
docker build -t mantle-mask:smoke .
bash scripts/docker-smoke.sh mantle-mask:smoke
```

`make test` 覆盖 Go race tests、go vet、Vitest 和 TypeScript 检查。E2E 使用临时数据目录和本地模拟上游，在 18080 / 18090 运行，不访问 AWS；请保证端口空闲。Docker smoke 检查非 root 用户、嵌入前端、鉴权、卷写入和重启持久化。

## 更新 README 截图

```bash
make build
cd web
UPDATE_SCREENSHOTS=1 npm run screenshots:readme
```

截图用真实前端搭配 Playwright 拦截的固定演示数据，不读取生产 `data/`，不包含真实凭据。输出到 `docs/images/overview.png`；普通 CI 只写 `test-results/readme-overview.png`，不会修改仓库图片。中文字体建议使用 Noto Sans CJK。更换界面后应人工检查截图再提交。

## 代码与 PR

- Go 使用 `gofmt`；TypeScript 使用严格类型、双引号、两空格缩进和无行尾分号。
- 复用现有 UI 组件，保持中文文案和无障碍交互。
- 修复鉴权、路由、持久化和 SSE 时添加对应测试。
- 修改用量存储时，另用一次性 MySQL 8.0.19+ 运行 `MANTLE_TEST_MYSQL_DSN='user:pass@tcp(host:3306)/' go test -race ./internal/usage`。DSN 不带库名时，每个测试会新建并删除自己的 `mantle_mask_test_*` 库，请勿指向生产库。
- 提交信息使用简短祈使句；PR 说明行为变化、关联 Issue、验证命令和结果。界面变更附截图。
- 不提交 `.env`、运行数据、私钥、AWS 凭据、管理员令牌或业务对话。安全问题请阅读 [SECURITY.md](SECURITY.md)。

## 真实 AWS 测试（可选且可能计费）

```bash
node web/scripts/aws-smoke.mjs /path/to/credentials.env --discover-only
# 以下调用会产生真实推理用量，必须自行确认权限和费用：
node web/scripts/aws-smoke.mjs /path/to/credentials.env
```

凭据文件须提供 `AWS_BEARER_TOKEN_BEDROCK` 与 `AWS_REGION`；脚本支持 export 前缀。真实云端测试绝不在常规 CI 自动运行。输出的脱敏结果留在被忽略的 `test-results/`。
