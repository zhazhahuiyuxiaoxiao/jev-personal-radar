package radar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

func backfillSaved(ctx context.Context, o Options) error {
	if o.Repository == "" || o.GitHubToken == "" {
		return errors.New("GITHUB_REPOSITORY and GITHUB_TOKEN are required for saved-link backfill")
	}
	client := o.HTTPClient
	if client == nil {
		client = httpClient()
	}
	gh, err := newGitHubClient(client, o.GitHubToken, o.Repository)
	if err != nil {
		return err
	}
	if o.GitHubURL != "" {
		gh.baseURL = o.GitHubURL
	}
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	issues, err := gh.listIssues(ctx, "radar-digest", "all")
	if err != nil {
		return err
	}
	changed, total := 0, 0
	for _, entry := range issues {
		if !strings.HasPrefix(entry.Title, "[Radar] ") || !strings.Contains(entry.Body, "<!-- radar-status:complete -->") {
			continue
		}
		updated, count, err := addSaveLinks(entry.Body, o.Repository, entry.Number)
		if err != nil {
			return fmt.Errorf("Issue #%d: %w", entry.Number, err)
		}
		total += count
		if updated == entry.Body {
			_, _ = fmt.Fprintf(out, "Issue #%d: unchanged (%d items)\n", entry.Number, count)
			continue
		}
		changed++
		_, _ = fmt.Fprintf(out, "Issue #%d: %d items, %d -> %d bytes\n", entry.Number, count, len(entry.Body), len(updated))
		if !o.ApplyBackfill {
			continue
		}
		latest, err := gh.getIssue(ctx, entry.Number)
		if err != nil {
			return err
		}
		if latest.Body != entry.Body || latest.State != entry.State {
			return fmt.Errorf("Issue #%d changed during backfill; no update was made", entry.Number)
		}
		if err := gh.updateIssue(ctx, entry.Number, updated, ""); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(out, "Complete: %d digests need changes, %d items; apply=%t\n", changed, total, o.ApplyBackfill)
	return nil
}
