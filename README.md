# Jev Personal Radar

每天从 GitHub 仓库搜索、少量 RSS 和手动保存的链接中选出最多六条信息，写入**私有** GitHub Issue。工作与学习/效率各最多三条。Jev 只做结构化相关性判断，不会生成文章摘要；日报保留原始标题、链接，并用可核查的主题词生成中文提示。

本仓库是可公开的程序与模板；真实关注词、Issue、密钥必须放在另一个**私有**运行仓库。程序运行时不调用 Codex 或 OpenAI API。

## 本地预览

需要 Go 1.25+。复制 `runtime-template/config.example.json` 为自己的 `config.json`（根目录下该文件已被 `.gitignore` 忽略），修改关注词后运行：

```sh
go test ./...
go run ./cmd/radar -config config.json -dry-run
```

预览不写 GitHub Issue，也不调用 Jev；未提供 `GITHUB_REPOSITORY` 和 `GITHUB_TOKEN` 时只读取 RSS，日报会标注跳过 GitHub 搜索。可用 `-date YYYY-MM-DD` 预览指定日期；真实运行只允许当天日期，以限制重复调用。RSS 是网站更新目录，程序只读取条目的标题、简述、链接和日期，不下载正文。

## 私有 GitHub Actions 运行

1. 把本程序放进你自己的公开 GitHub 仓库；另建一个私有仓库，复制 `runtime-template/` 中的 `config.example.json`、`.github/` 到私有仓库根目录。把配置文件重命名为 `config.json`，按自己兴趣修改。不要把真实配置和导入 Issue 复制到公开仓库。
2. 编辑私有仓库 `.github/workflows/radar.yml`：把 `YOUR_GITHUB_USER/jev-personal-radar` 和 `PIN_PUBLIC_COMMIT_SHA` 换成公开仓库名、已审核的**完整 40 位 commit SHA**。公共代码升级时再手动更新 SHA；不要让私有工作流自动运行浮动的公开分支。
3. 在私有仓库 Settings → Secrets and variables → Actions 添加 `TYPESAFE_API_KEY`。不要把 Key 写进配置、Issue、命令行参数或提交。工作流使用该仓库自带的 `GITHUB_TOKEN` 写 Issue，不需要另建 GitHub PAT，也不需要 Codex API Key。
4. 先手动运行一次 workflow。程序会建立 `radar-digest`、`radar-inbox` 两个标签；之后可用私有仓库的“Radar inbox”Issue 表单粘贴小红书等链接和个人备注。每天约北京时间 09:17 自动运行；GitHub 定时任务可能延迟或偶尔不触发，不能当严格准点服务。

手动导入优先于自动候选。只要日报成功写入，已纳入的导入 Issue 就会关闭；未纳入的保留到后续运行。同一天重跑不会再请求 Jev，只会尝试完成未关闭的导入/旧日报 Issue。日报保留历史，不自动删除。

## 成本、准确性与隐私边界

- Jev 模型固定 `jev-1.13.0`，每自然日最多 20 次请求，每次请求正文最多 8 KiB；首次运行前创建当天占位 Issue，防止同日重复收费。按 2026-09-21 [TypeSafe 公布的价格](https://docs.typesafe.ai/models) `$0.042/百万输入 token`，31 天理论上界约 `$0.22`，低于本项目每月 `$1` 的目标。**这不是 TypeSafe 账户级消费上限**：模型价格、服务商计费方式、用户删除日报后重跑等情况仍以控制台账单为准；如价格变化需暂停并重新核算。
- Jev 缺 Key、返回错误、额度/限流失败时，日报明确显示“降级”，余下内容走关键词规则。GitHub/RSS 失败会标注来源不完整；没有可靠候选时会如实发空日报。不会用虚构摘要填充。
- 发送给 Jev 的只有公开条目的标题、简述、来源和你配置的兴趣词；手动导入链接与个人备注不会发送。配置词如果敏感，请不要写进配置，因为它们会进入 Jev 请求；可改用宽泛主题词。
- RSS、仓库说明和手动链接均视为外部数据，不作为程序指令执行；只接受 HTTP(S) 链接。Jev 选择结果只是候选线索，重要事实请打开原文核对。
- 私有仓库的 Actions 日志和 Issue 含个人信息，应保持私有。不要给工作流过宽权限；模板只申请 `contents: read`、`issues: write`。GitHub Actions 运行时间和 TypeSafe API 计费是两回事。

## 配置和行为

`config.json` 的 `topics.work`、`topics.life` 分别定义关键词与 GitHub 仓库搜索式；`feeds` 定义 RSS/Atom 的名称、HTTP(S) 地址和分类。示例配置含 Go 官方博客、GitHub 更新日志和 Ness Labs，可随时替换。每次只看近七天有日期的条目；同链接（忽略常见跟踪参数）在近十四天日报里出现过则跳过。GitHub 搜索使用公开仓库信息，不读取你工作的私有仓库。

报错时先看私有 Actions 日志、当天 Issue 的“降级/来源不完整”提示和 [TypeSafe 账单](https://console.typesafe.ai/settings/billing)。不必向任何人发送 Key 或登录凭证。此项目当前没有自动发布、外部通知、微信接入或生成式摘要。
