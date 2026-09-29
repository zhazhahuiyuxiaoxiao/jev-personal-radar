package radar

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var issueFormHeading = regexp.MustCompile(`(?m)^### (.+)$`)

func formFields(body string) map[string]string {
	fields := make(map[string]string)
	locs := issueFormHeading.FindAllStringSubmatchIndex(body, -1)
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		fields[strings.TrimSpace(body[loc[2]:loc[3]])] = strings.TrimSpace(body[loc[1]:end])
	}
	return fields
}

func savedIssueNumber(raw, repo string) (int, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.RawQuery != "" || u.Fragment != "" {
		return 0, errors.New("invalid saved Issue URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0]+"/"+parts[1] != repo || parts[2] != "issues" {
		return 0, errors.New("saved Issue belongs to another repository")
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n < 1 {
		return 0, errors.New("invalid saved Issue number")
	}
	return n, nil
}
