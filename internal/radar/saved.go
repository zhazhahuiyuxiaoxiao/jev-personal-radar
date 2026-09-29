package radar

import (
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

var savedItemHeading = regexp.MustCompile(`^\d+\. \[(.+)\]\(<([^>]+)>\)`)

// addSaveLinks copies only text already present in the digest. The URL marker
// anchors each item even in the older, category-based digest format.
func addSaveLinks(body, repo string, number int) (string, int, error) {
	if repo == "" || number < 1 {
		return "", 0, errors.New("repository and digest issue number are required")
	}
	lines := strings.Split(body, "\n")
	dateMatch := regexp.MustCompile(`(?m)^# 个人信息雷达 · ([0-9]{4}-[0-9]{2}-[0-9]{2})$`).FindStringSubmatch(body)
	if len(dateMatch) != 2 {
		return "", 0, errors.New("digest heading has an unsupported format")
	}
	var result []string
	var block []string
	count := 0
	flush := func() error {
		if len(block) == 0 {
			return nil
		}
		markerLine := -1
		for i, line := range block {
			if urlMarker.MatchString(line) {
				if markerLine >= 0 {
					return errors.New("multiple radar-url markers in one item")
				}
				markerLine = i
			}
		}
		if markerLine < 0 {
			return errors.New("digest item has no radar-url marker")
		}
		match := savedItemHeading.FindStringSubmatch(block[0])
		if len(match) != 3 {
			return errors.New("digest item heading has an unsupported format")
		}
		encoded := urlMarker.FindStringSubmatch(block[markerLine])[1]
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || string(decoded) != canonicalURL(match[2]) {
			return errors.New("digest item URL marker does not match its link")
		}
		title := html.UnescapeString(match[1])
		title = strings.NewReplacer(`\[`, `[`, `\]`, `]`, `\*`, `*`, `\_`, `_`, "\\`", "`").Replace(title)
		var description []string
		for _, line := range block[1:markerLine] {
			if strings.HasPrefix(line, "   - [收藏](<") || strings.HasPrefix(line, "   - [反馈这条](<") {
				continue
			}
			if strings.HasPrefix(line, "   - ") {
				description = append(description, strings.TrimPrefix(line, "   - "))
			}
		}
		values := url.Values{
			"template":    {"radar-saved.yml"},
			"title":       {"[Radar Saved] " + title},
			"item_title":  {title},
			"source_url":  {match[2]},
			"digest_url":  {fmt.Sprintf("https://github.com/%s/issues/%d", repo, number)},
			"description": {strings.Join(description, "\n")},
		}
		saveLine := "   - [收藏](<https://github.com/" + repo + "/issues/new?" + values.Encode() + ">)"
		feedbackValues := url.Values{"template": {"radar-feedback.yml"}, "title": {"[Radar Feedback] " + title}, "item": {title}, "digest_date": {dateMatch[1]}}
		feedbackLine := "   - [反馈这条](<https://github.com/" + repo + "/issues/new?" + feedbackValues.Encode() + ">)"
		for i, line := range block {
			if strings.HasPrefix(line, "   - [收藏](<") || strings.HasPrefix(line, "   - [反馈这条](<") {
				continue
			}
			if i == markerLine {
				result = append(result, saveLine, feedbackLine)
			}
			result = append(result, line)
		}
		count++
		block = nil
		return nil
	}
	for _, line := range lines {
		if savedItemHeading.MatchString(line) {
			if err := flush(); err != nil {
				return "", 0, err
			}
			block = append(block, line)
			continue
		}
		if len(block) > 0 {
			if strings.HasPrefix(line, "## ") || line == "---" {
				if err := flush(); err != nil {
					return "", 0, err
				}
				result = append(result, line)
			} else {
				block = append(block, line)
			}
		} else {
			result = append(result, line)
		}
	}
	if err := flush(); err != nil {
		return "", 0, err
	}
	updated := strings.Join(result, "\n")
	viewLine := "[查看我的收藏](<https://github.com/" + repo + "/issues?q=is%3Aissue+label%3Aradar-saved>)"
	if !strings.Contains(updated, "[查看我的收藏]") || !strings.Contains(updated, "[查看搜索关注]") || !strings.Contains(updated, "[查看我的反馈]") {
		updated = regexp.MustCompile(`(?m)^\[查看我的收藏\].*\n`).ReplaceAllString(updated, "")
		heading := regexp.MustCompile(`(?m)^# 个人信息雷达 · [^\n]+\n`)
		if !heading.MatchString(updated) {
			return "", 0, errors.New("digest heading has an unsupported format")
		}
		feedbackValues := url.Values{"template": {"radar-feedback.yml"}, "title": {"[Radar Feedback] 遗漏主题"}, "verdict": {"遗漏"}, "digest_date": {dateMatch[1]}}
		links := viewLine + " · [查看搜索关注](<https://github.com/" + repo + "/issues?q=is%3Aissue+label%3Aradar-focus>) · [查看我的反馈](<https://github.com/" + repo + "/issues?q=is%3Aissue+label%3Aradar-feedback>) · [记录遗漏](<https://github.com/" + repo + "/issues/new?" + feedbackValues.Encode() + ">)"
		updated = heading.ReplaceAllStringFunc(updated, func(s string) string { return s + "\n" + links + "\n" })
	}
	return updated, count, nil
}
