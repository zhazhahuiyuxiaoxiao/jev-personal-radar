package radar

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseRSSAndAtom(t *testing.T) {
	now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	feed := Feed{Name: "Go Blog", Category: Work}
	rss := []byte(`<rss><channel><item><title>Go 1.27</title><link>https://go.dev/blog/go127</link><description>Go backend update</description><pubDate>Mon, 21 Sep 2026 00:00:00 +0000</pubDate></item></channel></rss>`)
	items, err := parseFeed(rss, feed, now)
	if err != nil || len(items) != 1 || items[0].Title != "Go 1.27" {
		t.Fatalf("RSS parse: items=%v err=%v", items, err)
	}
	atom := []byte(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>Learning</title><link href="https://example.com/learning" rel="alternate"/><summary>Productivity &amp; practice</summary><updated>2026-09-21T00:00:00Z</updated></entry></feed>`)
	items, err = parseFeed(atom, Feed{Name: "Ness Labs", Category: Life}, now)
	if err != nil || len(items) != 1 || items[0].Description != "Productivity & practice" {
		t.Fatalf("Atom parse: items=%v err=%v", items, err)
	}
	fullText := "This public article explains a repeatable learning method with spaced practice, review intervals, and examples that readers can use in daily study."
	rss = []byte(`<rss xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><item><title>Learning</title><link>https://example.com/learning</link><description>Short description</description><content:encoded><![CDATA[` + fullText + `]]></content:encoded></item></channel></rss>`)
	items, err = parseFeed(rss, feed, now)
	if err != nil || len(items) != 1 || items[0].SourceText != fullText || items[0].Description != "Short description" {
		t.Fatalf("RSS full text: items=%v err=%v", items, err)
	}
}

func TestGitHubSearchExcludesPrivateRepositories(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return testResponse(200, `{"items":[{"full_name":"o/private","html_url":"https://github.com/o/private","description":"secret","private":true},{"full_name":"o/public","html_url":"https://github.com/o/public","description":"public","private":false}]}`), nil
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/runtime")
	items, err := gh.searchRepositories(context.Background(), "go", Work, time.Now())
	if err != nil || len(items) != 1 || items[0].Title != "o/public" {
		t.Fatalf("private repository escaped filtering: %+v %v", items, err)
	}
}

func TestTrendingParserRequiresVerifiableHeat(t *testing.T) {
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	items, err := parseTrendingHTML(strings.NewReader(testTrendingHTML("example/new-tool", "A new developer tool", "1,234", "daily")), "daily", now)
	if err != nil || len(items) != 1 || items[0].HeatScore != 1 || !strings.Contains(items[0].HeatEvidence, "今日新增 1234 星") || items[0].URL != "https://github.com/example/new-tool" {
		t.Fatalf("trending parse: %+v %v", items, err)
	}
	if _, err := parseTrendingHTML(strings.NewReader(`<html><article class="Box-row"><h2><a href="/o/r">o/r</a></h2></article></html>`), "weekly", now); err == nil {
		t.Fatal("accepted a trending page without recent star evidence")
	}
	withBadFirst := `<article class="Box-row"><h2><a href="/bad/repo">bad/repo</a></h2></article>` + testTrendingHTML("example/second", "Useful", "900", "weekly")
	items, err = parseTrendingHTML(strings.NewReader(withBadFirst), "weekly", now)
	if err != nil || len(items) != 1 || !strings.Contains(items[0].HeatEvidence, "第 2 名") {
		t.Fatalf("trending rank after skipped article: %+v %v", items, err)
	}
}

func TestLiveGitHubTrendingOptional(t *testing.T) {
	if os.Getenv("RADAR_LIVE_TRENDING") != "1" {
		t.Skip("set RADAR_LIVE_TRENDING=1 for a live source check")
	}
	items, err := fetchTrending(context.Background(), httpClient(), "weekly", time.Now())
	if err != nil || len(items) == 0 {
		t.Fatalf("live GitHub Trending: %d items, %v", len(items), err)
	}
}

func TestLiveHackerNewsOptional(t *testing.T) {
	if os.Getenv("RADAR_LIVE_HN") != "1" {
		t.Skip("set RADAR_LIVE_HN=1 for a live source check")
	}
	items, err := fetchHackerNews(context.Background(), httpClient(), time.Now())
	if err != nil || len(items) == 0 {
		t.Fatalf("live Hacker News: %d items, %v", len(items), err)
	}
}

func TestHackerNewsSkipsColdAndOldStories(t *testing.T) {
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v0/topstories.json":
			return testResponse(200, `[1,2,3]`), nil
		case "/v0/item/1.json":
			return testResponse(200, fmt.Sprintf(`{"type":"story","title":"Unknown launch","url":"https://example.org/new","score":120,"descendants":31,"time":%d}`, now.Unix())), nil
		case "/v0/item/2.json":
			return testResponse(200, fmt.Sprintf(`{"type":"story","title":"Cold","url":"https://example.org/cold","score":5,"time":%d}`, now.Unix())), nil
		case "/v0/item/3.json":
			return testResponse(200, fmt.Sprintf(`{"type":"story","title":"Old","url":"https://example.org/old","score":500,"time":%d}`, now.Add(-72*time.Hour).Unix())), nil
		default:
			return testResponse(404, `{}`), nil
		}
	})}
	items, err := fetchHackerNews(context.Background(), client, now)
	if err != nil || len(items) != 1 || items[0].Title != "Unknown launch" || !strings.Contains(items[0].HeatEvidence, "120 分") {
		t.Fatalf("HN filtering: %+v %v", items, err)
	}
}

func TestPublicPageExtractionAndNetworkGuard(t *testing.T) {
	description, content, err := publicPageText([]byte(`<html><head><meta name="description" content="Plain-language overview"></head><body><nav>menu noise</nav><main><h1>New API</h1><p>A public tool with setup instructions.</p><script>secret noise</script></main></body></html>`))
	if err != nil || description != "Plain-language overview" || !strings.Contains(content, "setup instructions") || strings.Contains(content, "noise") {
		t.Fatalf("public page extraction: %q %q %v", description, content, err)
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254"} {
		if publicIP(net.ParseIP(address)) {
			t.Fatalf("accepted non-public address %s", address)
		}
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp := testResponse(http.StatusFound, "")
		resp.Header.Set("Location", "http://127.0.0.1/internal")
		return resp, nil
	})}
	if _, _, err := fetchPublicPage(context.Background(), client, "https://example.org/launch"); err == nil {
		t.Fatal("followed a redirect to a private address")
	}
}

func TestGlobalHeatSelectionKeepsManualPriority(t *testing.T) {
	items := []Item{{Title: "manual", InboxNumber: 7}}
	for i := 0; i < 7; i++ {
		items = append(items, Item{Title: fmt.Sprintf("work-%d", i), Category: Work, HeatScore: float64(7-i) / 7})
	}
	selected := rankAndSelect(items)
	if len(selected) != 6 || selected[0].Title != "manual" || selected[5].Title != "work-4" {
		t.Fatalf("global heat selection: %+v", selected)
	}
}

func TestMiniMaxRejectsIncompleteSummary(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return testResponse(200, `{"choices":[{"finish_reason":"length","message":{"content":"{\"intro\":\"未完成\",\"points\":[\"重点\"]}"}}],"base_resp":{"status_code":0}}`), nil
	})}
	mini := newMiniMaxClient(client, "test-key")
	_, _, _, err := mini.summarize(context.Background(), Item{Title: "test"}, strings.Repeat("public source text ", 10))
	if err == nil {
		t.Fatal("accepted truncated MiniMax response")
	}
}

func TestMiniMaxRequiresPlainLanguageAndFirstStepFields(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return testResponse(200, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"intro\":\"一个工具\",\"value\":\"帮助工作\"}"}}],"base_resp":{"status_code":0}}`), nil
	})}
	mini := newMiniMaxClient(client, "test-key")
	if _, _, _, err := mini.summarize(context.Background(), Item{Title: "test"}, strings.Repeat("public source text ", 10)); err == nil {
		t.Fatal("accepted a summary without a first step")
	}
}

func TestMiniMaxFailureShowsOriginalDescription(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"topics":{"work":{"keywords":["Go"]},"life":{"keywords":["learning"]}},"feeds":[]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var digestBody string
	miniCalls := 0
	now := time.Date(2026, 9, 21, 9, 15, 0, 0, time.FixedZone("CST", 8*3600))
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "github.com":
			return testResponse(503, "unavailable"), nil
		case req.URL.Path == "/v0/topstories.json":
			return testResponse(200, `[123]`), nil
		case req.URL.Path == "/v0/item/123.json":
			return testResponse(200, fmt.Sprintf(`{"type":"story","title":"Go backend tool launch","url":"https://example.org/item","score":100,"descendants":20,"time":%d}`, now.Unix())), nil
		case req.URL.Host == "example.org":
			return testResponse(200, `<html><head><meta name="description" content="A Go backend tool with a clear practical setup."></head><body><main>This public Go backend tool explains a practical method for managing jobs. It includes implementation examples, trade-offs, and step-by-step setup that developers can verify.</main></body></html>`), nil
		case req.URL.Host == "api.typesafe.ai":
			return testResponse(200, `{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":0.9},"learning":{"type":"noul","noul":0.1}},"usage":{"input_tokens":100}}`), nil
		case req.URL.Host == "api.minimax.cn":
			miniCalls++
			return testResponse(429, `{}`), nil
		case strings.Contains(req.URL.Path, "/labels/"):
			return testResponse(200, `{}`), nil
		case req.URL.Path == "/repos/o/private/issues" && req.Method == http.MethodGet:
			return testResponse(200, `[]`), nil
		case req.URL.Path == "/repos/o/private/issues" && req.Method == http.MethodPost:
			return testResponse(201, `{"number":1,"title":"[Radar] 2026-09-21"}`), nil
		case req.URL.Path == "/repos/o/private/issues/1" && req.Method == http.MethodPatch:
			var data struct{ Body string }
			_ = json.NewDecoder(req.Body).Decode(&data)
			digestBody = data.Body
			return testResponse(200, `{}`), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return testResponse(404, `{}`), nil
		}
	})}
	err := Run(context.Background(), Options{ConfigPath: configPath, Now: now, HTTPClient: client, GitHubToken: "test-token", Repository: "o/private", JevKey: "test-key", MiniMaxKey: "test-mini-key", Out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if miniCalls != 1 || !strings.Contains(digestBody, "有条目未生成中文摘要") || !strings.Contains(digestBody, "原始简介：A Go backend tool") || strings.Contains(digestBody, "是什么：这是") {
		t.Fatalf("failed summary was not marked: calls=%d body=%s", miniCalls, digestBody)
	}
}

func TestLegacyFeedDoesNotEnterHotDigest(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"topics":{"work":{"keywords":["Go"]},"life":{"keywords":["learning"]}},"feeds":[{"name":"Private Feed","url":"https://example.org/feed","category":"work"}]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var digestBody string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "example.org":
			t.Error("legacy feed was fetched for the hot digest")
			return testResponse(500, `{}`), nil
		case req.URL.Host == "github.com":
			return testResponse(503, "unavailable"), nil
		case req.URL.Path == "/v0/topstories.json":
			return testResponse(200, `[]`), nil
		case strings.Contains(req.URL.Path, "/labels/"):
			return testResponse(200, `{}`), nil
		case req.URL.Path == "/repos/o/private/issues" && req.Method == http.MethodGet:
			return testResponse(200, `[]`), nil
		case req.URL.Path == "/repos/o/private/issues" && req.Method == http.MethodPost:
			return testResponse(201, `{"number":1,"title":"[Radar] 2026-09-21"}`), nil
		case req.URL.Path == "/repos/o/private/issues/1" && req.Method == http.MethodPatch:
			var data struct{ Body string }
			_ = json.NewDecoder(req.Body).Decode(&data)
			digestBody = data.Body
			return testResponse(200, `{}`), nil
		case req.URL.Host == "api.minimax.cn":
			t.Error("unapproved RSS content was sent to MiniMax")
			return testResponse(500, `{}`), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return testResponse(404, `{}`), nil
		}
	})}
	now := time.Date(2026, 9, 21, 9, 15, 0, 0, time.FixedZone("CST", 8*3600))
	err := Run(context.Background(), Options{ConfigPath: configPath, Now: now, HTTPClient: client, GitHubToken: "test-token", Repository: "o/private", MiniMaxKey: "test-mini-key", Out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(digestBody, "今天没有可验证的推荐条目") || strings.Contains(digestBody, "Go learning") {
		t.Fatalf("legacy feed appeared in hot digest: %s", digestBody)
	}
}

func TestPrivateAndUnsafeURLs(t *testing.T) {
	for _, link := range []string{"javascript:alert(1)", "http://127.0.0.1/x", "https://localhost/x", "https://example.com/x>)"} {
		if validatePublicURL(link) == nil {
			t.Fatalf("accepted unsafe URL %q", link)
		}
	}
	if got := canonicalURL("https://Example.com/a/?utm_source=x#part"); got != "https://example.com/a" {
		t.Fatalf("canonical URL = %q", got)
	}
}

func TestKeywordUsesWordBoundaries(t *testing.T) {
	if containsKeyword("ongoing progress", "go") {
		t.Fatal("short keyword matched inside another word")
	}
	if !containsKeyword("go-tool for backend", "go") || !containsKeyword("personal knowledge management", "knowledge management") {
		t.Fatal("expected keyword boundary match")
	}
}

func TestInboxAndDedup(t *testing.T) {
	entry := issue{Number: 7, Title: "[Radar Inbox] 一个好方法", Body: "### URL\nhttps://example.com/a\n\n### Category\nlife\n\n### My note\n稍后实践", CreatedAt: time.Now()}
	item, err := parseInbox(entry)
	if err != nil || item.InboxNumber != 7 || item.Note != "稍后实践" {
		t.Fatalf("parse inbox: %+v %v", item, err)
	}
	body := renderDigest("2026-09-21", []Item{item}, false, nil, 0, 0, "")
	if got := inboxNumbers(body); len(got) != 1 || got[0] != 7 {
		t.Fatalf("inbox markers = %v", got)
	}
	seen := seenFromIssues([]issue{{Body: body, CreatedAt: time.Now()}}, time.Now())
	if !seen["https://example.com/a"] {
		t.Fatal("digest link not deduplicated")
	}
}

func TestJevRejectsIncompleteAnswer(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return testResponse(200, `{"model":"jev-1.13.0","answers":{"work":{"type":"noul"},"learning":{"type":"noul","noul":0.8}},"usage":{"input_tokens":100}}`), nil
	})}
	jev := newJevClient(client, "test-key")
	_, err := jev.evaluate(context.Background(), Item{Title: "Go article", Source: "Go Blog"}, map[string]Topic{Work: {Keywords: []string{"Go"}}, Life: {Keywords: []string{"learning"}}})
	if err == nil {
		t.Fatal("missing Jev probability was accepted")
	}
}

func TestDryRunShowsSourceFailureWithoutWriting(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"topics":{"work":{"keywords":["Go"]},"life":{"keywords":["learning"]}},"feeds":[{"name":"Broken Feed","url":"https://example.org/feed","category":"work"}]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Errorf("dry-run sent a write request: %s", req.Method)
		}
		return testResponse(503, "temporarily unavailable"), nil
	})}
	var output bytes.Buffer
	err := Run(context.Background(), Options{ConfigPath: configPath, DryRun: true, HTTPClient: client, Out: &output, Now: time.Date(2026, 9, 21, 9, 15, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "来源不完整") || !strings.Contains(output.String(), "今天没有可验证的推荐条目") {
		t.Fatalf("failure not reported honestly: %s", output.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func testTrendingHTML(repo, description, stars, period string) string {
	window := "today"
	if period == "weekly" {
		window = "this week"
	}
	return `<html><body><article class="Box-row"><h2><a href="/` + repo + `">` + repo + `</a></h2><p class="col-9">` + description + `</p><span>` + stars + ` stars ` + window + `</span></article></body></html>`
}

func TestRunCreatesAndReusesPrivateDigest(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"topics":{"work":{"keywords":["Go"],"github_queries":["go backend in:name,description"]},"life":{"keywords":["learning"],"github_queries":[]}},"feeds":[{"name":"Learning Feed","url":"https://example.org/feed","category":"life","allow_minimax":true}]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 9, 15, 0, 0, time.FixedZone("CST", 8*3600))
	var mu sync.Mutex
	var digest issue
	old := issue{Number: 1, Title: "[Radar] 2026-09-20", Body: "<!-- radar-status:complete -->", State: "open", CreatedAt: now.Add(-24 * time.Hour)}
	inbox := issue{Number: 10, Title: "[Radar Inbox] Go practice", Body: "### URL\nhttps://example.com/manual\n\n### Category\nwork\n\n### My note\nPrivate learning note", State: "open", CreatedAt: now}
	jevCalls := 0
	miniCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		path := req.URL.Path
		switch {
		case req.URL.Host == "github.com":
			return testResponse(200, testTrendingHTML("example/go-tool", "Go backend tool", "1,200", req.URL.Query().Get("since"))), nil
		case path == "/v0/topstories.json":
			return testResponse(200, `[123]`), nil
		case path == "/v0/item/123.json":
			return testResponse(200, fmt.Sprintf(`{"type":"story","title":"Jev launch","url":"https://example.org/learn","score":150,"descendants":40,"time":%d}`, now.Unix())), nil
		case req.URL.Host == "example.org":
			return testResponse(200, `<html><head><meta name="description" content="A new AI model API that readers can try."></head><body><main>This public article describes an AI model API with a documented playground, concrete examples, and a short setup process that readers can try.</main></body></html>`), nil
		case req.URL.Host == "api.typesafe.ai":
			b, _ := io.ReadAll(req.Body)
			if bytes.Contains(b, []byte("Private learning note")) {
				t.Error("personal note was sent to Jev")
			}
			jevCalls++
			return testResponse(200, `{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":0.9},"learning":{"type":"noul","noul":0.8}},"usage":{"input_tokens":100}}`), nil
		case req.URL.Host == "api.minimax.cn":
			b, _ := io.ReadAll(req.Body)
			if bytes.Contains(b, []byte("Private learning note")) || bytes.Contains(b, []byte("test-token")) {
				t.Error("private content was sent to MiniMax")
			}
			miniCalls++
			return testResponse(200, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"intro\":\"这是一个公开工具。\",\"value\":\"可以帮助你学习。\",\"first_step\":\"先看官方文档。\"}"}}],"base_resp":{"status_code":0}}`), nil
		case path == "/repos/example/go-tool/readme":
			content := base64.StdEncoding.EncodeToString([]byte("# Go tool\nThis public Go backend tool provides a practical method for managing jobs and processing tasks. It includes examples and clear setup instructions for developers."))
			return testResponse(200, `{"encoding":"base64","content":"`+content+`"}`), nil
		case strings.Contains(path, "/labels/"):
			return testResponse(200, `{"name":"ok"}`), nil
		case path == "/repos/o/private/issues" && req.Method == http.MethodGet:
			var result []issue
			if req.URL.Query().Get("labels") == "radar-digest" {
				result = append(result, old)
				if digest.Number > 0 {
					result = append(result, digest)
				}
			} else if inbox.State == "open" {
				result = append(result, inbox)
			}
			b, _ := json.Marshal(result)
			return testResponse(200, string(b)), nil
		case path == "/repos/o/private/issues" && req.Method == http.MethodPost:
			var data struct{ Title, Body string }
			_ = json.NewDecoder(req.Body).Decode(&data)
			digest = issue{Number: 2, Title: data.Title, Body: data.Body, State: "open", CreatedAt: now}
			b, _ := json.Marshal(digest)
			return testResponse(201, string(b)), nil
		case strings.HasPrefix(path, "/repos/o/private/issues/") && req.Method == http.MethodPatch:
			var data struct{ Body, State string }
			_ = json.NewDecoder(req.Body).Decode(&data)
			if strings.HasSuffix(path, "/2") {
				digest.Body = data.Body
			} else if strings.HasSuffix(path, "/1") {
				old.State = data.State
			} else if strings.HasSuffix(path, "/10") {
				inbox.State = data.State
			}
			return testResponse(200, `{}`), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return testResponse(404, `{}`), nil
		}
	})}
	options := Options{ConfigPath: configPath, Now: now, HTTPClient: client, GitHubToken: "test-token", Repository: "o/private", JevKey: "test-key", MiniMaxKey: "test-mini-key", Out: io.Discard}
	if err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if jevCalls != 2 || miniCalls != 2 || old.State != "closed" || inbox.State != "closed" || !strings.Contains(digest.Body, "Private learning note") || !strings.Contains(digest.Body, "Jev launch") || !strings.Contains(digest.Body, "这是一个公开工具") || !strings.Contains(digest.Body, "第一步怎么试：先看官方文档") || !strings.Contains(digest.Body, "radar-status:complete") {
		t.Fatalf("first run: Jev=%d MiniMax=%d old=%s inbox=%s digest=%s", jevCalls, miniCalls, old.State, inbox.State, digest.Body)
	}
	if err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if jevCalls != 2 || miniCalls != 2 {
		t.Fatalf("rerun called models again: Jev=%d MiniMax=%d", jevCalls, miniCalls)
	}
}

func TestRunMarksJevFailureAsDegraded(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"topics":{"work":{"keywords":["Go"]},"life":{"keywords":["learning"]}},"feeds":[{"name":"Go Feed","url":"https://example.org/feed","category":"work"}]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var body string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "github.com":
			return testResponse(200, testTrendingHTML("example/go-update", "Go backend", "1,200", req.URL.Query().Get("since"))), nil
		case req.URL.Path == "/v0/topstories.json":
			return testResponse(200, `[]`), nil
		case req.URL.Host == "api.typesafe.ai":
			return testResponse(429, `{}`), nil
		case strings.Contains(req.URL.Path, "/labels/"):
			return testResponse(200, `{}`), nil
		case req.URL.Path == "/repos/o/private/issues" && req.Method == http.MethodGet:
			return testResponse(200, `[]`), nil
		case req.URL.Path == "/repos/o/private/issues" && req.Method == http.MethodPost:
			return testResponse(201, `{"number":1,"title":"[Radar] 2026-09-21"}`), nil
		case req.URL.Path == "/repos/o/private/issues/1" && req.Method == http.MethodPatch:
			var data struct{ Body string }
			_ = json.NewDecoder(req.Body).Decode(&data)
			body = data.Body
			return testResponse(200, `{}`), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return testResponse(404, `{}`), nil
		}
	})}
	now := time.Date(2026, 9, 21, 9, 15, 0, 0, time.FixedZone("CST", 8*3600))
	err := Run(context.Background(), Options{ConfigPath: configPath, Now: now, HTTPClient: client, GitHubToken: "test-token", Repository: "o/private", JevKey: "test-key", Out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "⚠️ 本期为降级结果") || !strings.Contains(body, "example/go-update") || !strings.Contains(body, "Jev：1 次请求") {
		t.Fatalf("failed Jev call not marked and recovered: %s", body)
	}
}
