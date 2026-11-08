package extend

import (
	"bytes"
	"encoding/json"
)

// Ranking step and rule field descriptor metadata (SPEC §5.9, WS1a).
// Mirrored as TypeScript types next to the console consumer in
// web/src/pages/releases/ReleasePreferences.tsx.

// DescriptorOption is one selectable value in a descriptor editor. Label is
// optional: when empty the console shows Value.
type DescriptorOption struct {
	Value string `json:"value"`
	Label Text   `json:"label,omitempty"`
}

// Options builds DescriptorOptions from plain values whose display text is
// the value itself (codec names, source kinds, language codes).
func Options(values ...string) []DescriptorOption {
	out := make([]DescriptorOption, len(values))
	for i, v := range values {
		out[i] = DescriptorOption{Value: v}
	}
	return out
}

// RankingEditorKind names the inline editor a plugin ranking step offers
// for its settings on a quality profile.
type RankingEditorKind string

const (
	RankingEditorNone        RankingEditorKind = "none"
	RankingEditorOrderedList RankingEditorKind = "ordered_list"
	RankingEditorChoice      RankingEditorKind = "choice"
	RankingEditorRange       RankingEditorKind = "range"
	RankingEditorToggle      RankingEditorKind = "toggle"
)

// UnmarshalJSON accepts a plain editor kind string, null, or a plugin
// manifest editor object ({"kind": "choice"}).
func (k *RankingEditorKind) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*k = RankingEditorNone
		return nil
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		if s == "" {
			*k = RankingEditorNone
		} else {
			*k = RankingEditorKind(s)
		}
		return nil
	}
	var obj struct {
		Kind   string `json:"kind"`
		Editor string `json:"editor"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return err
	}
	switch {
	case obj.Kind != "":
		*k = RankingEditorKind(obj.Kind)
	case obj.Editor != "":
		*k = RankingEditorKind(obj.Editor)
	default:
		*k = RankingEditorNone
	}
	return nil
}

// RankingStepMeta is the Meta of a KindRankingStep descriptor.
type RankingStepMeta struct {
	// Editor is the inline editor kind for this step's own setting.
	Editor RankingEditorKind `json:"editor"`
	// Options are the values the editor offers: the ordered choices for
	// ordered_list and choice editors, the selectable bounds for range.
	Options []DescriptorOption `json:"options,omitempty"`
	// Choices are optional selectable choices when a step distinguishes
	// choices from options.
	Choices []DescriptorOption `json:"choices,omitempty"`
	// Required steps must stay in every ladder (connection, verified).
	Required bool `json:"required,omitempty"`
	// CanMove is false for steps the ladder renders as Automatic: the owner
	// cannot reorder them.
	CanMove bool `json:"canMove"`
	// ConfigKey names the QualityProfile field the editor edits for
	// built-in steps (preferredLanguages, preferredSources,
	// preferredCodecs, preferredAudio, preferLargest, heightRange, hdr,
	// customFormatScores).
	// Plugin steps leave it empty; their settings arrive with WS9h.
	ConfigKey string `json:"configKey,omitempty"`
}

// Rule value classes: how a condition value is edited and validated.
const (
	RuleValueText     = "text"     // free text with equals/contains/regex
	RuleValueChoice   = "choice"   // free text with suggested choices
	RuleValueLanguage = "language" // exact declared-language match
	RuleValueNumber   = "number"   // a min/max range
	RuleValueNone     = "none"     // takes no value at all
)

// RuleFieldMeta is the Meta of a KindRuleField descriptor.
type RuleFieldMeta struct {
	// ValueClass is one of the RuleValue* constants.
	ValueClass string `json:"valueClass"`
	// Operators are the comparisons the field accepts, in display order
	// (equals, contains, regex, range).
	Operators []string `json:"operators,omitempty"`
	// DefaultOperator is preselected when a condition is created or
	// retargeted to this field.
	DefaultOperator string `json:"defaultOperator,omitempty"`
	// Choices are suggested values for choice and language fields. Choice
	// fields still accept free text; language choices are ISO codes the
	// console labels through Intl.DisplayNames.
	Choices []DescriptorOption `json:"choices,omitempty"`
	// Unit is the value's unit for number fields ("GiB", "p").
	Unit string `json:"unit,omitempty"`
	// SingleValue fields can hold one value per release, so two required
	// rules demanding different values conflict.
	SingleValue bool `json:"singleValue,omitempty"`
	// Editor carries a plugin manifest's raw ruleConditions[].editor
	// (SPEC §5.1.3) when ValueClass was not set directly.
	Editor json.RawMessage `json:"editor,omitempty"`
}

// Resolved folds a plugin manifest Editor declaration into ValueClass,
// Choices, Unit and SingleValue when those fields were not set directly.
func (m RuleFieldMeta) Resolved() RuleFieldMeta {
	if trimmed := bytes.TrimSpace(m.Editor); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			if m.ValueClass == "" && s != "" {
				m.ValueClass = normalizeRuleValueClass(s)
			}
		} else {
			var obj struct {
				Kind        string             `json:"kind"`
				ValueClass  string             `json:"valueClass"`
				Choices     []DescriptorOption `json:"choices"`
				Options     []DescriptorOption `json:"options"`
				Unit        string             `json:"unit"`
				SingleValue bool               `json:"singleValue"`
			}
			if err := json.Unmarshal(trimmed, &obj); err == nil {
				if m.ValueClass == "" {
					switch {
					case obj.ValueClass != "":
						m.ValueClass = normalizeRuleValueClass(obj.ValueClass)
					case obj.Kind != "":
						m.ValueClass = normalizeRuleValueClass(obj.Kind)
					}
				}
				if len(m.Choices) == 0 {
					if len(obj.Choices) > 0 {
						m.Choices = obj.Choices
					} else if len(obj.Options) > 0 {
						m.Choices = obj.Options
					}
				}
				if m.Unit == "" && obj.Unit != "" {
					m.Unit = obj.Unit
				}
				if !m.SingleValue && obj.SingleValue {
					m.SingleValue = true
				}
			}
		}
	}
	if m.ValueClass == "" && len(m.Operators) == 1 && m.Operators[0] == "range" {
		m.ValueClass = RuleValueNumber
	}
	if m.DefaultOperator == "" && len(m.Operators) > 0 {
		m.DefaultOperator = m.Operators[0]
	}
	return m
}

func normalizeRuleValueClass(v string) string {
	if v == "range" {
		return RuleValueNumber
	}
	return v
}
