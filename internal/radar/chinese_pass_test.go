package radar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSearchesChineseTrendingOnlyAfterEmptyFirstPass(t *testing.T) {
	now := time.Date(2026, 9, 24, 11, 0, 0, 0, time.FixedZone("CST", 8*3600))
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"topics":{"work":{"keywords":["backend","full-stack"]},"life":{"keywords":["learning","full-stack"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var digest issue
	jevCalls, chineseFetches := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "github.com" && req.URL.Path == "/trending":
			if req.URL.Query().Get("spoken_language_code") == "zh" {
				chineseFetches++
				return testResponse(200, testTrendingHTML("example/full-stack", "可运行的全栈实战项目", "100", req.URL.Query().Get("since"))), nil
			}
			return testResponse(200, testTrendingHTML("example/unrelated", "A random game", "200", req.URL.Query().Get("since"))), nil
		case req.URL.Host == "hacker-news.firebaseio.com":
			return testResponse(200, `[]`), nil
		case req.URL.Host == "api.typesafe.ai":
			jevCalls++
			score := "0.1"
			if jevCalls == 2 {
				score = "0.9"
			}
			return testResponse(200, fmt.Sprintf(`{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":%s},"learning":{"type":"noul","noul":%s}},"usage":{"input_tokens":100}}`, score, score)), nil
		case req.URL.Host == "api.github.com" && strings.Contains(req.URL.Path, "/labels/"):
			return testResponse(200, `{}`), nil
		case req.URL.Host == "api.github.com" && req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/issues"):
			return testResponse(200, `[]`), nil
		case req.URL.Host == "api.github.com" && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/issues"):
			return testResponse(201, `{"number":3,"title":"[Radar] 2026-09-24","state":"open"}`), nil
		case req.URL.Host == "api.github.com" && req.Method == http.MethodPatch:
			var data struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(req.Body).Decode(&data)
			digest.Body = data.Body
			return testResponse(200, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}
	if err := Run(context.Background(), Options{ConfigPath: configPath, Now: now, HTTPClient: client, GitHubToken: "test-token", Repository: "o/private", JevKey: "test-key", Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if jevCalls != 2 || chineseFetches != 2 || !strings.Contains(digest.Body, "example/full-stack") || strings.Contains(digest.Body, "example/unrelated") || !strings.Contains(digest.Body, "GitHub 中文今日榜") || !strings.Contains(digest.Body, "Jev：2 次请求") {
		t.Fatalf("Chinese second pass did not select the matching item: calls=%d fetches=%d body=%s", jevCalls, chineseFetches, digest.Body)
	}
}

func TestChinesePassCapsExtraJevCallsAtThirty(t *testing.T) {
	now := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var page strings.Builder
		for i := 0; i < 25; i++ {
			page.WriteString(testTrendingHTML(fmt.Sprintf("example/%s-%02d", req.URL.Query().Get("since"), i), "A project", "10", req.URL.Query().Get("since")))
		}
		return testResponse(200, page.String()), nil
	})}
	items, failures := collectChineseCandidates(context.Background(), client, now, nil, nil, maxChineseJevRequests)
	if len(failures) != 0 || len(items) != 30 {
		t.Fatalf("second-pass candidate limit: items=%d failures=%v", len(items), failures)
	}
	for _, item := range items {
		if !strings.Contains(item.HeatURL, "spoken_language_code=zh") {
			t.Fatalf("missing Chinese heat source: %s", item.HeatURL)
		}
	}
}

func TestLiveChineseTrendingOptional(t *testing.T) {
	if os.Getenv("RADAR_LIVE_TRENDING") != "1" {
		t.Skip("set RADAR_LIVE_TRENDING=1 for a live source check")
	}
	items, err := fetchTrendingWithLanguage(context.Background(), httpClient(), "daily", time.Now(), "zh")
	if err != nil || len(items) == 0 || !strings.Contains(items[0].HeatURL, "spoken_language_code=zh") {
		t.Fatalf("live Chinese Trending: %d items, %v", len(items), err)
	}
}

func TestRetryEmptyDigestUpdatesOnceAndPreservesDailyUsage(t *testing.T) {
	now := time.Date(2026, 9, 24, 11, 0, 0, 0, time.FixedZone("CST", 8*3600))
	body := renderDigest("2026-09-24", nil, false, nil, 20, 8023, "本期没有获准交给 MiniMax 的自动条目；显示来源原始简介。")
	digest := issue{Number: 3, Title: digestTitle("2026-09-24"), Body: body, State: "open", CreatedAt: now}
	jevCalls, miniCalls, updates := 0, 0, 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "github.com":
			return testResponse(200, testTrendingHTML("example/full-stack", "可运行的全栈项目", "100", req.URL.Query().Get("since"))), nil
		case req.URL.Host == "api.typesafe.ai":
			jevCalls++
			return testResponse(200, `{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":0.9},"learning":{"type":"noul","noul":0.8}},"usage":{"input_tokens":100}}`), nil
		case req.URL.Host == "api.github.com" && strings.HasSuffix(req.URL.Path, "/readme"):
			content := base64.StdEncoding.EncodeToString([]byte("# Full stack project\nA reproducible full stack project with frontend, backend, database, deployment steps, and useful tests that readers can run locally."))
			return testResponse(200, `{"encoding":"base64","content":"`+content+`"}`), nil
		case req.URL.Host == "api.github.com" && req.Method == http.MethodGet:
			data, _ := json.Marshal(digest)
			return testResponse(200, string(data)), nil
		case req.URL.Host == "api.github.com" && req.Method == http.MethodPatch:
			var data struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(req.Body).Decode(&data)
			digest.Body = data.Body
			updates++
			return testResponse(200, `{}`), nil
		case req.URL.Host == "api.minimax.cn":
			miniCalls++
			return testResponse(200, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"intro\":\"一个完整的全栈项目。\",\"value\":\"可练习前后端与数据库。\",\"first_step\":\"先按文档启动本地环境。\"}"}}],"base_resp":{"status_code":0}}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	config := Config{Topics: map[string]Topic{Work: {Keywords: []string{"full-stack"}}, Life: {Keywords: []string{"learning"}}}}
	o := Options{JevKey: "test-key", MiniMaxKey: "test-mini-key", Out: io.Discard}
	if err := retryEmptyDigest(context.Background(), gh, client, config, o, digest, []issue{digest}, now); err != nil {
		t.Fatal(err)
	}
	if jevCalls != 1 || miniCalls != 1 || updates != 3 || !strings.Contains(digest.Body, "example/full-stack") || !strings.Contains(digest.Body, "Jev：21 次请求，8123 输入 token") || !strings.Contains(digest.Body, "<!-- radar-chinese-pass:complete -->") || !strings.Contains(digest.Body, "是什么：一个完整的全栈项目") {
		t.Fatalf("retry did not preserve usage or complete the summary: Jev=%d MiniMax=%d updates=%d body=%s", jevCalls, miniCalls, updates, digest.Body)
	}
	if err := retryEmptyDigest(context.Background(), gh, client, config, o, digest, []issue{digest}, now); err == nil || jevCalls != 1 || miniCalls != 1 {
		t.Fatalf("repeated empty retry should not call models: err=%v Jev=%d MiniMax=%d", err, jevCalls, miniCalls)
	}
}

func TestChinesePassJevFailureIsDegradedWithoutInventingItems(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.typesafe.ai" {
			t.Fatalf("unexpected request: %s", req.URL)
		}
		return testResponse(401, `{}`), nil
	})}
	config := Config{Topics: map[string]Topic{Work: {Keywords: []string{"backend"}}, Life: {Keywords: []string{"learning"}}}}
	result := scoreChineseCandidates(context.Background(), client, []Item{{Title: "example/unrelated", Description: "A game", Source: "GitHub Trending 中文"}}, config, "test-key", "")
	if result.Calls != 1 || !result.Degraded || len(result.Items) != 0 || len(result.Failures) != 1 {
		t.Fatalf("Jev failure was hidden or a result was invented: %+v", result)
	}
}

func TestInterruptedDigestDoesNotStartPaidChinesePass(t *testing.T) {
	now := time.Date(2026, 9, 24, 11, 0, 0, 0, time.FixedZone("CST", 8*3600))
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"topics":{"work":{"keywords":["backend"]},"life":{"keywords":["full-stack"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	digest := issue{Number: 3, Title: digestTitle("2026-09-24"), Body: "<!-- radar-status:running -->", State: "open", CreatedAt: now}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "github.com" && req.URL.Query().Get("spoken_language_code") == "zh":
			return testResponse(200, testTrendingHTML("example/full-stack", "A full-stack project", "100", req.URL.Query().Get("since"))), nil
		case req.URL.Host == "github.com":
			return testResponse(200, testTrendingHTML("example/unrelated", "A game", "100", req.URL.Query().Get("since"))), nil
		case req.URL.Host == "hacker-news.firebaseio.com":
			return testResponse(200, `[]`), nil
		case req.URL.Host == "api.typesafe.ai" || req.URL.Host == "api.minimax.cn":
			t.Fatalf("interrupted digest called a paid model: %s", req.URL.Host)
			return nil, nil
		case req.URL.Host == "api.github.com" && strings.Contains(req.URL.Path, "/labels/"):
			return testResponse(200, `{}`), nil
		case req.URL.Host == "api.github.com" && req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/issues"):
			if req.URL.Query().Get("labels") == "radar-digest" {
				data, _ := json.Marshal([]issue{digest})
				return testResponse(200, string(data)), nil
			}
			return testResponse(200, `[]`), nil
		case req.URL.Host == "api.github.com" && req.Method == http.MethodPatch:
			var data struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(req.Body).Decode(&data)
			digest.Body = data.Body
			return testResponse(200, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}
	err := Run(context.Background(), Options{ConfigPath: configPath, Now: now, HTTPClient: client, GitHubToken: "test-token", Repository: "o/private", JevKey: "test-key", MiniMaxKey: "test-mini-key", Out: io.Discard})
	if err != nil || !strings.Contains(digest.Body, "example/full-stack") || !strings.Contains(digest.Body, "降级结果") || !strings.Contains(digest.Body, "Jev：0 次请求") {
		t.Fatalf("interrupted fallback: err=%v body=%s", err, digest.Body)
	}
}

func TestRetryEmptyRejectsNonEmptyDigestBeforePaidCalls(t *testing.T) {
	body := renderDigest("2026-09-24", []Item{{Title: "example/tool", URL: "https://github.com/example/tool", Category: Work}}, false, nil, 1, 100, "")
	digest := issue{Number: 3, Body: body, State: "open"}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("nonempty retry made a request: %s", req.URL)
		return nil, nil
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	if err := retryEmptyDigest(context.Background(), gh, client, Config{}, Options{JevKey: "test-key"}, digest, nil, time.Now()); err == nil {
		t.Fatal("accepted a completed digest with an item")
	}
}
