package radar

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var digestItemHeading = regexp.MustCompile(`^[1-6]\. \[(.+)\]\(<(https?://[^>]+)>\) · `)

type digestItemBlock struct {
	lines   []string
	item    Item
	pending bool
}

// retrySummaries only changes the explanation lines of an existing completed
// digest. Its original selection, heat evidence, notes, and Jev usage remain.
func retrySummaries(ctx context.Context, gh *githubClient, client *http.Client, today issue, key, miniURL string, out io.Writer) error {
	if today.State != "open" {
		return errors.New("today's digest is not open")
	}
	lines := strings.Split(today.Body, "\n")
	heading, footer, note := -1, -1, -1
	for i, line := range lines {
		if line == "## 今日热点" {
			heading = i
		}
		if heading >= 0 && i > heading && line == "---" {
			footer = i
			break
		}
	}
	if heading < 0 || footer < 0 {
		return errors.New("today's digest has an unsupported format")
	}
	for i := 0; i < heading; i++ {
		line := lines[i]
		if strings.HasPrefix(line, "有条目未生成中文摘要（") || strings.HasPrefix(line, "未配置 MiniMax，") || strings.HasPrefix(line, "本期为中断后的重试，") || strings.HasPrefix(line, "中文摘要依据公开来源，") || strings.HasPrefix(line, "今日已补生成 ") {
			if note >= 0 {
				return errors.New("today's digest has multiple summary notes")
			}
			note = i
		}
	}
	if note < 0 {
		return errors.New("today's digest has no recognizable summary note")
	}
	var starts []int
	for i := heading + 1; i < footer; i++ {
		if digestItemHeading.MatchString(lines[i]) {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 || len(starts) > maxMiniMaxRequests {
		return errors.New("today's digest has no supported summary targets")
	}
	starts = append(starts, footer)
	blocks := make([]digestItemBlock, len(starts)-1)
	pending := 0
	for i := range blocks {
		block, err := parseDigestItemBlock(lines[starts[i]:starts[i+1]])
		if err != nil {
			return err
		}
		blocks[i] = block
		if block.pending {
			pending++
		}
	}
	if pending == 0 {
		_, _ = fmt.Fprintf(out, "今日私有 Issue #%d 已有中文说明，无需再次调用 MiniMax。\n", today.Number)
		return nil
	}
	mini := newMiniMaxClient(client, key)
	if miniURL != "" {
		mini.endpoint = miniURL
	}
	requests, completed := 0, 0
	var problems []string
	for i := range blocks {
		block := &blocks[i]
		if !block.pending {
			continue
		}
		var sourceText, basis string
		var err error
		if block.item.IsRepo {
			sourceText, err = gh.publicReadme(ctx, block.item)
			basis = "GitHub README"
		} else {
			_, sourceText, err = fetchPublicPage(ctx, client, block.item.URL)
			basis = "公开网页原文"
		}
		if err != nil {
			problems = append(problems, "公开原文无法读取")
			continue
		}
		if len([]rune(sourceText)) < 80 {
			problems = append(problems, "公开原文过短")
			continue
		}
		requests++ // A failed request may still be billable.
		intro, value, firstStep, err := mini.summarize(ctx, block.item, sourceText)
		if err != nil {
			problems = append(problems, err.Error())
			break // An invalid or exhausted key should not be tried on every item.
		}
		block.lines = addDigestSummary(block.lines, intro, value, firstStep, basis)
		completed++
	}
	if completed == 0 {
		return fmt.Errorf("today's digest was preserved: MiniMax generated no explanation (%d requests; %s)", requests, strings.Join(problems, "、"))
	}
	var rebuilt []string
	rebuilt = append(rebuilt, lines[:starts[0]]...)
	for _, block := range blocks {
		rebuilt = append(rebuilt, block.lines...)
	}
	rebuilt = append(rebuilt, lines[footer:]...)
	if completed == pending {
		rebuilt[note] = fmt.Sprintf("中文摘要依据公开来源，由 MiniMax 补生成（本次 %d 次请求）；可能有误，重要事实请核对原文。", requests)
	} else {
		rebuilt[note] = fmt.Sprintf("今日已补生成 %d 条中文说明（本次 MiniMax %d 次请求）；另有 %d 条未生成：%s。", completed, requests, pending-completed, markdownText(strings.Join(problems, "、")))
	}
	latest, err := gh.getIssue(ctx, today.Number)
	if err != nil {
		return err
	}
	if latest.Body != today.Body || latest.State != today.State {
		return errors.New("today's digest changed during summary generation; no update was made")
	}
	if err := gh.updateIssue(ctx, today.Number, strings.Join(rebuilt, "\n"), ""); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "已为私有 Issue #%d 补生成 %d 条中文说明（MiniMax %d 次请求）。\n", today.Number, completed, requests)
	return nil
}

func parseDigestItemBlock(lines []string) (digestItemBlock, error) {
	if len(lines) == 0 {
		return digestItemBlock{}, errors.New("empty digest item")
	}
	match := digestItemHeading.FindStringSubmatch(lines[0])
	if match == nil || validatePublicURL(match[2]) != nil {
		return digestItemBlock{}, errors.New("digest item has an invalid public link")
	}
	body := strings.Join(lines, "\n")
	marker := urlMarker.FindStringSubmatch(body)
	if len(marker) != 2 {
		return digestItemBlock{}, errors.New("digest item has no URL marker")
	}
	markedURL, err := base64.RawURLEncoding.DecodeString(marker[1])
	if err != nil || string(markedURL) != canonicalURL(match[2]) {
		return digestItemBlock{}, errors.New("digest item URL marker does not match its link")
	}
	block := digestItemBlock{lines: lines, item: Item{Title: match[1], URL: match[2], IsRepo: isGitHubRepoRoot(match[2])}}
	if inboxMarker.MatchString(body) || strings.Contains(body, "   - 是什么：") {
		return block, nil
	}
	if !strings.Contains(body, "   - 原始简介：") || !strings.Contains(body, "   - 对你有什么用：暂无可靠中文说明；请核对原文。") || !strings.Contains(body, "   - 第一步怎么试：先查看原文或官方文档。") {
		return digestItemBlock{}, errors.New("digest item has an unsupported explanation format")
	}
	block.pending = true
	return block, nil
}

func addDigestSummary(lines []string, intro, value, firstStep, basis string) []string {
	var result []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "   - 原始简介："):
			result = append(result, "   - 是什么："+markdownText(intro))
		case line == "   - 对你有什么用：暂无可靠中文说明；请核对原文。":
			result = append(result, "   - 对你有什么用："+markdownText(value))
		case line == "   - 第一步怎么试：先查看原文或官方文档。":
			result = append(result, "   - 第一步怎么试："+markdownText(firstStep), "   - 说明依据："+markdownText(basis))
		default:
			result = append(result, line)
		}
	}
	return result
}
