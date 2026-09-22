package radar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 { // carrier-grade NAT
			return false
		}
		if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) { // benchmark networks
			return false
		}
	}
	return true
}

func safePublicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	dialer := net.Dialer{Timeout: 5 * time.Second}
	for _, address := range addresses {
		if !publicIP(address.IP) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("public page resolves only to non-public addresses")
}

func publicPageClient(base *http.Client) *http.Client {
	client := *base
	client.Timeout = 8 * time.Second
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many public page redirects")
		}
		return validatePublicURL(req.URL.String())
	}
	if base.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = safePublicDial
		client.Transport = transport
	} else if transport, ok := base.Transport.(*http.Transport); ok {
		copy := transport.Clone()
		copy.Proxy = nil
		copy.DialContext = safePublicDial
		client.Transport = copy
	}
	return &client
}

func readableText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "nav", "footer", "header", "form", "svg":
				return
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func publicPageText(data []byte) (string, string, error) {
	root, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return "", "", err
	}
	var description string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" && (strings.EqualFold(nodeAttr(n, "name"), "description") || strings.EqualFold(nodeAttr(n, "property"), "og:description")) && description == "" {
			description = truncate(nodeAttr(n, "content"), 300)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	main := findNode(root, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "main" })
	if main == nil {
		main = findNode(root, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "article" })
	}
	if main == nil {
		main = findNode(root, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "body" })
	}
	if main == nil {
		if description != "" {
			return description, description, nil
		}
		return "", "", errors.New("public page has no readable body")
	}
	content := readableText(main)
	if description != "" && !strings.Contains(content, description) {
		content = description + " " + content
	}
	content = truncate(content, maxSummarySource)
	if description == "" {
		description = truncate(content, 300)
	}
	return description, content, nil
}

func fetchPublicPage(ctx context.Context, base *http.Client, link string) (string, string, error) {
	if err := validatePublicURL(link); err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "jev-personal-radar/0.1")
	resp, err := publicPageClient(base).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("public page HTTP %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "text/html") {
		return "", "", errors.New("public page is not HTML")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > 1<<20 {
		return "", "", errors.New("public page exceeds 1 MiB")
	}
	return publicPageText(data)
}
