package radar

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	Work = "work"
	Life = "life"
)

type Topic struct {
	Keywords      []string `json:"keywords"`
	GitHubQueries []string `json:"github_queries"`
}

type Feed struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	Category     string `json:"category"`
	AllowMiniMax bool   `json:"allow_minimax"`
}

type Config struct {
	Topics map[string]Topic `json:"topics"`
	Feeds  []Feed           `json:"feeds"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read config: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("decode config: %w", err)
	}
	if len(c.Topics) != 2 {
		return c, errors.New("config must define exactly work and life topics")
	}
	for _, category := range []string{Work, Life} {
		topic, ok := c.Topics[category]
		if !ok || len(topic.Keywords) == 0 {
			return c, fmt.Errorf("%s topic needs keywords", category)
		}
		for _, q := range topic.GitHubQueries {
			if strings.TrimSpace(q) == "" {
				return c, fmt.Errorf("%s topic has empty GitHub query", category)
			}
		}
	}
	if len(c.Feeds) > 10 {
		return c, errors.New("at most 10 feeds are allowed")
	}
	for _, feed := range c.Feeds {
		if feed.Category != Work && feed.Category != Life {
			return c, fmt.Errorf("feed %q has invalid category", feed.Name)
		}
		if err := validatePublicURL(feed.URL); err != nil {
			return c, fmt.Errorf("feed %q: %w", feed.Name, err)
		}
	}
	return c, nil
}
