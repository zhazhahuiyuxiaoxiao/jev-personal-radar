package radar

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var htmlTags = regexp.MustCompile(`<[^>]*>`)

type xmlFeed struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			PubDate     string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
	Entries []struct {
		Title string `xml:"title"`
		Link  []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Summary string `xml:"summary"`
		Updated string `xml:"updated"`
		Date    string `xml:"published"`
	} `xml:"entry"`
}

func parseDate(s string) time.Time {
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func cleanText(s string) string {
	return truncate(html.UnescapeString(htmlTags.ReplaceAllString(s, " ")), 300)
}

func parseFeed(data []byte, feed Feed, now time.Time) ([]Item, error) {
	var doc xmlFeed
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var items []Item
	add := func(title, link, desc, date string) {
		if validatePublicURL(link) != nil {
			return
		}
		published := parseDate(date)
		if !published.IsZero() && (published.Before(now.AddDate(0, 0, -7)) || published.After(now.Add(24*time.Hour))) {
			return
		}
		items = append(items, Item{Title: cleanText(title), URL: link, Description: cleanText(desc), Source: feed.Name, Category: feed.Category, Published: published})
	}
	for _, entry := range doc.Channel.Items {
		add(entry.Title, entry.Link, entry.Description, entry.PubDate)
	}
	for _, entry := range doc.Entries {
		link := ""
		for _, candidate := range entry.Link {
			if candidate.Rel == "" || candidate.Rel == "alternate" {
				link = candidate.Href
				break
			}
		}
		date := entry.Date
		if date == "" {
			date = entry.Updated
		}
		add(entry.Title, link, entry.Summary, date)
	}
	if len(doc.Channel.Items) == 0 && len(doc.Entries) == 0 {
		return nil, fmt.Errorf("feed contains no RSS items or Atom entries")
	}
	if len(items) > 15 {
		items = items[:15]
	}
	return items, nil
}

func fetchFeed(ctx context.Context, client *http.Client, feed Feed, now time.Time) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jev-personal-radar/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 2<<20 {
		return nil, fmt.Errorf("feed exceeds 2 MiB")
	}
	return parseFeed(b, feed, now)
}
