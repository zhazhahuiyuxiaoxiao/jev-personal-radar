# 日报运行基线

## Purpose

依据 `cmd/radar/main.go`、`internal/radar/app.go`、`internal/radar/items.go` 与 `internal/radar/radar_test.go`。

## Requirements

### Requirement: 预览与真实写入分离

程序 SHALL 在 dry-run 下禁用 Issue 写入和 Jev/MiniMax 调用；真实日报运行 MUST 使用当天北京时间日期。

#### Scenario: 本地预览
- **WHEN** 以 dry-run 运行有效配置
- **THEN** 读取公开来源并输出预览，不创建或更新 Issue，不调用两种模型

#### Scenario: 真实运行指定旧日期
- **WHEN** 非 dry-run 且日期不是 Asia/Shanghai 当天
- **THEN** Run 返回错误，不进入日报写入与模型调用流程

### Requirement: 同日日报复用

程序 SHALL 复用当天已完成日报，普通重跑不重复模型筛选或摘要生成；显式 retry 模式属于另行选择的操作。

#### Scenario: 当天已有完成日报
- **WHEN** 普通真实运行发现当天 complete 标记
- **THEN** 只执行已纳入导入项与上一份日报的收尾关闭，不重新调用模型

#### Scenario: 首次运行
- **WHEN** 当天日报尚不存在且目标配置有效
- **THEN** 在模型调用前创建 running 占位 Issue，完成后更新同一 Issue

### Requirement: 来源不足明确降级

程序 SHALL 在来源或相关性模型失败时标记缺口，不强行填满六条推荐。

#### Scenario: Jev 调用失败
- **WHEN** 首轮 Jev 请求失败
- **THEN** 后续候选使用保守关键词判断，日报记录降级信息
