package radar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxFocusJevRequests = 20
const maxDailyFocus = 5

type focusRequest struct {
	issue  issue
	want   string
	source string
	oneDay bool
}

type focusReport struct {
	lines   []string
	items   []Item
	calls   int
	tokens  int
	pending int
}

func reserveFocusSlot(selected []Item, focused []Item) []Item {
	if len(focused) == 0 {
		return selected
	}
	if len(selected) == 6 {
		selected = selected[:5]
	}
	return append(selected, focused[0])
}

func parseFocus(entry issue, saved map[int]issue, repo string, now time.Time) (focusRequest, string) {
	f := formFields(entry.Body)
	n, err := savedIssueNumber(f["已收藏的 Issue"], repo)
	if err != nil {
		return focusRequest{}, "关联收藏地址无效"
	}
	parent, ok := saved[n]
	if !ok || parent.State != "open" {
		return focusRequest{}, "关联收藏已关闭或不存在"
	}
	source := strings.TrimSpace(f["原文链接"])
	if validatePublicURL(source) != nil || canonicalURL(source) != canonicalURL(formFields(parent.Body)["原文链接"]) {
		return focusRequest{}, "原文与收藏不一致"
	}
	want := strings.TrimSpace(f["想找什么"])
	if want == "" || want == "_No response_" || len([]rune(want)) > 160 || strings.ContainsAny(want, "\r\n") {
		return focusRequest{}, "搜索描述无效"
	}
	mode := strings.TrimSpace(f["搜索期限"])
	if mode != "明天一次" && mode != "长期" {
		return focusRequest{}, "搜索期限无效"
	}
	today := now.Format("2006-01-02")
	created := entry.CreatedAt.In(now.Location()).Format("2006-01-02")
	if created >= today {
		return focusRequest{}, "明日起开始"
	}
	oneDay := mode == "明天一次"
	if oneDay && entry.CreatedAt.In(now.Location()).AddDate(0, 0, 1).Format("2006-01-02") != today {
		return focusRequest{}, "一次关注已过期"
	}
	return focusRequest{issue: entry, want: want, source: source, oneDay: oneDay}, ""
}

func focusQuery(want, scope string, now time.Time) string {
	// The form asks for a capability. Keep user words as search terms but
	// prevent them from injecting GitHub search qualifiers.
	want = strings.NewReplacer(":", " ", "\"", " ", "\n", " ", "\r", " ", "、", " ", "，", " ", "。", " ").Replace(want)
	terms := strings.Fields(want)
	if scope == "readme" && len(terms) > 2 {
		terms = terms[len(terms)-2:]
	}
	want = strings.Join(terms, " ")
	return truncate(want, 160) + " in:" + scope + " is:public archived:false fork:false created:>=" + now.AddDate(0, 0, -30).Format("2006-01-02")
}

func (g *githubClient) searchFocusRepos(ctx context.Context, want string, now time.Time, excluded map[string]bool) ([]Item, bool) {
	visited := map[string]bool{}
	var candidates []Item
	failed := false
	for _, scope := range []string{"name,description", "readme"} {
		path := "/search/repositories?q=" + url.QueryEscape(focusQuery(want, scope, now)) + "&sort=updated&order=desc&per_page=20"
		var response struct {
			Incomplete bool           `json:"incomplete_results"`
			Items      []searchedRepo `json:"items"`
		}
		if err := g.request(ctx, http.MethodGet, path, nil, &response); err != nil {
			failed = true
			continue
		}
		if response.Incomplete {
			failed = true
		}
		for _, repo := range response.Items {
			key := canonicalURL(repo.HTMLURL)
			if key == "" || visited[key] || excluded[key] || repo.Archived || repo.Fork || repo.Private || !isGitHubRepoRoot(repo.HTMLURL) || repo.CreatedAt.IsZero() || repo.CreatedAt.After(now) || repo.CreatedAt.Before(now.AddDate(0, 0, -30)) {
				continue
			}
			visited[key] = true
			growth, err := g.recentStarGrowth(ctx, repo.HTMLURL, now)
			if err != nil {
				failed = true
				continue
			}
			if growth <= 0 {
				continue
			}
			parsed, _ := url.Parse(repo.HTMLURL)
			candidates = append(candidates, Item{
				Title: repo.FullName, URL: repo.HTMLURL, Description: truncate(repo.Description, 300),
				IsRepo: true, AllowMiniMax: true, Source: "搜索关注", Published: repo.CreatedAt,
				HeatScore: float64(growth), HeatEvidence: fmt.Sprintf("近 7 天新增约 %d 星；累计 %d 星；创建于 %s", growth, repo.Stars, repo.CreatedAt.Format("2006-01-02")),
				HeatURL: "https://api.github.com/repos" + parsed.EscapedPath() + "/stargazers/history",
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].HeatScore > candidates[j].HeatScore })
	return candidates, failed
}

func runFocusSearch(ctx context.Context, gh *githubClient, client *http.Client, focusIssues, savedIssues []issue, digests []issue, current []Item, now time.Time, key, endpoint string) focusReport {
	r := focusReport{}
	saved := map[int]issue{}
	for _, entry := range savedIssues {
		saved[entry.Number] = entry
	}
	var active []focusRequest
	for _, entry := range focusIssues {
		request, status := parseFocus(entry, saved, gh.repo, now)
		if status != "" {
			r.lines = append(r.lines, fmt.Sprintf("- [关注 #%d](<https://github.com/%s/issues/%d>)：%s。", entry.Number, gh.repo, entry.Number, status))
			continue
		}
		active = append(active, request)
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].oneDay != active[j].oneDay {
			return active[i].oneDay
		}
		return active[i].issue.CreatedAt.Before(active[j].issue.CreatedAt)
	})
	if len(active) > maxDailyFocus {
		r.pending = len(active) - maxDailyFocus
		active = active[:maxDailyFocus]
	}
	excluded := seenFromIssues(digests, now)
	for _, item := range current {
		excluded[canonicalURL(item.URL)] = true
	}
	jev := newJevClient(client, key)
	jevAvailable := key != ""
	jevUnavailableReason := "Jev 未配置"
	if endpoint != "" {
		jev.endpoint = endpoint
	}
	for _, request := range active {
		entry := request.issue
		prefix := fmt.Sprintf("- [关注 #%d](<https://github.com/%s/issues/%d>)：", entry.Number, gh.repo, entry.Number)
		if !jevAvailable {
			r.lines = append(r.lines, prefix+"未完成（"+jevUnavailableReason+"）。")
			continue
		}
		if r.calls >= maxFocusJevRequests {
			r.lines = append(r.lines, prefix+"未完成（今日 Jev 重点搜索上限已用完）。")
			continue
		}
		localExcluded := make(map[string]bool, len(excluded)+1)
		for k, v := range excluded {
			localExcluded[k] = v
		}
		localExcluded[canonicalURL(request.source)] = true
		candidates, failed := gh.searchFocusRepos(ctx, request.want, now, localExcluded)
		matched := false
		shown := false
		modelFailed := false
		for _, item := range candidates {
			if r.calls >= maxFocusJevRequests {
				modelFailed = true
				break
			}
			r.calls++ // A failed call may still be billable.
			score, err := jev.evaluateFocus(ctx, item, request.want)
			if err != nil {
				modelFailed = true
				jevAvailable = false
				jevUnavailableReason = "Jev 请求失败"
				break
			}
			r.tokens += score.Tokens
			if score.Work < 0.6 && score.Life < 0.6 {
				continue
			}
			item.Category, item.Score = Work, score.Work
			if score.Life > score.Work {
				item.Category, item.Score = Life, score.Life
			}
			item.Reason = fmt.Sprintf("与你的搜索关注 #%d 相关", entry.Number)
			if len(r.items) == 0 {
				r.items = append(r.items, item)
				shown = true
			}
			excluded[canonicalURL(item.URL)] = true
			matched = true
			break
		}
		if failed || modelFailed {
			r.lines = append(r.lines, prefix+"未完成（搜索、增星核验或 Jev 失败）。")
		} else if shown {
			r.lines = append(r.lines, prefix+"找到符合条件的新项目，已纳入本期日报。")
		} else if matched {
			r.lines = append(r.lines, prefix+"找到符合条件的新项目；本期重点结果名额已满。")
		} else {
			r.lines = append(r.lines, prefix+"本次未找到符合条件的新项目。")
		}
	}
	return r
}

func formatFocusReport(r focusReport) string {
	var b strings.Builder
	b.WriteString("## 搜索关注\n\n")
	if len(r.lines) == 0 {
		b.WriteString("本期没有待搜索的关注。\n")
	}
	for _, line := range r.lines {
		b.WriteString(line + "\n")
	}
	if r.pending > 0 {
		fmt.Fprintf(&b, "\n另有 %d 条关注因每天最多处理 5 条而未处理。\n", r.pending)
	}
	fmt.Fprintf(&b, "\n重点搜索 Jev：%d 次请求，%d 输入 token。\n\n", r.calls, r.tokens)
	return b.String()
}
