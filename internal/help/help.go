// Package help serves the markdown-driven Help section. Source files live in
// internal/help/docs/*.md and are embedded into the binary so the appliance
// has zero external dependencies on the deployed Pi.
//
// The pattern mirrors plex-dashboard's help system but with three changes:
// (1) docs are go:embed'd, not read from disk; (2) markdown → HTML happens
// server-side via goldmark, not in JavaScript; (3) topic order and titles
// are captured in a single ordered slice rather than parallel maps.
package help

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed docs/*.md
var docsFS embed.FS

// Topic is one help article.
type Topic struct {
	Slug  string // filename without extension, used in URL
	Title string // human-readable from the first H1, falls back to slug
	Order int    // numeric prefix on filename if present (e.g. "01-foo.md")
}

// All returns every topic, sorted by Order then Title.
func All() ([]Topic, error) {
	entries, err := fs.ReadDir(docsFS, "docs")
	if err != nil {
		return nil, fmt.Errorf("read docs: %w", err)
	}
	var topics []Topic
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		t := topicFromFilename(e.Name())
		if title := firstH1(e.Name()); title != "" {
			t.Title = title
		}
		topics = append(topics, t)
	}
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Order != topics[j].Order {
			return topics[i].Order < topics[j].Order
		}
		return topics[i].Title < topics[j].Title
	})
	return topics, nil
}

// Render returns the HTML for a topic by slug. Returns an error if the slug
// doesn't resolve to an embedded file.
func Render(slug string) (htmlOut string, title string, err error) {
	for _, ext := range []string{".md"} {
		name := slug + ext
		body, rerr := fs.ReadFile(docsFS, "docs/"+name)
		if rerr != nil {
			continue
		}
		md := goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(html.WithUnsafe()),
		)
		var buf bytes.Buffer
		if err := md.Convert(body, &buf); err != nil {
			return "", "", fmt.Errorf("render markdown: %w", err)
		}
		t := topicFromFilename(name)
		if h1 := extractH1(string(body)); h1 != "" {
			t.Title = h1
		}
		return buf.String(), t.Title, nil
	}
	return "", "", fmt.Errorf("topic not found: %s", slug)
}

func topicFromFilename(name string) Topic {
	slug := strings.TrimSuffix(name, ".md")
	t := Topic{Slug: slug, Title: humanize(slug)}
	// Pull leading "NN-" prefix as the order if present.
	if len(slug) > 3 && slug[2] == '-' {
		var n int
		if _, err := fmt.Sscanf(slug[:2], "%d", &n); err == nil {
			t.Order = n
			t.Title = humanize(slug[3:])
		}
	}
	return t
}

func humanize(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstH1(name string) string {
	body, err := fs.ReadFile(docsFS, "docs/"+name)
	if err != nil {
		return ""
	}
	return extractH1(string(body))
}

func extractH1(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}
