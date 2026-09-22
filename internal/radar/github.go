package radar

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type githubClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
	repo       string
}

type issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

func newGitHubClient(client *http.Client, token, repo string) (*githubClient, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo, " ?#%") {
		return nil, errors.New("GITHUB_REPOSITORY must be owner/repo")
	}
	return &githubClient{httpClient: client, baseURL: "https://api.github.com", token: token, repo: repo}, nil
}

func (g *githubClient) request(ctx context.Context, method, path string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "jev-personal-radar/0.1")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub API %s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(b), 180))
	}
	if result != nil {
		if err := json.Unmarshal(b, result); err != nil {
			return fmt.Errorf("decode GitHub response: %w", err)
		}
	}
	return nil
}

func (g *githubClient) listIssues(ctx context.Context, label, state string) ([]issue, error) {
	var all []issue
	for page := 1; page <= 3; page++ {
		path := fmt.Sprintf("/repos/%s/issues?state=%s&labels=%s&per_page=100&page=%d&sort=created&direction=desc", g.repo, state, url.QueryEscape(label), page)
		var batch []issue
		if err := g.request(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

func (g *githubClient) createIssue(ctx context.Context, title, body string, labels []string) (issue, error) {
	var out issue
	err := g.request(ctx, http.MethodPost, "/repos/"+g.repo+"/issues", map[string]any{"title": title, "body": body, "labels": labels}, &out)
	return out, err
}

func (g *githubClient) updateIssue(ctx context.Context, number int, body, state string) error {
	fields := map[string]any{}
	if body != "" {
		fields["body"] = body
	}
	if state != "" {
		fields["state"] = state
	}
	return g.request(ctx, http.MethodPatch, "/repos/"+g.repo+"/issues/"+strconv.Itoa(number), fields, nil)
}

func (g *githubClient) ensureLabel(ctx context.Context, name, color string) error {
	var label struct {
		Name string `json:"name"`
	}
	path := "/repos/" + g.repo + "/labels/" + url.PathEscape(name)
	err := g.request(ctx, http.MethodGet, path, nil, &label)
	if err == nil {
		return nil
	}
	// Only a missing label is an invitation to create one. Permission or network
	// errors are surfaced rather than hidden behind a second write attempt.
	if !strings.Contains(err.Error(), "HTTP 404") {
		return err
	}
	return g.request(ctx, http.MethodPost, "/repos/"+g.repo+"/labels", map[string]string{"name": name, "color": color}, nil)
}

func (g *githubClient) searchRepositories(ctx context.Context, query, category string, now time.Time) ([]Item, error) {
	q := query + " pushed:>=" + now.AddDate(0, 0, -7).Format("2006-01-02")
	path := "/search/repositories?q=" + url.QueryEscape(q) + "&sort=updated&order=desc&per_page=10"
	var response struct {
		Incomplete bool `json:"incomplete_results"`
		Items      []struct {
			FullName    string    `json:"full_name"`
			HTMLURL     string    `json:"html_url"`
			Description string    `json:"description"`
			UpdatedAt   time.Time `json:"updated_at"`
			Archived    bool      `json:"archived"`
			Fork        bool      `json:"fork"`
			Private     bool      `json:"private"`
		} `json:"items"`
	}
	if err := g.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	if response.Incomplete {
		return nil, errors.New("GitHub search returned incomplete results")
	}
	var items []Item
	for _, entry := range response.Items {
		if entry.Archived || entry.Fork || entry.Private || validatePublicURL(entry.HTMLURL) != nil {
			continue
		}
		items = append(items, Item{Title: entry.FullName, URL: entry.HTMLURL, Description: truncate(entry.Description, 300), IsRepo: true, AllowMiniMax: true, Source: "GitHub", Category: category, Published: entry.UpdatedAt})
	}
	return items, nil
}

func (g *githubClient) publicReadme(ctx context.Context, item Item) (string, error) {
	u, err := url.Parse(item.URL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
		return "", errors.New("not a public GitHub repository URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(parts[0]+parts[1], "%\\") {
		return "", errors.New("not a GitHub repository root URL")
	}
	var readme struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/readme"
	if err := g.request(ctx, http.MethodGet, path, nil, &readme); err != nil {
		return "", err
	}
	if readme.Encoding != "base64" || readme.Content == "" {
		return "", errors.New("README content is unavailable")
	}
	decoded, err := base64.StdEncoding.DecodeString(readme.Content)
	if err != nil {
		return "", fmt.Errorf("decode README: %w", err)
	}
	return readmeExcerpt(string(decoded)), nil
}

func isGitHubRepoRoot(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" && !strings.ContainsAny(parts[0]+parts[1], "%\\")
}
