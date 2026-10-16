package episodeid

import (
	"reflect"
	"testing"
)

func TestFilenameIdentities(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []ID
	}{
		{"Seinfeld.S01E01.1080p.mkv", []ID{{1, 1}}},
		{"1x02 - The Stake Out.mp4", []ID{{1, 2}}},
		{"Season 2 Episode 3.mkv", []ID{{2, 3}}},
		{"s03e17e18_the_boyfriend.mkv", []ID{{3, 17}, {3, 18}}},
		{"Seinfeld S09E23-24 The Finale.mkv", []ID{{9, 23}, {9, 24}}},
		{"/media/Season 01/1080p_1989.mkv", nil},
		{"S00E01.mkv", nil},
		{"s03e17e19.mkv", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.name); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse=%v want %v", got, tc.want)
			}
		})
	}
}
