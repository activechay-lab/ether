package schema

import (
	"fmt"

	"regexp"
	"strings"

	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/extend"
)

func pointer(prefix, key string) string {
	escaped := strings.NewReplacer("~", "~0", "/", "~1").Replace(key)
	return prefix + "/" + escaped
}

// ValidateSchema checks a schema itself. Plugin schemas (forPlugin) may not
// use the json kind and must follow the copy rules of SPEC §5.5.
func ValidateSchema(s Schema, forPlugin bool) error {
	seen := map[string]bool{}
	for _, g := range s.Groups {
		for _, fields := range [][]Field{g.Fields, g.More} {
			if err := validateSchemaFields(fields, "", seen, forPlugin); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSchemaFields(fields []Field, prefix string, seen map[string]bool, forPlugin bool) error {
	for _, f := range fields {
		at := pointer(prefix, f.Key)
		switch {
		case f.Key == "":
			return fmt.Errorf("%s: key is required", prefix)
		case f.Label.Empty():
			return fmt.Errorf("%s: label is required", at)
		case !Known(f.Kind):
			return fmt.Errorf("%s: unknown kind %q", at, f.Kind)
		case forPlugin && f.Kind == KindJSON:
			return fmt.Errorf("%s: json fields are not allowed in plugin settings", at)
		case seen[at]:
			// The same key may appear in fields and more as two halves of one
			// group (TRANSCODE's rtxVideo); only leaf duplicates are an error.
			if f.Kind != KindGroup {
				return fmt.Errorf("%s: key is used twice", at)
			}
		}
		seen[at] = true
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			return fmt.Errorf("%s: min is above max", at)
		}
		switch f.Kind {
		case KindSelect, KindMultiSelect, KindOrderedList:
			if len(f.Options) == 0 && f.OptionsFrom == "" {
				return fmt.Errorf("%s: %s needs options", at, f.Kind)
			}
		case KindBytes:
			if u := f.Unit.Source; u != "" && u != "GiB" && u != "MiB" {
				return fmt.Errorf("%s: bytes unit must be GiB or MiB, not %q", at, u)
			}
		case KindGroup:
			if err := validateSchemaFields(f.Fields, at, seen, forPlugin); err != nil {
				return err
			}
		}
		if forPlugin {
			if err := copyLint(at, "label", f.Label, 40); err != nil {
				return err
			}
			if !f.Help.Empty() {
				if err := copyLint(at, "hint", f.Help, 120); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var bannedWords = regexp.MustCompile(`(?i)\b(simply|easily|just)\b`)

func copyLint(at, what string, text extend.Text, maxRunes int) error {
	source := text.Source
	if source == "" {
		source = text.Resolved
	}
	if source == "" { // Plugin $t copy is linted from its strings at install.
		return nil
	}
	switch {
	case utf8.RuneCountInString(source) > maxRunes:
		return fmt.Errorf("%s: %s is over %d characters", at, what, maxRunes)
	case strings.Contains(source, "—"):
		return fmt.Errorf("%s: %s contains an em dash", at, what)
	case strings.Contains(source, "!"):
		return fmt.Errorf("%s: %s contains an exclamation mark", at, what)
	case bannedWords.MatchString(source):
		return fmt.Errorf("%s: %s uses a word the copy rules ban (simply, just, easily)", at, what)
	case what == "hint" && strings.Count(strings.TrimRight(source, "."), ". ") > 0:
		return fmt.Errorf("%s: hint must be one sentence", at)
	}
	return nil
}
