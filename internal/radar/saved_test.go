package radar

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func savedFields(t *testing.T, body string) url.Values {
	t.Helper()
	start := strings.Index(body, "[收藏](<")
	if start < 0 {
		t.Fatal("missing save link")
	}
	start += len("[收藏](<")
	end := strings.Index(body[start:], ">)")
	if end < 0 {
		t.Fatal("malformed save link")
	}
	u, err := url.Parse(body[start : start+end])
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestAddSaveLinksNewAndManual(t *testing.T) {
	items := []Item{
		{Title: "中文 & Go [工具]", URL: "https://example.com/a?x=1&y=2", Source: "HN", Category: Work, Summary: "你好 & 世界", Value: "试一试", FirstStep: "读 README", SummaryFrom: "公开原文"},
		{Title: "手动保存", URL: "https://example.com/manual", Source: "手动导入", Category: Life, InboxNumber: 9, Note: "稍后研究"},
	}
	body := renderDigest("2026-09-28", items, false, nil, 0, 0, "")
	updated, count, err := addSaveLinks(body, "owner/private", 42)
	if err != nil || count != 2 {
		t.Fatalf("add: count=%d err=%v", count, err)
	}
	fields := savedFields(t, updated)
	if fields.Get("template") != "radar-saved.yml" || fields.Get("item_title") != "中文 & Go [工具]" || fields.Get("source_url") != items[0].URL || fields.Get("digest_url") != "https://github.com/owner/private/issues/42" {
		t.Fatalf("wrong prefills: %v", fields)
	}
	if !strings.Contains(fields.Get("description"), "是什么：你好 & 世界") || !strings.Contains(fields.Get("description"), "第一步怎么试：读 README") {
		t.Fatalf("missing digest explanation: %q", fields.Get("description"))
	}
	if strings.Count(updated, "[收藏](<") != 2 || !strings.Contains(updated, "[查看我的收藏]") || !strings.Contains(updated, "我的备注：稍后研究") {
		t.Fatal("manual item or saved list link missing")
	}
	if strings.Count(updated, "[反馈这条](<") != 2 || !strings.Contains(updated, "[记录遗漏](<") || !strings.Contains(updated, "[查看搜索关注](<") || !strings.Contains(updated, "[查看我的反馈](<") {
		t.Fatal("feedback or focus entries missing")
	}
	again, _, err := addSaveLinks(updated, "owner/private", 42)
	if err != nil || again != updated {
		t.Fatal("repeated backfill changed the digest")
	}
}

func TestAddSaveLinksOldAndEmpty(t *testing.T) {
	marker := base64.RawURLEncoding.EncodeToString([]byte("https://example.com/old"))
	old := "<!-- radar-status:complete -->\n# 个人信息雷达 · 2026-09-22\n\n## 工作\n\n1. [旧条目](<https://example.com/old>) · GitHub\n   - 入选原因：相关\n   - 下一步：读原文\n   <!-- radar-url: " + marker + " -->\n\n---\n"
	updated, count, err := addSaveLinks(old, "owner/private", 1)
	if err != nil || count != 1 {
		t.Fatalf("old: count=%d err=%v", count, err)
	}
	if got := savedFields(t, updated).Get("description"); got != "入选原因：相关\n下一步：读原文" {
		t.Fatalf("invented description: %q", got)
	}
	previous := regexp.MustCompile(`(?m)^   - \[反馈这条\].*\n`).ReplaceAllString(updated, "")
	previous = regexp.MustCompile(`(?m)^\[查看我的收藏\].*\n`).ReplaceAllString(previous, "[查看我的收藏](<https://github.com/owner/private/issues?q=is%3Aissue+label%3Aradar-saved>)\n")
	if err := validateFeedbackBackfillDiff(previous, updated); err != nil {
		t.Fatalf("old digest diff changed content: %v", err)
	}
	empty := renderDigest("2026-09-28", nil, false, nil, 0, 0, "")
	updated, count, err = addSaveLinks(empty, "owner/private", 43)
	if err != nil || count != 0 || strings.Contains(updated, "[收藏](<") || !strings.Contains(updated, "[查看我的收藏]") {
		t.Fatalf("empty: count=%d err=%v", count, err)
	}
}
