package releaseparse

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
)

// Go's regexp word boundaries are ASCII-only. Use explicit Unicode letter
// boundaries so Cyrillic markers match without matching words inside titles.
var (
	reRussianSeason        = regexp.MustCompile(`(?i)(?:^|[^\pL\pN])сезон\s*:?\s*(\d{1,2})(?:$|[^\pL\pN])`)
	reRussianSeasonSuffix  = regexp.MustCompile(`(?i)(?:^|[^\pL\pN])(\d{1,2})\s*сезон(?:$|[^\pL\pN])`)
	reRussianEpisode       = regexp.MustCompile(`(?i)(?:^|[^\pL\pN])сери[яий]\s*:?\s*(\d{1,3})(?:\s*-\s*(\d{1,3}))?(?:\s+из\s+(\d{1,3}))?(?:$|[^\pL\pN-])`)
	reRussianEpisodeSuffix = regexp.MustCompile(`(?i)(?:^|[^\pL\pN])(\d{1,3})\s*серия(?:$|[^\pL\pN])`)
	reLeadingEpisode       = regexp.MustCompile(`^(\d{2,3})\.\s*(\pL)`)
)

// stripLeadingEpisode keeps ordinary numeric English movie titles intact.
// Zero padding or a Cyrillic title distinguishes this filename convention.
func stripLeadingEpisode(stem string) (string, int) {
	loc := reLeadingEpisode.FindStringSubmatchIndex(stem)
	if loc == nil {
		return stem, 0
	}
	firstTitleRune := []rune(stem[loc[4]:loc[5]])[0]
	if stem[0] != '0' && !unicode.In(firstTitleRune, unicode.Cyrillic) {
		return stem, 0
	}
	n, _ := strconv.Atoi(stem[loc[2]:loc[3]])
	if n == 0 {
		return stem, 0
	}
	return stem[loc[4]:], n
}

func parseRussianEpisodes(spaced string, info *core.ReleaseInfo) int {
	at := -1
	season := reRussianSeason.FindStringSubmatchIndex(spaced)
	if season == nil {
		season = reRussianSeasonSuffix.FindStringSubmatchIndex(spaced)
	}
	if season != nil {
		n, _ := strconv.Atoi(spaced[season[2]:season[3]])
		if n > 0 && n <= 50 {
			info.Seasons = []int{n}
			at = season[0]
		}
	}
	ep := reRussianEpisode.FindStringSubmatchIndex(spaced)
	if ep == nil {
		ep = reRussianEpisodeSuffix.FindStringSubmatchIndex(spaced)
	}
	if ep != nil {
		m := submatches(spaced, ep)
		first, _ := strconv.Atoi(m[1])
		last := first
		if len(m) > 2 && m[2] != "" {
			last, _ = strconv.Atoi(m[2])
		}
		total := 999
		if len(m) > 3 && m[3] != "" {
			total, _ = strconv.Atoi(m[3])
		}
		if first > 0 && last >= first && last <= total {
			for n := first; n <= last; n++ {
				info.Episodes = append(info.Episodes, n)
			}
			// Even short ranges are packs: debrid must select the requested file.
			info.FullSeason = last > first
			if at < 0 || ep[0] < at {
				at = ep[0]
			}
		}
	}
	if len(info.Seasons) > 0 && len(info.Episodes) == 0 {
		info.FullSeason = true
	}
	return at
}

// transliterateRussian uses a deterministic Russian romanisation. The folded
// variant handles ё written as е; comparisons still require complete titles.
func transliterateRussian(s string, foldYo bool) string {
	letters := map[rune]string{
		'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "yo",
		'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
		'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
		'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch",
		'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if foldYo && r == 'ё' {
			r = 'е'
		}
		if latin, ok := letters[r]; ok {
			b.WriteString(latin)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
