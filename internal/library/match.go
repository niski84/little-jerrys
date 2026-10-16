// Package library imports a Seinfeld collection from removable media into
// local storage, using the same bundled episode catalog as the playback UI.
package library

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/niski84/little-jerrys/internal/episodeid"
	"github.com/niski84/little-jerrys/internal/tmdb"
)

type Episode struct {
	ID    episodeid.ID
	Title string
}

type Catalog struct {
	Episodes []Episode
	byID     map[episodeid.ID]Episode
}

func NewCatalog(cache *tmdb.Cache) (*Catalog, error) {
	c := &Catalog{byID: map[episodeid.ID]Episode{}}
	seasons, err := cache.Seasons()
	if err != nil {
		return nil, err
	}
	for _, s := range seasons {
		season, err := cache.Season(s)
		if err != nil {
			return nil, err
		}
		for _, e := range season.Episodes {
			ep := Episode{episodeid.ID{Season: s, Episode: e.EpisodeNumber}, e.Name}
			c.Episodes = append(c.Episodes, ep)
			c.byID[ep.ID] = ep
		}
	}
	if len(c.Episodes) == 0 {
		return nil, fmt.Errorf("the bundled episode catalog is empty")
	}
	return c, nil
}

var partSuffix = regexp.MustCompile(`(?i)(?:\s*\(([12])\)|[ ._-]+part[ ._-]*([12]))$`)
var seasonFolder = regexp.MustCompile(`(?i)(?:^|[/\\])season[ ._-]*0*([1-9])(?:[/\\]|$)`)
var folderEpisode = regexp.MustCompile(`(?i)^(?:episode[ ._-]*)?0*([0-9]{1,2})(?:[ ._-]+|$)`)
var junk = regexp.MustCompile(`(?i)\b(?:1080p|720p|2160p|480p|bluray|blu ray|brrip|webrip|web dl|hdtv|dvdrip|x264|x265|h264|h265|hevc|aac|ac3|mkv|mp4)\b`)
var seasonCode = regexp.MustCompile(`(?i)(?:s[0-9]{1,2}[ ._-]*e[0-9]{1,2}(?:e[0-9]{1,2})*|[0-9]{1,2}x[0-9]{1,2}|season[ ._-]*[0-9]{1,2}[ ._-]*episode[ ._-]*[0-9]{1,2})`)

func normalized(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func baseTitle(s string) string { return normalized(partSuffix.ReplaceAllString(s, "")) }

// Match uses explicit identities, punctuation-insensitive titles, season folder
// hints and conservative typo correction. Conflicting number/title evidence is
// reported instead of silently assigning the wrong episode.
func (c *Catalog) Match(path string, duration float64) ([]episodeid.ID, string, error) {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	ids := episodeid.Parse(stem)
	for _, id := range ids {
		if _, ok := c.byID[id]; !ok {
			return nil, "", fmt.Errorf("%s is outside the Seinfeld catalog; rename with the aired episode number and title", id)
		}
	}
	hint := 0
	if m := seasonFolder.FindStringSubmatch(filepath.ToSlash(path)); m != nil {
		hint, _ = strconv.Atoi(m[1])
	}
	if len(ids) == 0 && hint > 0 {
		if m := folderEpisode.FindStringSubmatch(stem); m != nil {
			e, _ := strconv.Atoi(m[1])
			id := episodeid.ID{Season: hint, Episode: e}
			if _, ok := c.byID[id]; ok {
				ids = []episodeid.ID{id}
			}
		}
	}
	clean := normalized(seasonCode.ReplaceAllString(stem, " "))
	clean = strings.TrimSpace(junk.ReplaceAllString(clean, " "))
	clean = normalized(clean)
	clean = strings.TrimPrefix(clean, "seinfeld ")
	clean = strings.ReplaceAll(clean, " part ", " ")
	var matches []Episode
	for _, e := range c.Episodes {
		if hint > 0 && e.ID.Season != hint {
			continue
		}
		title := normalized(e.Title)
		if title == clean || (len(title) >= 7 && strings.Contains(" "+clean+" ", " "+title+" ")) {
			matches = append(matches, e)
		}
	}
	// A title without a part suffix can represent both parts of a double
	// episode, but only a full-length combined file establishes coverage.
	if len(matches) == 0 {
		for _, e := range c.Episodes {
			if hint > 0 && e.ID.Season != hint {
				continue
			}
			title := baseTitle(e.Title)
			if len(title) >= 7 && strings.Contains(" "+clean+" ", " "+title+" ") {
				matches = append(matches, e)
			}
		}
	}
	// Prefer the most specific title: "The Soup" is also a substring of
	// "The Soup Nazi", but identifies a different episode.
	if len(matches) > 1 {
		longest := 0
		for _, e := range matches {
			longest = max(longest, len(normalized(e.Title)))
		}
		filtered := matches[:0]
		for _, e := range matches {
			if len(normalized(e.Title)) == longest {
				filtered = append(filtered, e)
			}
		}
		matches = filtered
	}
	if len(ids) > 0 {
		if len(ids) > 1 && duration < float64(len(ids))*17.5*60 {
			return nil, "", fmt.Errorf("filename names %d episodes but the video is too short to verify all parts; correct the episode numbering", len(ids))
		}
		if len(matches) > 0 {
			found := false
			for _, e := range matches {
				if episodeid.Contains(ids, e.ID.Season, e.ID.Episode) {
					found = true
				}
			}
			if !found {
				return nil, "", fmt.Errorf("episode number %s conflicts with title %q; rename using the correct SxxExx and title", ids[0], stem)
			}
			if len(ids) > 1 {
				for _, id := range ids {
					matched := false
					for _, e := range matches {
						if e.ID == id {
							matched = true
						}
					}
					if !matched {
						return nil, "", fmt.Errorf("combined filename includes %s but its title does not identify that part; correct the title and episode range", id)
					}
				}
			}
			if len(ids) == 1 && len(matches) == 2 && consecutiveParts(matches) && duration >= 35*60 && episodeid.Contains(ids, matches[0].ID.Season, matches[0].ID.Episode) {
				return []episodeid.ID{matches[0].ID, matches[1].ID}, "episode number and combined two-part title", nil
			}
		}
		return ids, "episode number", nil
	}
	if len(matches) == 1 {
		return []episodeid.ID{matches[0].ID}, "episode title", nil
	}
	if len(matches) == 2 && consecutiveParts(matches) && duration >= 35*60 {
		return []episodeid.ID{matches[0].ID, matches[1].ID}, "combined two-part title and duration", nil
	}
	if len(matches) > 1 {
		return nil, "", fmt.Errorf("ambiguous title %q; include SxxExx (or SxxExxEyy for a combined episode)", stem)
	}
	// Typo correction only accepts a close, uniquely best full title. No
	// guessing from release group names or a generic word such as "episode".
	best, runner := 1000, 1000
	var candidate Episode
	for _, e := range c.Episodes {
		if hint > 0 && e.ID.Season != hint {
			continue
		}
		title := normalized(e.Title)
		d := editDistance(clean, title)
		if d < best {
			runner = best
			best = d
			candidate = e
		} else if d < runner {
			runner = d
		}
	}
	if len(clean) >= 9 && best <= 2 && runner-best >= 2 {
		return []episodeid.ID{candidate.ID}, "unique close title", nil
	}
	return nil, "", fmt.Errorf("could not identify %q; rename it like Seinfeld.S01E01.The.Seinfeld.Chronicles.mkv", stem)
}

func consecutiveParts(e []Episode) bool {
	return len(e) == 2 && e[0].ID.Season == e[1].ID.Season && e[1].ID.Episode == e[0].ID.Episode+1 && baseTitle(e[0].Title) == baseTitle(e[1].Title)
}

func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	row := make([]int, len(y)+1)
	for j := range row {
		row[j] = j
	}
	for i, ar := range x {
		prev := row[0]
		row[0] = i + 1
		for j, br := range y {
			old := row[j+1]
			cost := 0
			if ar != br {
				cost = 1
			}
			row[j+1] = min(row[j+1]+1, row[j]+1, prev+cost)
			prev = old
		}
	}
	return row[len(y)]
}

func (c *Catalog) filename(ids []episodeid.ID, ext string) string {
	title := c.byID[ids[0]].Title
	if len(ids) > 1 {
		title = partSuffix.ReplaceAllString(title, "")
	}
	return episodeid.Prefix(ids) + "_" + strings.ReplaceAll(normalized(title), " ", "_") + strings.ToLower(ext)
}
