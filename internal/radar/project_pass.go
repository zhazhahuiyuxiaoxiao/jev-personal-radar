package radar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var digestJevUsage = regexp.MustCompile(`Jev：([0-9]+) 次请求，([0-9]+) 输入 token`)

type secondPassResult struct {
	Items    []Item
	Calls    int
	Tokens   int
	Degraded bool
	Failures []string
}

func scoreProjectCandidates(ctx context.Context, client *http.Client, candidates []Item, config Config, key, endpoint string) secondPassResult {
	result := secondPassResult{Degraded: key == "" && len(candidates) > 0}
	jev := newJevClient(client, key)
	if endpoint != "" {
		jev.endpoint = endpoint
	}
	jevAvailable := key != ""
	for _, item := range candidates {
		if !jevAvailable {
			keywordFallback(&item, config)
		} else {
			result.Calls++ // A failed request may still be billable.
			score, err := jev.evaluate(ctx, item, config.Topics)
			if err != nil {
				result.Degraded = true
				result.Failures = append(result.Failures, "Jev 项目补查判断失败，后续使用保守关键词规则")
				jevAvailable = false
				keywordFallback(&item, config)
			} else {
				result.Tokens += score.Tokens
				if score.Work < 0.6 && score.Life < 0.6 {
					continue
				}
				item.Category, item.Score = Work, score.Work
				if score.Life > score.Work {
					item.Category, item.Score = Life, score.Life
				}
				item.Reason = "Jev 判断与你的" + map[string]string{Work: "工作", Life: "学习"}[item.Category] + "方向相关"
			}
		}
		if item.Score >= 0 {
			result.Items = append(result.Items, item)
		}
	}
	return result
}

func keywordFallback(item *Item, config Config) {
	workScore, workKeyword := keywordScore(*item, config.Topics[Work].Keywords)
	lifeScore, lifeKeyword := keywordScore(*item, config.Topics[Life].Keywords)
	if workScore == 0 && lifeScore == 0 {
		item.Score = -1
		return
	}
	item.Category, item.Score = Work, workScore
	keyword := workKeyword
	if lifeScore > workScore {
		item.Category, item.Score, keyword = Life, lifeScore, lifeKeyword
	}
	item.Reason = "Jev 未参与，按「" + keyword + "」主题作保守判断"
}

func retryEmptyDigest(ctx context.Context, gh *githubClient, client *http.Client, config Config, o Options, today issue, digests []issue, now time.Time) error {
	if today.State != "open" || !strings.Contains(today.Body, "<!-- radar-status:complete -->") || !strings.Contains(today.Body, "## 今日热点\n\n暂无新条目。") {
		return errors.New("today's digest is not a completed empty digest")
	}
	if strings.Contains(today.Body, "<!-- radar-chinese-pass:") || strings.Contains(today.Body, "<!-- radar-project-pass:") {
		return errors.New("today's project retry was already started; check the existing Issue and Actions run")
	}
	if o.JevKey == "" {
		return errors.New("TYPESAFE_API_KEY is required for project retry")
	}
	usage := digestJevUsage.FindStringSubmatch(today.Body)
	if len(usage) != 3 {
		return errors.New("today's digest has no recognizable Jev usage")
	}
	previousCalls, err := strconv.Atoi(usage[1])
	if err != nil || previousCalls < 0 || previousCalls > maxJevRequests {
		return errors.New("today's Jev usage exceeds the first-pass limit")
	}
	previousTokens, err := strconv.Atoi(usage[2])
	if err != nil {
		return errors.New("today's Jev token usage is invalid")
	}
	seen := seenFromIssues(digests, now)
	candidates, failures := gh.findProjectCandidates(ctx, now, seen, nil, maxProjectJevRequests)
	latest, err := gh.getIssue(ctx, today.Number)
	if err != nil {
		return err
	}
	if latest.Body != today.Body || latest.State != today.State {
		return errors.New("today's digest changed before project retry; no update was made")
	}
	runningBody := strings.Replace(today.Body, "<!-- radar-status:complete -->", "<!-- radar-status:complete -->\n<!-- radar-project-pass:running -->\n\n项目补查进行中；若运行中断，请核对 Actions。", 1)
	if err := gh.updateIssue(ctx, today.Number, runningBody, ""); err != nil {
		return err
	}
	result := scoreProjectCandidates(ctx, client, candidates, config, o.JevKey, o.JevURL)
	result.Failures = append(failures, result.Failures...)
	selected := rankAndSelect(result.Items)
	summaryNote := "本期没有获准交给 MiniMax 的自动条目；显示来源原始简介。"
	if len(selected) > 0 && o.MiniMaxKey == "" {
		summaryNote = "未配置 MiniMax，未生成中文摘要；下方显示来源原始简介。"
	} else if len(selected) > 0 {
		summaryNote = "有条目未生成中文摘要（本次 MiniMax 0 次请求；原因：项目补查刚完成）；这些条目显示原始简介。"
	}
	body := renderDigest(now.Format("2006-01-02"), selected, result.Degraded, result.Failures, previousCalls+result.Calls, previousTokens+result.Tokens, summaryNote)
	body = strings.Replace(body, "<!-- radar-status:complete -->", "<!-- radar-status:complete -->\n<!-- radar-project-pass:complete -->", 1)
	body = strings.Replace(body, "## 今日热点", "首轮 0 条后，已补查 GitHub 全栈项目（中文优先，核对 README 与近期增星）。\n\n## 今日热点", 1)
	if err := gh.updateIssue(ctx, today.Number, body, ""); err != nil {
		return fmt.Errorf("project retry completed but could not update Issue #%d: %w", today.Number, err)
	}
	_, _ = fmt.Fprintf(o.Out, "项目补查已更新私有 Issue #%d：%d 条，本次 Jev %d 次。\n", today.Number, len(selected), result.Calls)
	if len(selected) > 0 && o.MiniMaxKey != "" {
		today.Body = body
		return retrySummaries(ctx, gh, client, today, o.MiniMaxKey, o.MiniMaxURL, o.Out)
	}
	return nil
}
