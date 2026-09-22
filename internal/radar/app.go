package radar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Options struct {
	ConfigPath  string
	Date        string
	DryRun      bool
	Out         io.Writer
	GitHubToken string
	Repository  string
	JevKey      string
	MiniMaxKey  string
	Now         time.Time
	HTTPClient  *http.Client
	GitHubURL   string // tests only
	JevURL      string // tests only
	MiniMaxURL  string // tests only
}

func Run(ctx context.Context, o Options) error {
	config, err := LoadConfig(o.ConfigPath)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return err
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	now := o.Now.In(location)
	date := o.Date
	if date == "" {
		date = now.Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return fmt.Errorf("invalid -date: %w", err)
	}
	if !o.DryRun && date != now.Format("2006-01-02") {
		return errors.New("real runs must use today's Asia/Shanghai date; -date is for dry-run previews")
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	client := o.HTTPClient
	if client == nil {
		client = httpClient()
	}
	var gh *githubClient
	if o.Repository != "" {
		gh, err = newGitHubClient(client, o.GitHubToken, o.Repository)
		if err != nil {
			return err
		}
		if o.GitHubURL != "" {
			gh.baseURL = o.GitHubURL
		}
	}
	if !o.DryRun && (gh == nil || o.GitHubToken == "") {
		return errors.New("GITHUB_REPOSITORY and GITHUB_TOKEN are required for a real run")
	}
	var digests, inbox []issue
	var today *issue
	if gh != nil && o.GitHubToken != "" {
		if !o.DryRun {
			if err := gh.ensureLabel(ctx, "radar-digest", "0e8a16"); err != nil {
				return err
			}
			if err := gh.ensureLabel(ctx, "radar-inbox", "1d76db"); err != nil {
				return err
			}
		}
		digests, err = gh.listIssues(ctx, "radar-digest", "all")
		if err != nil {
			return err
		}
		inbox, err = gh.listIssues(ctx, "radar-inbox", "open")
		if err != nil {
			return err
		}
		for i := range digests {
			if digests[i].Title == digestTitle(date) {
				today = &digests[i]
				break
			}
		}
	}
	if today != nil && strings.Contains(today.Body, "<!-- radar-status:complete -->") {
		if !o.DryRun {
			return finishIssues(ctx, gh, *today, digests, inbox)
		}
		_, _ = fmt.Fprintf(o.Out, "今日 Issue #%d 已完成；dry-run 不更新。\n", today.Number)
		return nil
	}
	allowJev := !o.DryRun && today == nil && o.JevKey != ""
	allowSummary := !o.DryRun && today == nil && o.MiniMaxKey != ""
	if !o.DryRun && today == nil {
		created, err := gh.createIssue(ctx, digestTitle(date), "<!-- radar-status:running -->\n正在采集；如运行中断，重试会使用规则筛选并标明降级。", []string{"radar-digest"})
		if err != nil {
			return err
		}
		today = &created
	}
	seen := seenFromIssues(digests, now)
	var candidates []Item
	var failures []string
	for _, entry := range inbox {
		item, err := parseInbox(entry)
		if err != nil {
			failures = append(failures, fmt.Sprintf("手动导入 #%d 格式错误", entry.Number))
			continue
		}
		if !seen[canonicalURL(item.URL)] {
			candidates = append(candidates, item)
		}
	}
	for _, period := range []string{"daily", "weekly"} {
		items, err := fetchTrending(ctx, client, period, now)
		if err != nil {
			failures = append(failures, "GitHub "+period+" 热榜读取失败")
			continue
		}
		candidates = append(candidates, items...)
	}
	hnItems, hnErr := fetchHackerNews(ctx, client, now)
	if hnErr != nil {
		failures = append(failures, "Hacker News 热榜读取不完整")
	}
	candidates = append(candidates, hnItems...)
	var manual []Item
	automaticByURL := make(map[string]Item)
	manualURLs := make(map[string]bool)
	for _, item := range candidates {
		key := canonicalURL(item.URL)
		if key == "" || seen[key] {
			continue
		}
		if item.InboxNumber > 0 {
			if !manualURLs[key] {
				manual = append(manual, item)
				manualURLs[key] = true
			}
			continue
		}
		if manualURLs[key] {
			continue
		}
		if previous, ok := automaticByURL[key]; !ok {
			automaticByURL[key] = item
		} else if item.HeatScore > previous.HeatScore {
			if item.Description == "" {
				item.Description = previous.Description
			}
			item.IsRepo = item.IsRepo || previous.IsRepo
			automaticByURL[key] = item
		} else {
			if previous.Description == "" {
				previous.Description = item.Description
			}
			previous.IsRepo = previous.IsRepo || item.IsRepo
			automaticByURL[key] = previous
		}
	}
	var automatic []Item
	for _, item := range automaticByURL {
		automatic = append(automatic, item)
	}
	sort.SliceStable(automatic, func(i, j int) bool {
		if automatic[i].HeatScore != automatic[j].HeatScore {
			return automatic[i].HeatScore > automatic[j].HeatScore
		}
		return automatic[i].URL < automatic[j].URL
	})
	if len(manual) >= 6 {
		automatic = nil
	}
	if len(automatic) > maxJevRequests {
		automatic = automatic[:maxJevRequests]
	}
	pageFailures := make([]bool, len(automatic))
	var pages sync.WaitGroup
	pageSlots := make(chan struct{}, 4)
	for i := range automatic {
		if automatic[i].IsRepo || automatic[i].Description != "" {
			continue
		}
		pages.Add(1)
		go func(i int) {
			defer pages.Done()
			pageSlots <- struct{}{}
			defer func() { <-pageSlots }()
			description, content, err := fetchPublicPage(ctx, client, automatic[i].URL)
			if err != nil {
				pageFailures[i] = true
				return
			}
			automatic[i].Description = description
			automatic[i].SourceText = content
		}(i)
	}
	pages.Wait()
	failedPages := 0
	for _, failed := range pageFailures {
		if failed {
			failedPages++
		}
	}
	if failedPages > 0 {
		failures = append(failures, fmt.Sprintf("%d 篇热门文章外链无法读取简介，相关性判断可能遗漏", failedPages))
	}
	degraded := !allowJev && len(automatic) > 0
	jevcalls, tokens := 0, 0
	var eligible []Item
	eligible = append(eligible, manual...)
	fallback := func(item *Item) {
		workScore, workKeyword := keywordScore(*item, config.Topics[Work].Keywords)
		lifeScore, lifeKeyword := keywordScore(*item, config.Topics[Life].Keywords)
		if workScore == 0 && lifeScore == 0 {
			item.Score = -1
			return
		}
		item.Category = Work
		keyword := workKeyword
		item.Score = workScore
		if lifeScore > workScore {
			item.Category = Life
			keyword = lifeKeyword
			item.Score = lifeScore
		}
		item.Reason = "Jev 未参与，按「" + keyword + "」主题作保守判断"
	}
	if allowJev {
		jev := newJevClient(client, o.JevKey)
		if o.JevURL != "" {
			jev.endpoint = o.JevURL
		}
		jevAvailable := true
		for i := range automatic {
			if !jevAvailable {
				fallback(&automatic[i])
				continue
			}
			if jevcalls >= maxJevRequests {
				degraded = true
				fallback(&automatic[i])
				continue
			}
			jevcalls++ // A failed request may still be billable.
			score, err := jev.evaluate(ctx, automatic[i], config.Topics)
			if err != nil {
				degraded = true
				failures = append(failures, "Jev 判断失败，后续使用保守关键词规则")
				jevAvailable = false
				fallback(&automatic[i])
				continue
			}
			tokens += score.Tokens
			if score.Work < 0.6 && score.Life < 0.6 {
				automatic[i].Score = -1
				continue
			}
			automatic[i].Category = Work
			automatic[i].Score = score.Work
			if score.Life > score.Work {
				automatic[i].Category = Life
				automatic[i].Score = score.Life
			}
			automatic[i].Reason = "Jev 判断与你的" + map[string]string{Work: "工作", Life: "学习"}[automatic[i].Category] + "方向相关"
		}
	} else {
		for i := range automatic {
			fallback(&automatic[i])
		}
	}
	for _, item := range automatic {
		if item.Score >= 0 {
			eligible = append(eligible, item)
		}
	}
	selected := rankAndSelect(eligible)
	summaryNote := "中文摘要仅依据公开来源，可能有误；重要事实请核对原文。"
	if o.DryRun {
		summaryNote = "预览不调用 MiniMax；下方显示来源原始简介。"
	} else if o.MiniMaxKey == "" {
		summaryNote = "未配置 MiniMax，未生成中文摘要；下方显示来源原始简介。"
	} else if !allowSummary {
		summaryNote = "本期为中断后的重试，不重复调用 MiniMax；未完成的条目显示原始简介。"
	}
	if allowSummary {
		mini := newMiniMaxClient(client, o.MiniMaxKey)
		if o.MiniMaxURL != "" {
			mini.endpoint = o.MiniMaxURL
		}
		miniAvailable := true
		failed := 0
		calls := 0
		var summaryProblems []string
		for i := range selected {
			item := &selected[i]
			if item.InboxNumber > 0 || !item.AllowMiniMax || !miniAvailable || calls >= maxMiniMaxRequests {
				continue
			}
			sourceText := item.SourceText
			basis := "公开网页原文"
			if item.IsRepo {
				var err error
				sourceText, err = gh.publicReadme(ctx, *item)
				if err != nil {
					failed++
					summaryProblems = append(summaryProblems, "GitHub README 读取失败")
					continue
				}
				basis = "GitHub README"
			} else if sourceText == "" {
				_, sourceText, err = fetchPublicPage(ctx, client, item.URL)
				if err != nil {
					failed++
					summaryProblems = append(summaryProblems, "公开网页读取失败")
					continue
				}
			}
			if len([]rune(sourceText)) < 80 {
				failed++
				summaryProblems = append(summaryProblems, "来源内容过短")
				continue
			}
			calls++ // Failed requests may still be billable.
			intro, value, firstStep, err := mini.summarize(ctx, *item, sourceText)
			if err != nil {
				failed++
				summaryProblems = append(summaryProblems, err.Error())
				miniAvailable = false
				continue
			}
			item.Summary, item.Value, item.FirstStep, item.SummaryFrom = intro, value, firstStep, basis
		}
		if failed > 0 || !miniAvailable {
			summaryNote = fmt.Sprintf("有条目未生成中文摘要（本次 MiniMax %d 次请求；原因：%s）；这些条目显示原始简介。", calls, strings.Join(summaryProblems, "、"))
		} else if calls == 0 {
			summaryNote = "本期没有获准交给 MiniMax 的自动条目；显示来源原始简介。"
		} else {
			summaryNote = fmt.Sprintf("中文摘要依据公开来源，由 MiniMax 生成（本次 %d 次请求）；可能有误，重要事实请核对原文。", calls)
		}
	}
	body := renderDigest(date, selected, degraded, failures, jevcalls, tokens, summaryNote)
	if o.DryRun {
		_, err = io.WriteString(o.Out, body)
		return err
	}
	if err := gh.updateIssue(ctx, today.Number, body, ""); err != nil {
		return err
	}
	today.Body = body
	_, _ = fmt.Fprintf(o.Out, "日报已写入私有 Issue #%d：%d 条，Jev %d 次。\n", today.Number, len(selected), jevcalls)
	return finishIssues(ctx, gh, *today, digests, inbox)
}

func finishIssues(ctx context.Context, gh *githubClient, today issue, digests, inbox []issue) error {
	included := make(map[int]bool)
	for _, n := range inboxNumbers(today.Body) {
		included[n] = true
	}
	for _, entry := range inbox {
		if included[entry.Number] {
			if err := gh.updateIssue(ctx, entry.Number, "", "closed"); err != nil {
				return fmt.Errorf("close imported issue #%d: %w", entry.Number, err)
			}
		}
	}
	var previous *issue
	for i := range digests {
		entry := &digests[i]
		if entry.Number == today.Number || entry.State != "open" || !strings.Contains(entry.Body, "<!-- radar-status:complete -->") {
			continue
		}
		if previous == nil || entry.CreatedAt.After(previous.CreatedAt) {
			previous = entry
		}
	}
	if previous != nil {
		if err := gh.updateIssue(ctx, previous.Number, "", "closed"); err != nil {
			return fmt.Errorf("close previous digest #%d: %w", previous.Number, err)
		}
	}
	return nil
}
