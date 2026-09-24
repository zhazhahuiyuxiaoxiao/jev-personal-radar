package radar

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	urlMarker   = regexp.MustCompile(`<!-- radar-url: ([A-Za-z0-9_-]+) -->`)
	inboxMarker = regexp.MustCompile(`<!-- radar-inbox: ([0-9]+) -->`)
)

func digestTitle(date string) string { return "[Radar] " + date }

func seenFromIssues(issues []issue, now time.Time) map[string]bool {
	seen := make(map[string]bool)
	for _, entry := range issues {
		if entry.CreatedAt.Before(now.AddDate(0, 0, -14)) {
			continue
		}
		for _, match := range urlMarker.FindAllStringSubmatch(entry.Body, -1) {
			decoded, err := base64.RawURLEncoding.DecodeString(match[1])
			if err == nil {
				seen[string(decoded)] = true
			}
		}
	}
	return seen
}

func inboxNumbers(body string) []int {
	var numbers []int
	for _, match := range inboxMarker.FindAllStringSubmatch(body, -1) {
		n, err := strconv.Atoi(match[1])
		if err == nil {
			numbers = append(numbers, n)
		}
	}
	return numbers
}

func markdownText(s string) string {
	s = truncate(s, 300)
	replacer := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(s)
}

func renderDigest(date string, items []Item, degraded bool, failures []string, jevCalls, jevTokens int, summaryNote string) string {
	var b strings.Builder
	b.WriteString("<!-- radar-status:complete -->\n")
	fmt.Fprintf(&b, "# 个人信息雷达 · %s\n\n", date)
	if degraded {
		b.WriteString("⚠️ 本期为降级结果：Jev 未完成全部筛选，部分或全部条目使用规则筛选。\n\n")
	} else if jevCalls == 0 {
		if len(items) == 0 {
			b.WriteString("本期没有通过热度及相关性筛选的条目，无需调用 Jev。\n\n")
		} else {
			b.WriteString("本期仅有手动导入条目，无需调用 Jev。\n\n")
		}
	} else {
		b.WriteString("筛选方式：先看公开热榜，空结果时补查公开项目，再由 Jev 判断相关性；标题与链接来自原始来源。\n\n")
	}
	if summaryNote != "" {
		b.WriteString(markdownText(summaryNote) + "\n\n")
	}
	if len(failures) > 0 {
		b.WriteString("来源不完整：" + markdownText(strings.Join(failures, "；")) + "。\n\n")
	}
	if len(items) == 0 {
		if len(failures) > 0 {
			b.WriteString("今天没有可验证的推荐条目；请检查来源状态。\n\n")
		} else {
			b.WriteString("今天没有符合筛选条件的新条目。\n\n")
		}
	}
	b.WriteString("## 今日热点\n\n")
	for i, item := range items {
		category := "工作"
		if item.Category == Life {
			category = "学习与效率"
		}
		fmt.Fprintf(&b, "%d. [%s](<%s>) · %s · %s\n", i+1, markdownText(item.Title), item.URL, markdownText(item.Source), category)
		if item.Summary != "" {
			fmt.Fprintf(&b, "   - 是什么：%s\n", markdownText(item.Summary))
		} else if item.Description != "" {
			fmt.Fprintf(&b, "   - 原始简介：%s\n", markdownText(item.Description))
		} else {
			b.WriteString("   - 原始简介：来源未提供，暂无法可靠解释。\n")
		}
		if item.HeatEvidence != "" {
			fmt.Fprintf(&b, "   - 为什么火：%s（[热度来源](<%s>)）\n", markdownText(item.HeatEvidence), item.HeatURL)
		} else {
			b.WriteString("   - 为什么推荐：你手动保存的内容，未核验热度。\n")
		}
		if item.Reason != "" && item.InboxNumber == 0 {
			fmt.Fprintf(&b, "   - 与你相关：%s\n", markdownText(item.Reason))
		}
		if item.Value != "" {
			fmt.Fprintf(&b, "   - 对你有什么用：%s\n", markdownText(item.Value))
			fmt.Fprintf(&b, "   - 第一步怎么试：%s\n", markdownText(item.FirstStep))
			fmt.Fprintf(&b, "   - 说明依据：%s\n", markdownText(item.SummaryFrom))
		} else if item.InboxNumber > 0 {
			b.WriteString("   - 对你有什么用：手动导入不自动读取原文；请结合你的备注判断。\n")
		} else {
			b.WriteString("   - 对你有什么用：暂无可靠中文说明；请核对原文。\n")
			b.WriteString("   - 第一步怎么试：先查看原文或官方文档。\n")
		}
		if item.Note != "" {
			fmt.Fprintf(&b, "   - 我的备注：%s\n", markdownText(item.Note))
		}
		fmt.Fprintf(&b, "   <!-- radar-url: %s -->\n", base64.RawURLEncoding.EncodeToString([]byte(canonicalURL(item.URL))))
		if item.InboxNumber > 0 {
			fmt.Fprintf(&b, "   <!-- radar-inbox: %d -->\n", item.InboxNumber)
		}
	}
	if len(items) == 0 {
		b.WriteString("暂无新条目。\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "---\nJev：%d 次请求，%d 输入 token。仅对公共条目标题、简述和来源做判断；手动备注未发送。\n", jevCalls, jevTokens)
	return b.String()
}

func parseInbox(entry issue) (Item, error) {
	sections := map[string]string{}
	var current string
	for _, line := range strings.Split(entry.Body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "### ") {
			current = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "### ")))
			continue
		}
		if current != "" && line != "" {
			if sections[current] != "" {
				sections[current] += " "
			}
			sections[current] += line
		}
	}
	link := strings.TrimSpace(sections["url"])
	category := strings.ToLower(strings.TrimSpace(sections["category"]))
	if err := validatePublicURL(link); err != nil {
		return Item{}, err
	}
	if category != Work && category != Life {
		return Item{}, fmt.Errorf("category must be work or life")
	}
	title := strings.TrimSpace(strings.TrimPrefix(entry.Title, "[Radar Inbox]"))
	if title == "" {
		title = link
	}
	note := truncate(sections["my note"], 300)
	if note == "_No response_" {
		note = ""
	}
	return Item{Title: truncate(title, 180), URL: link, Source: "手动导入", Category: category, InboxNumber: entry.Number, Note: note, Published: entry.CreatedAt, Score: 100, Reason: "你手动保存的内容"}, nil
}
