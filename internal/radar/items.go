package radar

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Item struct {
	Title       string
	URL         string
	Description string
	Source      string
	Category    string
	Published   time.Time
	InboxNumber int
	Note        string
	Score       float64
	Reason      string
	Action      string
}

func validatePublicURL(raw string) error {
	if strings.ContainsAny(raw, "<> \t\r\n") {
		return fmt.Errorf("invalid HTTP(S) URL %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("invalid HTTP(S) URL %q", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("local URL is not allowed: %q", raw)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return fmt.Errorf("private network URL is not allowed: %q", raw)
	}
	return nil
}

func canonicalURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for key := range q {
		if strings.HasPrefix(strings.ToLower(key), "utm_") || key == "ref" || key == "source" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

func keywordScore(item Item, keywords []string) (float64, string) {
	haystack := strings.ToLower(item.Title + " " + item.Description + " " + item.Source)
	var hits []string
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword != "" && containsKeyword(haystack, strings.ToLower(keyword)) {
			hits = append(hits, keyword)
		}
	}
	if len(hits) == 0 {
		return 0, ""
	}
	return float64(len(hits)), hits[0]
}

func containsKeyword(text, keyword string) bool {
	for start := 0; start < len(text); {
		index := strings.Index(text[start:], keyword)
		if index < 0 {
			return false
		}
		index += start
		end := index + len(keyword)
		leftOK, rightOK := true, true
		if index > 0 {
			left, _ := utf8.DecodeLastRuneInString(text[:index])
			leftOK = !unicode.IsLetter(left) && !unicode.IsDigit(left)
		}
		if end < len(text) {
			right, _ := utf8.DecodeRuneInString(text[end:])
			rightOK = !unicode.IsLetter(right) && !unicode.IsDigit(right)
		}
		if leftOK && rightOK {
			return true
		}
		start = end
	}
	return false
}

func rankAndSelect(items []Item) []Item {
	var result []Item
	for _, category := range []string{Work, Life} {
		var group []Item
		for _, item := range items {
			if item.Category == category {
				group = append(group, item)
			}
		}
		sort.SliceStable(group, func(i, j int) bool {
			if (group[i].InboxNumber > 0) != (group[j].InboxNumber > 0) {
				return group[i].InboxNumber > 0
			}
			if group[i].Score != group[j].Score {
				return group[i].Score > group[j].Score
			}
			return group[i].Published.After(group[j].Published)
		})
		if len(group) > 3 {
			group = group[:3]
		}
		result = append(result, group...)
	}
	return result
}
