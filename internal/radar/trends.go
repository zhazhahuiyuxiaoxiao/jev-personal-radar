package radar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	trendingLimit = 25
	hnLimit       = 30
)

var periodStars = regexp.MustCompile(`(?i)([0-9][0-9,]*)\s+stars?\s+(today|this week)`)

func nodeAttr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, part := range strings.Fields(nodeAttr(n, "class")) {
		if part == class {
			return true
		}
	}
	return false
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func findNode(n *html.Node, predicate func(*html.Node) bool) *html.Node {
	if predicate(n) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findNode(child, predicate); found != nil {
			return found
		}
	}
	return nil
}

func parseTrendingHTML(r io.Reader, period string, now time.Time) ([]Item, error) {
	return parseTrendingHTMLWithLanguage(r, period, now, "")
}

func parseTrendingHTMLWithLanguage(r io.Reader, period string, now time.Time, spokenLanguage string) ([]Item, error) {
	root, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub Trending: %w", err)
	}
	var articles []*html.Node
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "article" && hasClass(n, "Box-row") {
			articles = append(articles, n)
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(root)
	var items []Item
	for index, article := range articles {
		if index >= trendingLimit {
			break
		}
		anchor := findNode(article, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && n.Parent != nil && n.Parent.Data == "h2"
		})
		if anchor == nil {
			continue
		}
		path := strings.Trim(nodeAttr(anchor, "href"), "/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(path, "?#%\\") {
			continue
		}
		match := periodStars.FindStringSubmatch(nodeText(article))
		if len(match) != 3 {
			continue
		}
		if (period == "daily" && !strings.EqualFold(match[2], "today")) || (period == "weekly" && !strings.EqualFold(match[2], "this week")) {
			continue
		}
		stars, err := strconv.Atoi(strings.ReplaceAll(match[1], ",", ""))
		if err != nil || stars <= 0 {
			continue
		}
		paragraph := findNode(article, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "p" && hasClass(n, "col-9")
		})
		description := ""
		if paragraph != nil {
			description = truncate(nodeText(paragraph), 300)
		}
		rank := index + 1
		window := "今日"
		if period == "weekly" {
			window = "本周"
		}
		heatURL := "https://github.com/trending?since=" + period
		source := "GitHub Trending"
		listName := "GitHub " + window + "榜"
		if spokenLanguage == "zh" {
			heatURL += "&spoken_language_code=zh"
			source += " 中文"
			listName = "GitHub 中文" + window + "榜"
		}
		items = append(items, Item{
			Title: parts[0] + "/" + parts[1], URL: "https://github.com/" + path,
			Description: description, IsRepo: true, AllowMiniMax: true,
			Source: source, Published: now,
			HeatScore:    float64(trendingLimit-rank+1) / trendingLimit,
			HeatEvidence: fmt.Sprintf("%s第 %d 名，%s新增 %d 星", listName, rank, window, stars),
			HeatURL:      heatURL,
		})
	}
	if len(items) == 0 {
		return nil, errors.New("GitHub Trending has no verifiable repositories")
	}
	return items, nil
}

func fetchTrending(ctx context.Context, client *http.Client, period string, now time.Time) ([]Item, error) {
	return fetchTrendingWithLanguage(ctx, client, period, now, "")
}

func fetchTrendingWithLanguage(ctx context.Context, client *http.Client, period string, now time.Time, spokenLanguage string) ([]Item, error) {
	if period != "daily" && period != "weekly" {
		return nil, errors.New("invalid trending period")
	}
	if spokenLanguage != "" && spokenLanguage != "zh" {
		return nil, errors.New("invalid trending spoken language")
	}
	endpoint := "https://github.com/trending?since=" + period
	if spokenLanguage == "zh" {
		endpoint += "&spoken_language_code=zh"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jev-personal-radar/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub Trending HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 3<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 3<<20 {
		return nil, errors.New("GitHub Trending page exceeds 3 MiB")
	}
	return parseTrendingHTMLWithLanguage(strings.NewReader(string(data)), period, now, spokenLanguage)
}

type hnStory struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Text        string `json:"text"`
	Score       int    `json:"score"`
	Descendants int    `json:"descendants"`
	Time        int64  `json:"time"`
	Dead        bool   `json:"dead"`
	Deleted     bool   `json:"deleted"`
}

func fetchHackerNews(ctx context.Context, client *http.Client, now time.Time) ([]Item, error) {
	var ids []int64
	if err := getHNJSON(ctx, client, "https://hacker-news.firebaseio.com/v0/topstories.json", &ids); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("Hacker News returned no top stories")
	}
	if len(ids) > hnLimit {
		ids = ids[:hnLimit]
	}
	type result struct {
		story hnStory
		err   error
	}
	results := make([]result, len(ids))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i].err = getHNJSON(ctx, client, fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id), &results[i].story)
		}(i, id)
	}
	wg.Wait()
	var items []Item
	failed := 0
	for i, result := range results {
		if result.err != nil {
			failed++
			continue
		}
		story := result.story
		if story.Type != "story" || story.Dead || story.Deleted || story.Score < 30 || story.Title == "" || validatePublicURL(story.URL) != nil {
			continue
		}
		published := time.Unix(story.Time, 0)
		if story.Time <= 0 || published.Before(now.Add(-48*time.Hour)) || published.After(now.Add(2*time.Hour)) {
			continue
		}
		items = append(items, Item{
			Title: truncate(story.Title, 180), URL: story.URL, Description: truncate(cleanText(story.Text), 300),
			Source: "Hacker News", IsRepo: isGitHubRepoRoot(story.URL), AllowMiniMax: true, Published: published,
			HeatScore:    float64(hnLimit-i) / hnLimit,
			HeatEvidence: fmt.Sprintf("Hacker News 热门榜第 %d 名，%d 分、%d 条评论", i+1, story.Score, story.Descendants),
			HeatURL:      fmt.Sprintf("https://news.ycombinator.com/item?id=%d", ids[i]),
		})
	}
	if failed == len(results) {
		return nil, errors.New("Hacker News stories could not be read")
	}
	if failed > 0 {
		return items, fmt.Errorf("%d Hacker News stories could not be read", failed)
	}
	return items, nil
}

func getHNJSON(ctx context.Context, client *http.Client, endpoint string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "jev-personal-radar/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Hacker News HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(dest)
}
