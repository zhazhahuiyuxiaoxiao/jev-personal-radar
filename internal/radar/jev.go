package radar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	jevModel       = "jev-1.13.0"
	maxJevRequests = 20
	maxRequestSize = 8 << 10
)

type jevClient struct {
	httpClient *http.Client
	endpoint   string
	key        string
}

type jevResult struct {
	Work   float64
	Life   float64
	Tokens int
}

func (j *jevClient) evaluate(ctx context.Context, item Item, topics map[string]Topic) (jevResult, error) {
	var result jevResult
	request := map[string]any{
		"model": jevModel,
		"state": map[string]any{
			"title":       truncate(item.Title, 180),
			"description": truncate(item.Description, 500),
			"source":      item.Source,
		},
		"questions": map[string]any{
			"work":     map[string]any{"type": "noul", "instructions": fmt.Sprintf("Would this specific item help someone working on %v? Also value substantive, runnable full-stack projects with frontend, backend, database and setup or deployment guidance, even without AI. Any programming language is acceptable; Go is not required. Judge only from the supplied public title and description. Answer no to unrelated hype or generic marketing.", topics[Work].Keywords)},
			"learning": map[string]any{"type": "noul", "instructions": fmt.Sprintf("Would this specific item help someone learning or improving productivity around %v? Also value substantive, runnable full-stack projects with frontend, backend, database and setup or deployment guidance, even without AI. Any programming language is acceptable; Go is not required. Judge only from the supplied public title and description. Answer no to unrelated hype or generic marketing.", topics[Life].Keywords)},
		},
	}
	b, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	if len(b) > maxRequestSize {
		return result, errors.New("Jev request exceeds 8 KiB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.endpoint, bytes.NewReader(b))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+j.key)
	req.Header.Set("Content-Type", "application/json")
	response, err := j.httpClient.Do(req)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return result, fmt.Errorf("Jev HTTP %d", response.StatusCode)
	}
	var data struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		} `json:"answers"`
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&data); err != nil {
		return result, err
	}
	a, okA := data.Answers["work"]
	c, okC := data.Answers["learning"]
	if data.Model != jevModel || !okA || !okC || a.Type != "noul" || c.Type != "noul" || a.Noul == nil || c.Noul == nil || *a.Noul < 0 || *a.Noul > 1 || *c.Noul < 0 || *c.Noul > 1 || data.Usage.InputTokens <= 0 {
		return result, errors.New("Jev response has unexpected model, answer or usage")
	}
	return jevResult{Work: *a.Noul, Life: *c.Noul, Tokens: data.Usage.InputTokens}, nil
}

func newJevClient(client *http.Client, key string) *jevClient {
	return &jevClient{httpClient: client, endpoint: "https://api.typesafe.ai/v1/systemone", key: key}
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many HTTP redirects")
		}
		return validatePublicURL(req.URL.String())
	}}
}
