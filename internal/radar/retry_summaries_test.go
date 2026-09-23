package radar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRetrySummariesUpdatesOnlyExistingDigest(t *testing.T) {
	item := Item{Title: "example/tool", URL: "https://github.com/example/tool", Description: "An original public description", IsRepo: true, HeatEvidence: "今日新增 100 星", HeatURL: "https://github.com/trending", Reason: "与你关注的 AI 工具相关", Source: "GitHub"}
	manual := Item{Title: "我的收藏", URL: "https://example.org/manual", InboxNumber: 12, Note: "私人备注", Source: "手动导入"}
	original := renderDigest("2026-09-23", []Item{item, manual}, false, nil, 20, 8167, "有条目未生成中文摘要（本次 MiniMax 1 次请求；原因：MiniMax HTTP 401）；请核对原文。")
	today := issue{Number: 2, Title: digestTitle("2026-09-23"), Body: original, State: "open"}
	var patched string
	miniCalls, readmeCalls := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/repos/example/tool/readme":
			readmeCalls++
			content := base64.StdEncoding.EncodeToString([]byte("# Tool\nThis public tool has detailed installation instructions, examples, and an overview of its useful features for developers."))
			return testResponse(200, `{"encoding":"base64","content":"`+content+`"}`), nil
		case req.URL.Host == "api.minimax.cn":
			miniCalls++
			body, _ := io.ReadAll(req.Body)
			if strings.Contains(string(body), "私人备注") || strings.Contains(string(body), "example.org/manual") {
				t.Error("private manual item sent to MiniMax")
			}
			return testResponse(200, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"intro\":\"一个公开工具。\",\"value\":\"可以帮助你学习。\",\"first_step\":\"先看官方文档。\"}"}}],"base_resp":{"status_code":0}}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/repos/o/private/issues/2":
			b, _ := json.Marshal(today)
			return testResponse(200, string(b)), nil
		case req.Method == http.MethodPatch && req.URL.Path == "/repos/o/private/issues/2":
			var fields struct{ Body string }
			if err := json.NewDecoder(req.Body).Decode(&fields); err != nil {
				t.Fatal(err)
			}
			patched = fields.Body
			return testResponse(200, `{}`), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return testResponse(404, `{}`), nil
		}
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	if err := retrySummaries(context.Background(), gh, client, today, "test-mini-key", "", io.Discard); err != nil {
		t.Fatal(err)
	}
	if miniCalls != 1 || readmeCalls != 1 || patched == "" || !strings.Contains(patched, "是什么：一个公开工具") || !strings.Contains(patched, "为什么火：今日新增 100 星") || !strings.Contains(patched, "Jev：20 次请求，8167 输入 token") || !strings.Contains(patched, "私人备注") || !strings.Contains(patched, "<!-- radar-inbox: 12 -->") || strings.Contains(patched, "暂无可靠中文说明") {
		t.Fatalf("retry changed unexpected fields or missed explanation: MiniMax=%d README=%d body=%s", miniCalls, readmeCalls, patched)
	}
}

func TestRetrySummariesPreservesDigestOnMiniMaxFailure(t *testing.T) {
	item := Item{Title: "example/tool", URL: "https://github.com/example/tool", Description: "An original public description", IsRepo: true, Source: "GitHub"}
	original := renderDigest("2026-09-23", []Item{item}, false, nil, 20, 8167, "有条目未生成中文摘要（本次 MiniMax 1 次请求；原因：MiniMax HTTP 401）；请核对原文。")
	today := issue{Number: 2, Title: digestTitle("2026-09-23"), Body: original, State: "open"}
	patches := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/repos/example/tool/readme":
			content := base64.StdEncoding.EncodeToString([]byte("# Tool\nThis public tool has detailed installation instructions, examples, and an overview of its useful features for developers."))
			return testResponse(200, `{"encoding":"base64","content":"`+content+`"}`), nil
		case req.URL.Host == "api.minimax.cn":
			return testResponse(401, `{"error":"invalid key"}`), nil
		case req.Method == http.MethodPatch:
			patches++
			return testResponse(200, `{}`), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return testResponse(404, `{}`), nil
		}
	})}
	gh, _ := newGitHubClient(client, "test-token", "o/private")
	err := retrySummaries(context.Background(), gh, client, today, "test-mini-key", "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "MiniMax HTTP 401") || patches != 0 || today.Body != original {
		t.Fatalf("failed retry must not change issue: err=%v patches=%d", err, patches)
	}
}
