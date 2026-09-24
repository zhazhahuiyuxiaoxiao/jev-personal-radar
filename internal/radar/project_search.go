package radar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxProjectJevRequests = 30
const maxProjectInspections = 60

type searchedRepo struct {
	FullName    string    `json:"full_name"`
	HTMLURL     string    `json:"html_url"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	Stars       int       `json:"stargazers_count"`
	Archived    bool      `json:"archived"`
	Fork        bool      `json:"fork"`
	Private     bool      `json:"private"`
}

func (g *githubClient) findProjectCandidates(ctx context.Context, now time.Time, seen, considered map[string]bool, limit int) ([]Item, []string) {
	cutoff := now.AddDate(-1, 0, 0).Format("2006-01-02")
	oldestCutoff := now.AddDate(-2, 0, 0).Format("2006-01-02")
	queries := []string{
		"全栈 项目 in:name,description is:public archived:false fork:false created:>=" + cutoff,
		"前端 后端 数据库 部署 in:readme is:public archived:false fork:false created:>=" + cutoff,
		"full-stack project in:name,description is:public archived:false fork:false created:>=" + cutoff,
		"前端 后端 数据库 部署 in:readme is:public archived:false fork:false created:" + oldestCutoff + ".." + cutoff + " stars:>=1000",
		"前端 后端 数据库 部署 in:readme is:public archived:false fork:false created:<" + oldestCutoff + " stars:>=5000",
	}
	queryInspectionLimits := []int{15, 15, 10, 10, 10}
	var candidates []Item
	var failures []string
	inspected := 0
	visited := map[string]bool{}
	for queryIndex, query := range queries {
		if len(candidates) >= limit || inspected >= maxProjectInspections {
			break
		}
		inspectedInQuery := 0
		var response struct {
			Incomplete bool           `json:"incomplete_results"`
			Items      []searchedRepo `json:"items"`
		}
		path := "/search/repositories?q=" + url.QueryEscape(query) + "&per_page=30"
		if err := g.request(ctx, http.MethodGet, path, nil, &response); err != nil {
			failures = append(failures, "GitHub 项目搜索失败")
			continue
		}
		if response.Incomplete {
			failures = append(failures, "GitHub 项目搜索结果不完整")
		}
		for _, repo := range response.Items {
			if len(candidates) >= limit || inspected >= maxProjectInspections || inspectedInQuery >= queryInspectionLimits[queryIndex] {
				break
			}
			key := canonicalURL(repo.HTMLURL)
			if key == "" || visited[key] || seen[key] || considered[key] || repo.Archived || repo.Fork || repo.Private || !isGitHubRepoRoot(repo.HTMLURL) {
				continue
			}
			visited[key] = true
			inspected++
			inspectedInQuery++
			if repo.CreatedAt.IsZero() || repo.CreatedAt.After(now) {
				continue
			}
			if repo.CreatedAt.Before(now.AddDate(-1, 0, 0)) && repo.Stars < minOlderProjectStars {
				continue
			}
			if repo.CreatedAt.Before(now.AddDate(-2, 0, 0)) && repo.Stars < minOldestProjectStars {
				continue
			}
			item := Item{Title: repo.FullName, URL: repo.HTMLURL, Description: truncate(repo.Description, 300), IsRepo: true, AllowMiniMax: true, Source: "GitHub 项目搜索", Published: repo.CreatedAt}
			readme, err := g.publicReadmeRaw(ctx, item)
			if err != nil {
				failures = append(failures, "部分 GitHub README 无法读取")
				continue
			}
			if !hasFullStackReadmeEvidence(readme) {
				continue
			}
			growth, err := g.recentStarGrowth(ctx, repo.HTMLURL, now)
			if err != nil {
				failures = append(failures, "部分 GitHub 增星历史无法读取")
				continue
			}
			if growth <= 0 {
				continue
			}
			item.HeatScore = float64(growth)
			item.HeatEvidence = fmt.Sprintf("近 7 天新增约 %d 星；累计 %d 星；创建于 %s", growth, repo.Stars, repo.CreatedAt.Format("2006-01-02"))
			repoPath, _ := url.Parse(repo.HTMLURL) // Validated as a GitHub repository root above.
			item.HeatURL = "https://api.github.com/repos" + repoPath.EscapedPath() + "/stargazers/history"
			readmeSummary := readmeEvidenceSummary(readme)
			if item.Description == "" {
				item.Description = "README：" + readmeSummary
			} else {
				item.Description = truncate(item.Description+"；README："+readmeSummary, 500)
			}
			candidates = append(candidates, item)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].HeatScore != candidates[j].HeatScore {
			return candidates[i].HeatScore > candidates[j].HeatScore
		}
		return candidates[i].Published.After(candidates[j].Published)
	})
	return candidates, dedupeStrings(failures)
}

const minOlderProjectStars = 1000
const minOldestProjectStars = 5000

func hasFullStackReadmeEvidence(readme string) bool {
	text := strings.ToLower(readme)
	groups := [][]string{
		{"前端", "frontend", "front-end", "vue", "react", "next.js", "angular"},
		{"后端", "backend", "back-end", "服务端", "spring boot", "nestjs", "express", "django", "fastapi"},
		{"数据库", "database", "mysql", "postgres", "sqlite", "mongodb", "redis"},
		{"部署", "运行", "启动", "安装", "deploy", "quick start", "getting started", "docker compose", "docker-compose", "run locally"},
	}
	if len([]rune(readme)) < 200 {
		return false
	}
	for _, group := range groups {
		found := false
		for _, term := range group {
			if strings.Contains(text, term) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func readmeEvidenceSummary(readme string) string {
	var lines []string
	for _, line := range strings.Split(readme, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#-* "))
		if len([]rune(line)) < 15 || strings.HasPrefix(line, "![") || strings.HasPrefix(line, "[") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "前端") || strings.Contains(lower, "后端") || strings.Contains(lower, "数据库") || strings.Contains(lower, "frontend") || strings.Contains(lower, "backend") || strings.Contains(lower, "database") || strings.Contains(lower, "deploy") || strings.Contains(lower, "部署") {
			lines = append(lines, truncate(line, 120))
		}
		if len(lines) == 3 {
			break
		}
	}
	if len(lines) == 0 {
		return truncate(readme, 250)
	}
	return truncate(strings.Join(lines, "；"), 300)
}

func (g *githubClient) recentStarGrowth(ctx context.Context, repoURL string, now time.Time) (int, error) {
	u, err := url.Parse(repoURL)
	if err != nil || !isGitHubRepoRoot(repoURL) {
		return 0, errors.New("invalid GitHub repository URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/stargazers/history?per_page=3"
	var weeks []struct {
		Week int64 `json:"week"`
		Days []int `json:"days"`
	}
	if err := g.request(ctx, http.MethodGet, path, nil, &weeks); err != nil {
		return 0, err
	}
	cutoff := now.AddDate(0, 0, -7)
	growth := 0
	for _, week := range weeks {
		if len(week.Days) != 7 {
			return 0, errors.New("invalid GitHub star history")
		}
		for day, count := range week.Days {
			date := time.Unix(week.Week, 0).AddDate(0, 0, day)
			if !date.Before(cutoff) && !date.After(now) {
				growth += count
			}
		}
	}
	return growth, nil
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
