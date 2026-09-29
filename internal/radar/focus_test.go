package radar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func focusFixture(number int, created time.Time, mode string) issue {
	return issue{Number: number, State: "open", CreatedAt: created, Body: "### 已收藏的 Issue\nhttps://github.com/o/private/issues/1\n\n### 原文链接\nhttps://github.com/old/tool\n\n### 想找什么\nlocal AI storyboard\n\n### 搜索期限\n" + mode + "\n"}
}

func TestFocusCalendarEligibility(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 29, 9, 17, 0, 0, zone)
	saved := issue{Number: 1, State: "open", Body: "### 条目标题\n旧工具\n\n### 原文链接\nhttps://github.com/old/tool\n\n### 研究备注\nprivate note"}
	lookup := map[int]issue{1: saved}
	tests := []struct {
		created      time.Time
		mode, status string
	}{
		{time.Date(2026, 9, 28, 23, 59, 0, 0, zone), "明天一次", ""},
		{time.Date(2026, 9, 27, 23, 59, 0, 0, zone), "明天一次", "一次关注已过期"},
		{time.Date(2026, 9, 20, 9, 0, 0, 0, zone), "长期", ""},
		{now, "长期", "明日起开始"},
	}
	for _, tc := range tests {
		_, status := parseFocus(focusFixture(2, tc.created, tc.mode), lookup, "o/private", now)
		if status != tc.status {
			t.Errorf("%s %s: got %q want %q", tc.created, tc.mode, status, tc.status)
		}
	}
	saved.State = "closed"
	lookup[1] = saved
	if _, status := parseFocus(focusFixture(2, now.AddDate(0, 0, -1), "长期"), lookup, "o/private", now); status != "关联收藏已关闭或不存在" {
		t.Fatal(status)
	}
}

func TestFocusSearchLimitsFailureAndDedup(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 29, 9, 17, 0, 0, zone)
	saved := issue{Number: 1, State: "open", Body: "### 原文链接\nhttps://github.com/old/tool"}
	var searches, jevCalls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/search/repositories"):
			searches++
			if searches == 1 {
				return testResponse(503, `{}`), nil
			}
			return testResponse(200, fmt.Sprintf(`{"items":[{"full_name":"new/tool","html_url":"https://github.com/new/tool","description":"local AI storyboard","created_at":"%s","stargazers_count":10},{"full_name":"old/tool","html_url":"https://github.com/old/tool","created_at":"%s"}]}`, now.AddDate(0, 0, -1).Format(time.RFC3339), now.AddDate(0, 0, -1).Format(time.RFC3339))), nil
		case strings.Contains(req.URL.Path, "/stargazers/history"):
			week := now.AddDate(0, 0, -1).Unix()
			return testResponse(200, fmt.Sprintf(`[{"week":%d,"days":[1,0,0,0,0,0,0]}]`, week)), nil
		case req.URL.Host == "api.typesafe.ai":
			jevCalls++
			return testResponse(200, `{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":0.9},"learning":{"type":"noul","noul":0.2}},"usage":{"input_tokens":9}}`), nil
		default:
			t.Fatalf("unexpected %s", req.URL)
			return nil, nil
		}
	})}
	gh, _ := newGitHubClient(client, "test", "o/private")
	var focuses []issue
	for i := 0; i < 6; i++ {
		focuses = append(focuses, focusFixture(i+2, now.AddDate(0, 0, -1).Add(time.Duration(i)*time.Minute), "长期"))
	}
	r := runFocusSearch(context.Background(), gh, client, focuses, []issue{saved}, nil, nil, now, "test", "")
	if r.pending != 1 || searches != 10 || jevCalls != 1 || r.calls != 1 || len(r.items) != 1 || !strings.Contains(strings.Join(r.lines, ""), "未完成") {
		t.Fatalf("pending=%d searches=%d Jev=%d calls=%d items=%d lines=%v", r.pending, searches, jevCalls, r.calls, len(r.items), r.lines)
	}
}

func TestSixManualItemsReserveOneFocusSlot(t *testing.T) {
	var manual []Item
	for i := 1; i <= 6; i++ {
		manual = append(manual, Item{InboxNumber: i, URL: fmt.Sprintf("https://example.org/%d", i), Score: 100})
	}
	selected := reserveFocusSlot(rankAndSelect(manual), []Item{{URL: "https://github.com/new/tool"}})
	if len(selected) != 6 || selected[5].URL != "https://github.com/new/tool" {
		t.Fatalf("wrong focus reservation: %+v", selected)
	}
	for _, item := range selected {
		if item.InboxNumber == 6 {
			t.Fatal("sixth manual import was shown instead of retained")
		}
	}
}

func TestFocusJevRequestCeiling(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 29, 9, 17, 0, 0, zone)
	var repos []searchedRepo
	for i := 0; i < 21; i++ {
		repos = append(repos, searchedRepo{FullName: fmt.Sprintf("new/tool%d", i), HTMLURL: fmt.Sprintf("https://github.com/new/tool%d", i), Description: "local AI storyboard", CreatedAt: now.AddDate(0, 0, -1)})
	}
	response, _ := json.Marshal(map[string]any{"items": repos})
	var calls, searches int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/search/repositories"):
			searches++
			return testResponse(200, string(response)), nil
		case strings.Contains(req.URL.Path, "/stargazers/history"):
			return testResponse(200, fmt.Sprintf(`[{"week":%d,"days":[1,0,0,0,0,0,0]}]`, now.AddDate(0, 0, -1).Unix())), nil
		case req.URL.Host == "api.typesafe.ai":
			calls++
			return testResponse(200, `{"model":"jev-1.13.0","answers":{"work":{"type":"noul","noul":0.1},"learning":{"type":"noul","noul":0.1}},"usage":{"input_tokens":9}}`), nil
		default:
			t.Fatalf("unexpected %s", req.URL)
			return nil, nil
		}
	})}
	gh, _ := newGitHubClient(client, "test", "o/private")
	focus := focusFixture(2, now.AddDate(0, 0, -1), "长期")
	saved := issue{Number: 1, State: "open", Body: "### 原文链接\nhttps://github.com/old/tool"}
	r := runFocusSearch(context.Background(), gh, client, []issue{focus}, []issue{saved}, nil, nil, now, "test", "")
	if searches != 2 || calls != maxFocusJevRequests || r.calls != maxFocusJevRequests || len(r.items) != 0 || !strings.Contains(strings.Join(r.lines, ""), "未完成") {
		t.Fatalf("searches=%d calls=%d report=%+v", searches, calls, r)
	}
}
