// Package episodeid is the shared filename convention used by importing,
// playback, episode browsing and episode weights.
package episodeid

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type ID struct{ Season, Episode int }

func (id ID) String() string { return fmt.Sprintf("S%02dE%02d", id.Season, id.Episode) }

var code = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s([0-9]{1,2})[ ._-]*e([0-9]{1,2})`)
var cross = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([0-9]{1,2})x([0-9]{1,2})`)
var extra = regexp.MustCompile(`(?i)^[ ._]*e([0-9]{1,2})`)
var span = regexp.MustCompile(`(?i)^-[ ._]*(?:e)?([0-9]{1,2})(?:[^0-9]|$)`)
var spelled = regexp.MustCompile(`(?i)season[ ._-]*([0-9]{1,2})[ ._-]*episode[ ._-]*([0-9]{1,2})`)

// Parse recognizes S01E02, S01 E02, 1x02, Season 1 Episode 2, and explicit
// multi-part names such as S03E17E18 or S03E17-18. It never interprets 1080p,
// years, or arbitrary bare numbers as episode identities.
func Parse(path string) []ID {
	name := filepath.Base(path)
	for _, pattern := range []*regexp.Regexp{code, cross, spelled} {
		m := pattern.FindStringSubmatchIndex(name)
		if m == nil {
			continue
		}
		s, _ := strconv.Atoi(name[m[2]:m[3]])
		e, _ := strconv.Atoi(name[m[4]:m[5]])
		if s < 1 || e < 1 {
			return nil
		}
		ids := []ID{{s, e}}
		tail := name[m[1]:]
		for len(ids) < 4 {
			next := extra.FindStringSubmatchIndex(tail)
			if next == nil {
				break
			}
			n, _ := strconv.Atoi(tail[next[2]:next[3]])
			if n != ids[len(ids)-1].Episode+1 {
				return nil
			}
			ids = append(ids, ID{s, n})
			tail = tail[next[1]:]
		}
		if len(ids) == 1 {
			if next := span.FindStringSubmatch(tail); next != nil {
				n, _ := strconv.Atoi(next[1])
				if n > e && n-e < 4 {
					for e++; e <= n; e++ {
						ids = append(ids, ID{s, e})
					}
				}
			}
		}
		return ids
	}
	return nil
}

func Contains(ids []ID, season, episode int) bool {
	for _, id := range ids {
		if id.Season == season && id.Episode == episode {
			return true
		}
	}
	return false
}

func Prefix(ids []ID) string {
	if len(ids) == 0 {
		return ""
	}
	p := strings.ToLower(ids[0].String())
	for _, id := range ids[1:] {
		p += fmt.Sprintf("e%02d", id.Episode)
	}
	return p
}
