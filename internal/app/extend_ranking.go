package app

import (
	"fmt"

	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

// registerRankingDescriptors registers the built-in rule fields (WS1a).
// Copy is the console's existing English, declared with extend.T so the
// same translations carry over. Ranking steps come only from plugins: Ether
// ranks with the profile's quality order and custom format scores. The
// resolver gets the registry here because it is built before the
// extensibility runtime.
func (a *App) registerRankingDescriptors(r *extend.Registry) error {
	if err := r.Register(builtinRankingDescriptors()...); err != nil {
		return err
	}
	if a.resolver != nil {
		a.resolver.SetExtendRegistry(r)
	}
	return nil
}

// builtinRankingDescriptors builds every ruleField descriptor Ether ships,
// in current console order. Rule fields come from releasepolicy.
// BuiltinFields, so the registry cannot drift from the code that validates
// and runs them.
func builtinRankingDescriptors() []extend.Descriptor {
	return ruleFieldDescriptors()
}
func ruleFieldDescriptors() []extend.Descriptor {
	// Presentation per field: label and hint copy, suggested choices, unit.
	// Validation shape (class, operators, single-value) comes from
	// releasepolicy so compile and the picker read the same spec.
	type fieldCopy struct {
		label   extend.Text
		hint    extend.Text
		choices []extend.DescriptorOption
		unit    string
	}
	languageCodes := extend.Options(
		"en", "es", "es-419", "es-ES", "pt", "pt-BR", "pt-PT", "fr", "de", "it",
		"nl", "sv", "da", "no", "nb", "nn", "fi", "is", "ja", "ko", "zh", "pl",
		"cs", "hu", "ro", "tr", "ru", "uk", "ar", "he", "hi", "th", "vi", "id",
	)
	copy := map[string]fieldCopy{
		"language":  {label: extend.T("Declared audio language"), choices: languageCodes},
		"subtitles": {label: extend.T("Declared subtitle language"), choices: languageCodes},
		"audio_matches_original": {
			label: extend.T("Original-language audio"),
			hint:  extend.T("Declared audio must match this title's original language. Subtitles do not count, and a filename alone cannot make the comparison."),
		},
		"name":  {label: extend.T("Release name")},
		"group": {label: extend.T("Release group")},
		"source": {
			label:   extend.T("Release type"),
			choices: extend.Options("webdl", "webrip", "bluray", "remux", "hdtv", "dvd", "cam"),
		},
		"video": {
			label:   extend.T("Video codec"),
			choices: extend.Options("h264", "hevc", "av1", "mpeg2", "xvid"),
		},
		"audio": {
			label:   extend.T("Audio codec"),
			choices: extend.Options("aac", "ac3", "eac3", "truehd", "dts", "dtshd", "opus"),
		},
		"height":   {label: extend.T("Resolution"), unit: "p"},
		"size_gib": {label: extend.T("Size (GiB)"), unit: "GiB"},
		"hdr": {
			label:   extend.T("HDR"),
			choices: extend.Options("hdr10", "dv"),
		},
		"edition":  {label: extend.T("Edition")},
		"indexer":  {label: extend.T("Indexer")},
		"protocol": {label: extend.T("Protocol"), choices: extend.Options("usenet", "torrent", "http")},
	}
	fields := releasepolicy.BuiltinFields()
	ds := make([]extend.Descriptor, 0, len(fields))
	for i, f := range fields {
		c, ok := copy[f.ID]
		if !ok {
			panic(fmt.Sprintf("extend ranking: no descriptor copy for rule field %q", f.ID))
		}
		d := extend.Descriptor{
			Kind: extend.KindRuleField, ID: f.ID, Owner: extend.Builtin,
			Label: c.label, Hint: c.hint, Order: i,
		}.WithMeta(extend.RuleFieldMeta{
			ValueClass:      f.ValueClass,
			Operators:       f.Operators,
			DefaultOperator: f.DefaultOperator,
			Choices:         c.choices,
			Unit:            c.unit,
			SingleValue:     f.SingleValue,
		})
		ds = append(ds, d)
	}
	return ds
}
