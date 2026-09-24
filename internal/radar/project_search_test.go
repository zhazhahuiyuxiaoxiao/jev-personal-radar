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

const fullStackReadme = "# Full stack project\nThis practical project includes a Vue frontend, a Go backend, a PostgreSQL database, and Docker Compose deployment. Follow the installation steps to run locally and learn the application architecture. The frontend calls a backend API, which persists data in PostgreSQL."

func searchResponse(repos ...searchedRepo) string {
	b, _ := json.Marshal(map[string]any{"items": repos})
	return string(b)
}

func readmeResponse(body string) string {
	b, _ := json.Marshal(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(body))})
	return string(b)
}

func starHistoryResponse(now time.Time, recent int) string {
	week := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -int(now.UTC().Weekday()))
	days := [7]int{}
	days[int(now.UTC().Weekday())] = recent
	return fmt.Sprintf(`[{"week":%d,"days":%v}]`, week.Unix(), intArrayJSON(days))
}

func intArrayJSON(days [7]int) string {
	b, _ := json.Marshal(days)
	return string(b)
}

func TestProjectSearchGatesAndRecentGrowth(t *testing.T) {
	now := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	repos := []searchedRepo{
		{FullName: "new/good", HTMLURL: "https://github.com/new/good", CreatedAt: now.AddDate(0, -2, 0), Stars: 8},
		{FullName: "new/docs", HTMLURL: "https://github.com/new/docs", CreatedAt: now.AddDate(0, -2, 0), Stars: 500},
		{FullName: "new/stale", HTMLURL: "https://github.com/new/stale", CreatedAt: now.AddDate(0, -2, 0), Stars: 500},
		{FullName: "old/low", HTMLURL: "https://github.com/old/low", CreatedAt: now.AddDate(-1, -1, 0), Stars: 999},
		{FullName: "old/good", HTMLURL: "https://github.com/old/good", CreatedAt: now.AddDate(-1, -1, 0), Stars: 1000},
		{FullName: "ancient/low", HTMLURL: "https://github.com/ancient/low", CreatedAt: now.AddDate(-3, 0, 0), Stars: 4999},
		{FullName: "ancient/good", HTMLURL: "https://github.com/ancient/good", CreatedAt: now.AddDate(-3, 0, 0), Stars: 5000},
	}
	readmes, histories := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/search/repositories":
			return testResponse(200, searchResponse(repos...)), nil
		case strings.HasSuffix(req.URL.Path, "/readme"):
			readmes++
			if strings.Contains(req.URL.Path, "/docs/") {
				return testResponse(200, readmeResponse("# Resource list\nA collection of links and tutorials for developers.")), nil
			}
			return testResponse(200, readmeResponse(fullStackReadme)), nil
		case strings.HasSuffix(req.URL.Path, "/stargazers/history"):
			histories++
			growth := 5
			if strings.Contains(req.URL.Path, "/stale/") {
				growth = 0
			}
			return testResponse(200, starHistoryResponse(now, growth)), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	items, failures := gh.findProjectCandidates(context.Background(), now, map[string]bool{"https://github.com/old/good": true}, nil, 30)
	if len(failures) != 0 || len(items) != 2 || items[0].Title != "new/good" || items[1].Title != "ancient/good" {
		t.Fatalf("project gates: items=%+v failures=%v", items, failures)
	}
	if readmes != 4 || histories != 3 {
		t.Fatalf("unexpected API calls: readmes=%d histories=%d", readmes, histories)
	}
	if items[0].HeatScore != 5 || !strings.Contains(items[0].HeatEvidence, "累计 8 星") {
		t.Fatalf("new low-star project lacked real growth evidence: %+v", items[0])
	}
}

func TestProjectSearchCapsCandidatesAndDeduplicates(t *testing.T) {
	now := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	searches := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/search/repositories":
			searches++
			var repos []searchedRepo
			for i := 0; i < 30; i++ {
				name := fmt.Sprintf("example/repo-%d-%02d", searches, i)
				repos = append(repos, searchedRepo{FullName: name, HTMLURL: "https://github.com/" + name, CreatedAt: now.AddDate(0, 0, -20), Stars: 0})
			}
			return testResponse(200, searchResponse(repos...)), nil
		case strings.HasSuffix(req.URL.Path, "/readme"):
			return testResponse(200, readmeResponse(fullStackReadme)), nil
		case strings.HasSuffix(req.URL.Path, "/stargazers/history"):
			return testResponse(200, starHistoryResponse(now, 1)), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	items, failures := gh.findProjectCandidates(context.Background(), now, nil, nil, maxProjectJevRequests)
	if len(failures) != 0 || len(items) != 30 {
		t.Fatalf("candidate cap or dedup: %d items %v", len(items), failures)
	}
}

func TestRunUsesProjectSearchOnlyForEmptyFirstPass(t *testing.T) {
	now := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"topics":{"work":{"keywords":["backend"]},"life":{"keywords":["full-stack"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var digest issue
	jevCalls, searches := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "github.com":
			return testResponse(200, testTrendingHTML("example/unrelated", "A game", "100", req.URL.Query().Get("since"))), nil
		case req.URL.Host == "hacker-news.firebaseio.com":
			return testResponse(200, `[]`), nil
		case req.URL.Host == "api.typesafe.ai":
			jevCalls++
			score := "0.1"
			if jevCalls == 2 {
				score = "0.9"
			}
			return testResponse(200, fmt.Sprintf(`{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":%s},"learning":{"type":"noul","noul":%s}},"usage":{"input_tokens":100}}`, score, score)), nil
		case req.URL.Host == "api.github.com" && req.URL.Path == "/search/repositories":
			searches++
			return testResponse(200, searchResponse(searchedRepo{FullName: "example/full-stack", HTMLURL: "https://github.com/example/full-stack", CreatedAt: now.AddDate(0, -1, 0), Stars: 3})), nil
		case req.URL.Host == "api.github.com" && strings.HasSuffix(req.URL.Path, "/readme"):
			return testResponse(200, readmeResponse(fullStackReadme)), nil
		case req.URL.Host == "api.github.com" && strings.HasSuffix(req.URL.Path, "/stargazers/history"):
			return testResponse(200, starHistoryResponse(now, 3)), nil
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
	if jevCalls != 2 || searches != 5 || !strings.Contains(digest.Body, "example/full-stack") || !strings.Contains(digest.Body, "近 7 天新增约 3 星") || strings.Contains(digest.Body, "你手动保存") || !strings.Contains(digest.Body, "Jev：2 次请求") {
		t.Fatalf("project second pass: Jev=%d searches=%d body=%s", jevCalls, searches, digest.Body)
	}
}

func TestRetryEmptyRejectsOldMarkerBeforePaidCalls(t *testing.T) {
	body := renderDigest("2026-09-24", nil, false, nil, 20, 8023, "")
	body = strings.Replace(body, "<!-- radar-status:complete -->", "<!-- radar-status:complete -->\n<!-- radar-chinese-pass:complete -->", 1)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("old retry marker should reject before requests: %s", req.URL)
		return nil, nil
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	if err := retryEmptyDigest(context.Background(), gh, client, Config{}, Options{JevKey: "test-key"}, issue{Number: 3, Body: body, State: "open"}, nil, time.Now()); err == nil {
		t.Fatal("accepted a digest already retried by old code")
	}
}

func TestRetryEmptyUpdatesOnceAndPreservesDailyUsage(t *testing.T) {
	now := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	body := renderDigest("2026-09-24", nil, false, nil, 20, 8023, "")
	digest := issue{Number: 3, Body: body, State: "open", CreatedAt: now}
	jevCalls, updates := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/search/repositories":
			return testResponse(200, searchResponse(searchedRepo{FullName: "example/full-stack", HTMLURL: "https://github.com/example/full-stack", CreatedAt: now.AddDate(0, -1, 0), Stars: 5})), nil
		case strings.HasSuffix(req.URL.Path, "/readme"):
			return testResponse(200, readmeResponse(fullStackReadme)), nil
		case strings.HasSuffix(req.URL.Path, "/stargazers/history"):
			return testResponse(200, starHistoryResponse(now, 5)), nil
		case req.Method == http.MethodGet && req.URL.Path == "/repos/o/private/issues/3":
			data, _ := json.Marshal(digest)
			return testResponse(200, string(data)), nil
		case req.Method == http.MethodPatch && req.URL.Path == "/repos/o/private/issues/3":
			var data struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(req.Body).Decode(&data)
			digest.Body = data.Body
			updates++
			return testResponse(200, `{}`), nil
		case req.URL.Host == "api.typesafe.ai":
			jevCalls++
			return testResponse(200, `{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":0.9},"learning":{"type":"noul","noul":0.8}},"usage":{"input_tokens":100}}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	config := Config{Topics: map[string]Topic{Work: {Keywords: []string{"full-stack"}}, Life: {Keywords: []string{"learning"}}}}
	o := Options{JevKey: "test-key", Out: io.Discard}
	if err := retryEmptyDigest(context.Background(), gh, client, config, o, digest, []issue{digest}, now); err != nil {
		t.Fatal(err)
	}
	if jevCalls != 1 || updates != 2 || !strings.Contains(digest.Body, "<!-- radar-project-pass:complete -->") || !strings.Contains(digest.Body, "example/full-stack") || !strings.Contains(digest.Body, "Jev：21 次请求，8123 输入 token") {
		t.Fatalf("retry result: Jev=%d updates=%d body=%s", jevCalls, updates, digest.Body)
	}
	if err := retryEmptyDigest(context.Background(), gh, client, config, o, digest, []issue{digest}, now); err == nil || jevCalls != 1 {
		t.Fatalf("repeated retry made paid calls: %v, %d", err, jevCalls)
	}
}

func TestOneTimeAfterChineseRetryPreservesUsageAndBothMarkers(t *testing.T) {
	now := time.Date(2026, 9, 24, 14, 0, 0, 0, time.FixedZone("CST", 8*3600))
	body := renderDigest("2026-09-24", nil, false, nil, 48, 22400, "")
	body = strings.Replace(body, "<!-- radar-status:complete -->", "<!-- radar-status:complete -->\n<!-- radar-chinese-pass:complete -->", 1)
	digest := issue{Number: 3, Body: body, State: "open", CreatedAt: now}
	jevCalls, updates := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/search/repositories":
			return testResponse(200, searchResponse(searchedRepo{FullName: "example/full-stack", HTMLURL: "https://github.com/example/full-stack", CreatedAt: now.AddDate(0, -1, 0), Stars: 5})), nil
		case strings.HasSuffix(req.URL.Path, "/readme"):
			return testResponse(200, readmeResponse(fullStackReadme)), nil
		case strings.HasSuffix(req.URL.Path, "/stargazers/history"):
			return testResponse(200, starHistoryResponse(now, 5)), nil
		case req.Method == http.MethodGet && req.URL.Path == "/repos/o/private/issues/3":
			data, _ := json.Marshal(digest)
			return testResponse(200, string(data)), nil
		case req.Method == http.MethodPatch && req.URL.Path == "/repos/o/private/issues/3":
			var data struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(req.Body).Decode(&data)
			digest.Body = data.Body
			updates++
			return testResponse(200, `{}`), nil
		case req.URL.Host == "api.typesafe.ai":
			jevCalls++
			return testResponse(200, `{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":0.9},"learning":{"type":"noul","noul":0.8}},"usage":{"input_tokens":100}}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	config := Config{Topics: map[string]Topic{Work: {Keywords: []string{"full-stack"}}, Life: {Keywords: []string{"learning"}}}}
	o := Options{JevKey: "test-key", RetryAfterChinese: true, Out: io.Discard}
	if err := retryEmptyDigest(context.Background(), gh, client, config, o, digest, []issue{digest}, now); err != nil {
		t.Fatal(err)
	}
	if jevCalls != 1 || updates != 2 || !strings.Contains(digest.Body, "<!-- radar-chinese-pass:complete -->") || !strings.Contains(digest.Body, "<!-- radar-project-pass:complete -->") || !strings.Contains(digest.Body, "Jev：49 次请求，22500 输入 token") {
		t.Fatalf("after-Chinese retry: Jev=%d updates=%d body=%s", jevCalls, updates, digest.Body)
	}
	if err := retryEmptyDigest(context.Background(), gh, client, config, o, digest, []issue{digest}, now); err == nil || jevCalls != 1 {
		t.Fatalf("repeated after-Chinese retry made paid calls: %v, %d", err, jevCalls)
	}
}

func TestAfterChineseRetryRejectsWrongDayOrBudget(t *testing.T) {
	body := renderDigest("2026-09-24", nil, false, nil, 49, 22400, "")
	body = strings.Replace(body, "<!-- radar-status:complete -->", "<!-- radar-status:complete -->\n<!-- radar-chinese-pass:complete -->", 1)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("invalid retry made a request: %s", req.URL)
		return nil, nil
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	o := Options{JevKey: "test-key", RetryAfterChinese: true}
	for _, now := range []time.Time{time.Date(2026, 9, 24, 14, 0, 0, 0, time.UTC), time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC)} {
		if err := retryEmptyDigest(context.Background(), gh, client, Config{}, o, issue{Number: 3, Body: body, State: "open"}, nil, now); err == nil {
			t.Fatalf("accepted invalid after-Chinese retry on %s", now)
		}
	}
}

func TestProjectPassJevFailureIsDegraded(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testResponse(401, `{}`), nil
	})}
	config := Config{Topics: map[string]Topic{Work: {Keywords: []string{"backend"}}, Life: {Keywords: []string{"learning"}}}}
	result := scoreProjectCandidates(context.Background(), client, []Item{{Title: "example/unrelated", Description: "A game", Source: "GitHub 项目搜索"}}, config, "test-key", "")
	if result.Calls != 1 || !result.Degraded || len(result.Items) != 0 || len(result.Failures) != 1 {
		t.Fatalf("Jev failure was hidden or a result was invented: %+v", result)
	}
}
