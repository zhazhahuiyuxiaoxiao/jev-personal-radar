# jev-personal-radar 工程基线

- 核对日期：2026-10-29。
- 基线：`main@13d7ec2d1f94e771f7ca599d58710d0877deda9c`（文档补录前提交）。
- 来源：本仓库已提交源码、测试和现有说明；不纳入其他工作区未提交或未合并能力。
- 范围：只补文档和流程，不改变业务、依赖、数据或部署。

## 能力与证据

| 验收目标 | 源码 | 既有测试 |
| --- | --- | --- |
| 预览不写 Issue、不调用模型；真实运行限定当天 | `cmd/radar/main.go`、`internal/radar/app.go` 的 `Run` | `internal/radar/radar_test.go` 的 dry-run、同日复用与降级用例 |
| 来源热度、手动优先与最多六条 | `internal/radar/trends.go`、`items.go`、`app.go` | `radar_test.go` 的热度解析/排序、`focus_test.go` 的预留位置用例 |
| 首轮空结果才项目补查、关注检索限额 | `internal/radar/project_search.go`、`project_pass.go`、`focus_search.go` | `project_search_test.go`、`focus_test.go` |
| 补摘要只更新既有日报，失败或并发编辑保护 | `internal/radar/retry_summaries.go` | `retry_summaries_test.go` |
| 公开网页地址限制、正文提取 | `internal/radar/public_page.go`、`items.go` | `radar_test.go` 的网络守卫用例 |

## 启动与验证

`go.mod` 要求 Go 1.25.0，唯一直接依赖是 `golang.org/x/net`。使用已准备工具链和依赖运行 `go test ./...`；真实来源检查由 `RADAR_LIVE_TRENDING=1` / `RADAR_LIVE_HN=1` 显式启用，默认跳过，本次均未执行。

将 `runtime-template/config.example.json` 复制为自己的忽略文件 `config.json`，配置 work/life 主题后，可执行 `go run ./cmd/radar -config config.json -dry-run`。该预览仍请求公开 GitHub/Hacker News，不调用 Jev/MiniMax，也不写 Issue；不要把真实兴趣词纳入提交。

正式运行读取 `GITHUB_REPOSITORY=<OWNER/PRIVATE_RUNTIME_REPO>`、`GITHUB_TOKEN=<TOKEN>`、`TYPESAFE_API_KEY=<KEY>`、`MINIMAX_API_KEY=<KEY>` 等环境字段。只记录字段与占位符，不通过命令行打印实际凭据；公开模板钉住完整代码 SHA，权限为 contents:read、issues:write。

## 验收清单与边界

- [ ] 预览、缺模型 Key、来源失败及同日已完成日报重跑。
- [ ] 手动导入优先、热度证据、六条上限、模型调用上限。
- [ ] 显式补摘要零成功不覆盖、期间编辑冲突不覆盖。
- [ ] 实际私有目标、Actions 权限、提供商费用及网络来源另行验证。

README 原有报价附历史日期，本次未重新核价，不能当当前价格承诺。CLI 依赖部署者提供正确私有目标，不能仅凭日志中的“私有”字样证明仓库可见性。公开 runtime-template 当前只有每天 09:17 的基础日报调度与三个 Issue 表单：未包含重试选择参数、focus-links 工作流或 focus 表单，README 已据此更正；本次不增加运行能力。

## 本次验证记录

- 已执行：OpenSpec 1.14.2 全量严格格式校验、`git diff --check`、新改 Markdown 相对路径核对及命令来源静态核对，结果见本次 PR。
- 未执行：业务测试、依赖安装、服务启动、真实平台集成、收费模型调用、发布/部署。仅文档变更不将源码审阅表述为运行通过。
- 后续功能验收应运行上文对应测试，并补正常、边界和失败路径证据；外部系统与真实设备需另行授权及验证。

## 文档分工

[README](../README.md) 是用途与启动入口；[AGENTS](../AGENTS.md) 约束修改流程；[OpenSpec](../openspec/README.md) 区分当前能力与未来变更。旧文档中的已验证结论保留其原时间与上下文，不作为当前生产证据。
