package radar

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	body := renderDigest("2026-09-21", []Item{item}, false, nil, 0, 0)
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
		return testResponse(200, `{"model":"jev-1.13.0","answers":{"relevant":{"type":"noul"},"actionable":{"type":"noul","noul":0.8}},"usage":{"input_tokens":100}}`), nil
	})}
	jev := newJevClient(client, "test-key")
	_, err := jev.evaluate(context.Background(), Item{Title: "Go article", Source: "Go Blog"}, []string{"Go"})
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

func TestRunCreatesAndReusesPrivateDigest(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"topics":{"work":{"keywords":["Go"],"github_queries":["go backend in:name,description"]},"life":{"keywords":["learning"],"github_queries":[]}},"feeds":[{"name":"Learning Feed","url":"https://example.org/feed","category":"life"}]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 9, 15, 0, 0, time.FixedZone("CST", 8*3600))
	var mu sync.Mutex
	var digest issue
	old := issue{Number: 1, Title: "[Radar] 2026-09-20", Body: "<!-- radar-status:complete -->", State: "open", CreatedAt: now.Add(-24 * time.Hour)}
	inbox := issue{Number: 10, Title: "[Radar Inbox] Go practice", Body: "### URL\nhttps://example.com/manual\n\n### Category\nwork\n\n### My note\nPrivate learning note", State: "open", CreatedAt: now}
	jevCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		path := req.URL.Path
		switch {
		case req.URL.Host == "example.org":
			return testResponse(200, `<rss><channel><item><title>Learning method</title><link>https://example.com/learn</link><description>learning practice</description><pubDate>Mon, 21 Sep 2026 00:00:00 +0000</pubDate></item></channel></rss>`), nil
		case req.URL.Host == "api.typesafe.ai":
			b, _ := io.ReadAll(req.Body)
			if bytes.Contains(b, []byte("Private learning note")) {
				t.Error("personal note was sent to Jev")
			}
			jevCalls++
			return testResponse(200, `{"model":"jev-1.13.0","answers":{"relevant":{"type":"noul","noul":0.9},"actionable":{"type":"noul","noul":0.8}},"usage":{"input_tokens":100}}`), nil
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
		case path == "/search/repositories":
			return testResponse(200, `{"items":[{"full_name":"example/go-tool","html_url":"https://github.com/example/go-tool","description":"Go backend tool","updated_at":"2026-09-21T00:00:00Z"}]}`), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return testResponse(404, `{}`), nil
		}
	})}
	options := Options{ConfigPath: configPath, Now: now, HTTPClient: client, GitHubToken: "test-token", Repository: "o/private", JevKey: "test-key", Out: io.Discard}
	if err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if jevCalls != 2 || old.State != "closed" || inbox.State != "closed" || !strings.Contains(digest.Body, "Private learning note") || !strings.Contains(digest.Body, "radar-status:complete") {
		t.Fatalf("first run: Jev=%d old=%s inbox=%s digest=%s", jevCalls, old.State, inbox.State, digest.Body)
	}
	if err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if jevCalls != 2 {
		t.Fatalf("rerun called Jev again: %d", jevCalls)
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
		case req.URL.Host == "example.org":
			return testResponse(200, `<rss><channel><item><title>Go update</title><link>https://example.org/item</link><description>Go backend</description><pubDate>Mon, 21 Sep 2026 00:00:00 +0000</pubDate></item></channel></rss>`), nil
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
	if !strings.Contains(body, "⚠️ 本期为降级结果") || !strings.Contains(body, "Go update") || !strings.Contains(body, "Jev：1 次请求") {
		t.Fatalf("failed Jev call not marked and recovered: %s", body)
	}
}
