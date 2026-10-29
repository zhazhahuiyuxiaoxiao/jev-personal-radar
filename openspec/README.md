# OpenSpec 维护入口

本目录是对现有项目的渐进式基线补录，不是历史提案，也不表示新功能已经开发或验收。参考 [OpenSpec 既有项目接入指南](https://github.com/Fission-AI/OpenSpec/blob/main/docs/existing-projects.md)。

- `specs/<capability>/spec.md`：有源码依据的当前核心行为；只覆盖选定流程，不宣称全量覆盖。
- `changes/`：未来实际变更的提案与增量规范；本次没有待开发功能，因此只保留 `.gitkeep`。
- `config.yaml`：项目上下文与 spec-driven 产物规则。

后续非简单变更流程：明确验收与需确认的业务取舍 → 在 `changes/<change-id>/` 写 `proposal.md` → 在 `changes/<change-id>/specs/<capability>/spec.md` 写增量规范（ADDED/MODIFIED/REMOVED Requirements）→ 写 `design.md`、`tasks.md` → 严格校验并完成必要确认 → apply，按任务实施与验证 → 同步正式 specs 后 archive。不要将未实现的期望直接覆盖当前规范，也不要为本次补录伪造历史 change。

已安装 OpenSpec CLI 时，在仓库根目录运行：

```sh
OPENSPEC_TELEMETRY=0 openspec validate --all --strict --no-interactive
git diff --check
```

本次使用 OpenSpec 1.14.2。校验仅检查规范结构；业务测试、真实外部服务与生产发布另见 [工程基线](../docs/engineering-baseline.md)。工具升级应单独评估，不由文档任务自动安装依赖。
