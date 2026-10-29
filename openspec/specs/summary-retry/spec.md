# 既有摘要补录基线

## Purpose

依据 `internal/radar/retry_summaries.go` 与 `internal/radar/retry_summaries_test.go`；对应显式 CLI 参数 `-retry-summaries`，公开工作流模板尚未接入该选项。

## Requirements

### Requirement: 只补既有条目的说明

补摘要 SHALL 只处理当天已完成且开放的日报中缺失说明的条目，不重新筛选或调用 Jev。

#### Scenario: 仍有缺失摘要
- **WHEN** 日报格式有效、公开来源可读且 MiniMax 返回有效说明
- **THEN** 保留原选择及热度证据，补入缺失说明并更新原 Issue

#### Scenario: 已有全部摘要
- **WHEN** 日报中没有待补目标
- **THEN** 返回无需再调用的信息，不请求 MiniMax

### Requirement: 失败与并发编辑保护

补摘要 SHALL 在零条生成成功或日报在生成期间变化时保留远端正文。

#### Scenario: 无成功说明
- **WHEN** 所有待补条目均无法生成可靠说明
- **THEN** 返回错误，不更新日报

#### Scenario: 期间有编辑
- **WHEN** 写入前重新读取的 Issue 正文或状态不同于开始时
- **THEN** 返回 changed 错误，不覆盖该 Issue
