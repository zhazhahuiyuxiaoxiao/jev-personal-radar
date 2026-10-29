# jev-personal-radar 项目约定

## 文档与变更约定（2026-10-29）

- 公共程序与私有运行内容分离，禁止读取或提交实际 config.json、个人 Issue 正文和反馈笔记。
- 区分 dry-run 的公开网络读取与真实运行的 Issue 写入/收费调用；禁止将预览解释成离线运行。
- 保持同日普通重跑不再调用模型、显式重试和调用上限、手动导入不发模型、来源失败明确降级的语义。
- `go test ./...` 是测试入口；默认不设置 `RADAR_LIVE_TRENDING` 或 `RADAR_LIVE_HN`。实际运行需要明确网络、费用和私有目标授权。

- 先读 README、[工程基线](docs/engineering-baseline.md)及相关源码/测试；当前规范位于 `openspec/specs/`。
- 先明确可验证验收目标；涉及权限、持久化格式、外部传输、删除或失败语义的业务变化，先说明旧/新行为并确认范围。
- 当前文档基线不授权部署、迁移、提交、推送或读取真实凭据/用户数据。示例使用占位符，保留来源与许可。
- 非简单行为变更先按 [OpenSpec 流程](openspec/README.md)建立 change；仅文档修正保持最小范围，不伪造功能提案。
- 文档检查：`OPENSPEC_TELEMETRY=0 openspec validate --all --strict --no-interactive`、`git diff --check`，并核对相对链接及命令来源；格式检查不等于业务验收。
