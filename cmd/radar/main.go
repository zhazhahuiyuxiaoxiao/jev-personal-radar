package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"jev-personal-radar/internal/radar"
)

func main() {
	configPath := flag.String("config", "config.json", "private JSON configuration")
	date := flag.String("date", "", "digest date in Asia/Shanghai (YYYY-MM-DD); defaults to today")
	dryRun := flag.Bool("dry-run", false, "preview without writing GitHub issues or calling Jev/MiniMax")
	retrySummaries := flag.Bool("retry-summaries", false, "add MiniMax explanations to today's completed private digest")
	flag.Parse()
	if err := radar.Run(context.Background(), radar.Options{
		ConfigPath:     *configPath,
		Date:           *date,
		DryRun:         *dryRun,
		RetrySummaries: *retrySummaries,
		Out:            os.Stdout,
		GitHubToken:    os.Getenv("GITHUB_TOKEN"),
		Repository:     os.Getenv("GITHUB_REPOSITORY"),
		JevKey:         os.Getenv("TYPESAFE_API_KEY"),
		MiniMaxKey:     os.Getenv("MINIMAX_API_KEY"),
		Now:            time.Now(),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "radar:", err)
		os.Exit(1)
	}
}
