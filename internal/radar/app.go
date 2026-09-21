package radar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
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
	Now         time.Time
	HTTPClient  *http.Client
	GitHubURL   string // tests only
	JevURL      string // tests only
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
	for _, feed := range config.Feeds {
		items, err := fetchFeed(ctx, client, feed, now)
		if err != nil {
			failures = append(failures, feed.Name+" 读取失败")
			continue
		}
		candidates = append(candidates, items...)
	}
	if gh != nil {
		for category, topic := range config.Topics {
			for _, query := range topic.GitHubQueries {
				items, err := gh.searchRepositories(ctx, query, category, now)
				if err != nil {
					failures = append(failures, "GitHub "+category+" 搜索失败")
					continue
				}
				candidates = append(candidates, items...)
			}
		}
	} else {
		failures = append(failures, "未设置 GITHUB_REPOSITORY，跳过 GitHub 搜索")
	}
	var automatic []Item
	unique := make(map[string]bool)
	for _, item := range candidates {
		key := canonicalURL(item.URL)
		if key == "" || seen[key] || unique[key] {
			continue
		}
		unique[key] = true
		if item.InboxNumber > 0 {
			automatic = append(automatic, item)
			continue
		}
		score, keyword := keywordScore(item, config.Topics[item.Category].Keywords)
		if score == 0 {
			continue
		}
		item.Score = score
		item.Reason = "与你关注的「" + keyword + "」主题相关；是否适用需查看原文"
		item.Action = "打开原文，判断是否值得收藏或尝试"
		automatic = append(automatic, item)
	}
	sort.SliceStable(automatic, func(i, j int) bool {
		if automatic[i].InboxNumber != automatic[j].InboxNumber {
			return automatic[i].InboxNumber > automatic[j].InboxNumber
		}
		return automatic[i].Score > automatic[j].Score
	})
	var bounded []Item
	counts := map[string]int{}
	for _, item := range automatic {
		if counts[item.Category] >= 10 {
			continue
		}
		counts[item.Category]++
		bounded = append(bounded, item)
	}
	degraded := !allowJev
	jevcalls, tokens := 0, 0
	if allowJev {
		jev := newJevClient(client, o.JevKey)
		if o.JevURL != "" {
			jev.endpoint = o.JevURL
		}
		jevAvailable := true
		for i := range bounded {
			if bounded[i].InboxNumber > 0 {
				continue
			}
			if !jevAvailable {
				continue
			}
			if jevcalls >= maxJevRequests {
				degraded = true
				break
			}
			jevcalls++ // A failed request may still be billable.
			score, err := jev.evaluate(ctx, bounded[i], config.Topics[bounded[i].Category].Keywords)
			if err != nil {
				degraded = true
				failures = append(failures, "Jev 判断失败，使用规则分数")
				jevAvailable = false
				continue
			}
			tokens += score.Tokens
			if score.Relevant < 0.5 || score.Actionable < 0.45 {
				bounded[i].Score = -1
				continue
			}
			bounded[i].Score = score.Relevant*10 + score.Actionable*5 + bounded[i].Score
		}
	}
	var eligible []Item
	for _, item := range bounded {
		if item.Score >= 0 {
			eligible = append(eligible, item)
		}
	}
	selected := rankAndSelect(eligible)
	body := renderDigest(date, selected, degraded, failures, jevcalls, tokens)
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
