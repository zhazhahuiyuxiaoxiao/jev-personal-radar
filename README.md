# Jev Personal Radar

Jev Personal Radar 的意思是“Jev 个人信息雷达”。每天从 GitHub 今日/本周热门仓库、Hacker News 热门文章及你手动保存的链接中，选出**最多六条**写入私有 GitHub Issue。先确认近期热度，再由 Jev 判断它与你的工作或学习是否相关；MiniMax 依据最终入选的公开原文，用白话解释“是什么、有什么用、第一步怎么试”。每条热门推荐都列出可点开的热度证据；当天没有合适内容就不凑数。

本仓库是可公开的程序与模板；真实关注词、Issue、密钥必须放在另一个**私有**运行仓库。程序运行时不调用 Codex 或 OpenAI API。

## 本地预览

需要 Go 1.25+。复制 `runtime-template/config.example.json` 为自己的 `config.json`（根目录下该文件已被 `.gitignore` 忽略），修改关注词后运行：

```sh
go test ./...
go run ./cmd/radar -config config.json -dry-run
```

预览不写 GitHub Issue，也不调用 Jev 或 MiniMax；无需 GitHub Token 即可读取公开热榜。预览中的“与你相关”仅按关键词保守判断，**不能代表正式运行时 Jev 的判断**。可用 `-date YYYY-MM-DD` 预览指定日期；真实运行只允许当天日期，以限制重复调用。热榜来自当前公开页面/API，不能用 `-date` 查看历史热榜。

## 私有 GitHub Actions 运行

1. 把本程序放进你自己的公开 GitHub 仓库；另建一个私有仓库，复制 `runtime-template/` 中的 `config.example.json`、`.github/` 到私有仓库根目录。把配置文件重命名为 `config.json`，按自己兴趣修改。工作关键词可包含较宽的“AI API”“AI 开发工具”，让新名字也有机会被 Jev 判断。不要把真实配置、导入 Issue 或反馈 Issue 复制到公开仓库。已有私有运行仓库可沿用旧配置；其中旧 `github_queries`、`feeds` 不再为主日报提供候选。
2. 编辑私有仓库 `.github/workflows/radar.yml`：把 `YOUR_GITHUB_USER/jev-personal-radar` 和 `PIN_PUBLIC_COMMIT_SHA` 换成公开仓库名、已审核的**完整 40 位 commit SHA**。公共代码升级时再手动更新 SHA；不要让私有工作流自动运行浮动的公开分支。
3. 在私有仓库 Settings → Secrets and variables → Actions 添加 `TYPESAFE_API_KEY` 和 `MINIMAX_API_KEY` 两个 repository secrets。后者是在 MiniMax 开放平台创建的 Key；普通 API Key 走按量付费，订阅 Key 使用 Token Plan/积分，按你的账户资源选择。不要把 Key 写进配置、Issue、命令行参数或提交。工作流使用该仓库自带的 `GITHUB_TOKEN` 写 Issue，不需要另建 GitHub PAT，也不需要 Codex API Key。
4. 先手动运行一次 workflow。程序会建立 `radar-digest`、`radar-inbox` 两个标签；之后可用私有仓库的“Radar inbox”Issue 表单粘贴小红书等链接和个人备注。每天约北京时间 09:17 自动运行；GitHub 定时任务可能延迟或偶尔不触发，不能当严格准点服务。

已有的已完成日报不会自动重写；升级后的新格式从下一份日报开始生效。手动导入的链接仍不抓网页、不调用 MiniMax，只显示你填写的备注。

如果当天日报已完成，但因 MiniMax Key 或额度问题缺少中文说明，可在私有工作流中手动选择 `retry_summaries`。它只读取当天已入选的公开条目原文，为缺失的说明再次调用 MiniMax（最多六次），然后原位更新同一个私有 Issue；不重新筛选，也不调用 Jev。若一条说明都没生成成功，Issue 保持原样并让工作流报错。此操作是显式付费重试，不属于普通同日重跑的零模型调用保证。

手动导入优先于自动候选，且不要求有热度证据。只要日报成功写入，已纳入的导入 Issue 就会关闭；未纳入的保留到后续运行。同一天重跑不会再请求 Jev 或 MiniMax，只会尝试完成未关闭的导入/旧日报 Issue；如果首次运行在摘要阶段中断，重试会显示原始简介，不重复付费生成。日报保留历史，不自动删除。

## 私有反馈与人工调整

在**私有运行仓库**点 New issue → Radar feedback，每个条目记录一次“有用”“无关”或“遗漏”，可附日报日期与简短原因。遗漏可以写希望看到的主题或公开链接；不要填写公司内部资料。反馈 Issue 不会被日报程序读取、关闭或发送给 Jev，也不会自动改变推荐，暂时由你保留作为复盘依据。

建议先积累一到两周反馈，再区分“热榜没有收录”“已收录但 Jev 判断不相关”“内容看不懂”三类。当前主来源仅覆盖 GitHub 和 Hacker News，不保证捕获每一次新产品发布；若反复漏掉同一类来源，再考虑增加它，而不是继续堆关键词。这个反馈入口不增加 Jev 或 Codex 调用。

## 成本、准确性与隐私边界

- Jev 模型固定 `jev-1.13.0`，每自然日最多 20 次请求，每次请求正文最多 8 KiB；首次运行前创建当天占位 Issue，防止同日重复收费。按 2026-09-21 [TypeSafe 公布的价格](https://docs.typesafe.ai/models) `$0.042/百万输入 token`，31 天理论上界约 `$0.22`，低于 Jev 部分每月 `$1` 的目标；MiniMax 另计。**这不是 TypeSafe 账户级消费上限**：模型价格、服务商计费方式、用户删除日报后重跑等情况仍以控制台账单为准；如价格变化需暂停并重新核算。
- MiniMax 固定使用 `MiniMax-M3`，只对最终入选的自动条目调用，单次运行最多 6 次、每条来源正文最多 6000 字符、生成上限 1000 token。按 2026-09-22 [MiniMax 中国站标准价格](https://platform.minimax.cn/docs/guides/pricing-paygo) 输入 ¥2.1/百万 token、输出 ¥8.4/百万 token 计费；实际金额取决于正文长度、模型思考输出、调用失败与官方价格变化。**程序限制不是账户级消费上限**，需要在 MiniMax 控制台查看用量。
- Jev 缺 Key、返回错误、额度/限流失败时，日报明确显示“降级”；热榜候选仅在命中关注词时才保守入选。GitHub/Hacker News 失败会标注来源不完整；没有可靠候选时会如实发空日报，不拿旧热榜或普通 RSS 凑数。
- MiniMax 缺 Key、来源内容不足、README/公开网页不可读取或生成失败时，条目显示原始简介，并明确无法可靠解释。生成内容可能有误，重要事实仍需原文核对；“热度”数字来自来源页面/API，不由模型生成。
- 发送给 Jev 的只有公开条目的标题、公开简述、来源和你配置的兴趣词；手动导入链接与个人备注不会发送。配置词如果敏感，请不要写进配置，因为它们会进入 Jev 请求；可改用宽泛主题词。
- 发送给 MiniMax 的只有最终入选的公开自动条目的标题，以及公开仓库 README 或热门文章链接的公开网页正文；不发送私有配置、手动导入链接、个人备注或 GitHub Token。MiniMax 默认连接中国站 `api.minimax.cn`；请使用该站支持的 Key。
- 公开网页只允许 HTTP(S)，校验重定向及解析后的地址，限制大小与超时；网页、仓库说明和手动链接均视为外部数据，不作为程序指令执行。网页不可读或资料不足时不编造上手步骤。
- 私有仓库的 Actions 日志和 Issue 含个人信息，应保持私有。不要给工作流过宽权限；模板只申请 `contents: read`、`issues: write`。GitHub Actions 运行时间和 TypeSafe API 计费是两回事。

## 配置和行为

`config.json` 的 `topics.work`、`topics.life` 关键词是 Jev 的个人相关性提示，不再限制热榜采集。旧 `github_queries`、`feeds` 字段仍可读取以兼容原有私有配置，但热度优先日报不再抓取或推荐这些来源。GitHub 读取今日/本周 Trending；Hacker News 读取热门榜前 30 条中近 48 小时、至少 30 分且带公开链接的文章。两个榜单去重后最多 20 条进入 Jev 判断；同链接（忽略常见跟踪参数）在近十四天日报里出现过则跳过。模型只能从热榜候选中选内容，不读取你工作的私有仓库。

报错时先看私有 Actions 日志、当天 Issue 的“降级/来源不完整/摘要失败”提示、[TypeSafe 账单](https://console.typesafe.ai/settings/billing)和 MiniMax 控制台用量。不必向任何人发送 Key 或登录凭证。此项目当前没有自动发布、外部通知或微信接入。
