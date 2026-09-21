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

func renderDigest(date string, items []Item, degraded bool, failures []string, jevCalls, jevTokens int) string {
	var b strings.Builder
	b.WriteString("<!-- radar-status:complete -->\n")
	fmt.Fprintf(&b, "# 个人信息雷达 · %s\n\n", date)
	if degraded {
		b.WriteString("⚠️ 本期为降级结果：Jev 未完成全部筛选，部分或全部条目使用规则筛选。\n\n")
	} else if jevCalls == 0 {
		b.WriteString("本期仅有手动导入条目，无需调用 Jev。\n\n")
	} else {
		b.WriteString("筛选方式：Jev 结构化判断；标题与链接来自原始来源，中文提示不是文章摘要。\n\n")
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
	for _, category := range []string{Work, Life} {
		if category == Work {
			b.WriteString("## 工作\n\n")
		} else {
			b.WriteString("## 学习与效率\n\n")
		}
		count := 0
		for _, item := range items {
			if item.Category != category {
				continue
			}
			count++
			fmt.Fprintf(&b, "%d. [%s](<%s>) · %s\n", count, markdownText(item.Title), item.URL, markdownText(item.Source))
			fmt.Fprintf(&b, "   - 入选原因：%s\n", markdownText(item.Reason))
			fmt.Fprintf(&b, "   - 下一步：%s\n", markdownText(item.Action))
			if item.Note != "" {
				fmt.Fprintf(&b, "   - 我的备注：%s\n", markdownText(item.Note))
			}
			fmt.Fprintf(&b, "   <!-- radar-url: %s -->\n", base64.RawURLEncoding.EncodeToString([]byte(canonicalURL(item.URL))))
			if item.InboxNumber > 0 {
				fmt.Fprintf(&b, "   <!-- radar-inbox: %d -->\n", item.InboxNumber)
			}
		}
		if count == 0 {
			b.WriteString("暂无新条目。\n")
		}
		b.WriteString("\n")
	}
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
	return Item{Title: truncate(title, 180), URL: link, Source: "手动导入", Category: category, InboxNumber: entry.Number, Note: note, Published: entry.CreatedAt, Score: 100, Reason: "你手动保存的内容", Action: "打开链接，按自己的备注决定是否实践"}, nil
}
