package resolver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
)

// ValidateStepConfig validates the bounded value object described by a plugin editor.
func ValidateStepConfig(meta, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > 8192 {
		return fmt.Errorf("configuration is too large")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return fmt.Errorf("configuration must be an object")
	}
	if len(obj) == 0 {
		return nil
	}
	for _, v := range obj {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("configuration values cannot be null")
		}
	}
	var desc struct {
		Editor  json.RawMessage   `json:"editor"`
		Options []json.RawMessage `json:"options"`
		Choices []json.RawMessage `json:"choices"`
	}
	if err := json.Unmarshal(meta, &desc); err != nil {
		return fmt.Errorf("invalid editor")
	}
	var kind string
	var ed struct {
		Kind    string            `json:"kind"`
		Editor  string            `json:"editor"`
		Options []json.RawMessage `json:"options"`
		Choices []json.RawMessage `json:"choices"`
	}
	if json.Unmarshal(desc.Editor, &kind) != nil {
		if json.Unmarshal(desc.Editor, &ed) != nil {
			return fmt.Errorf("invalid editor")
		}
		kind = ed.Kind
		if kind == "" {
			kind = ed.Editor
		}
	}
	options := ed.Options
	if len(options) == 0 {
		options = ed.Choices
	}
	if len(options) == 0 {
		options = desc.Options
	}
	if len(options) == 0 {
		options = desc.Choices
	}
	allowed := map[string]bool{}
	for _, o := range options {
		var str string
		if json.Unmarshal(o, &str) != nil {
			var v struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(o, &v) != nil {
				return fmt.Errorf("invalid editor option")
			}
			str = v.Value
		}
		allowed[str] = true
	}
	switch kind {
	case "toggle":
		var value bool
		if len(obj) != 1 || json.Unmarshal(obj["value"], &value) != nil {
			return fmt.Errorf("choose on or off")
		}
	case "choice":
		var value string
		if len(obj) != 1 || json.Unmarshal(obj["value"], &value) != nil || !allowed[value] {
			return fmt.Errorf("choose a listed value")
		}
	case "ordered_list":
		var value []string
		if len(obj) != 1 || json.Unmarshal(obj["value"], &value) != nil || value == nil {
			return fmt.Errorf("choose an ordered list")
		}
		seen := map[string]bool{}
		for _, v := range value {
			if !allowed[v] || seen[v] {
				return fmt.Errorf("choose unique listed values")
			}
			seen[v] = true
		}
	case "range":
		var min, max float64
		if len(obj) != 2 || json.Unmarshal(obj["min"], &min) != nil || json.Unmarshal(obj["max"], &max) != nil || min > max {
			return fmt.Errorf("choose a valid range")
		}
	default:
		return fmt.Errorf("this step has no supported editor")
	}
	return nil
}

// ValidateRankingConfig checks a profile's plugin score steps against the
// installed plugin descriptors; absent plugins
// retain their saved settings so uninstalling one does not make profiles unsaveable.
func (s *Service) ValidateRankingConfig(p core.QualityProfile) map[string]string {
	errs := map[string]string{}
	r := s.extendRegistry()
	if r == nil {
		return errs
	}
	for id, raw := range p.PluginScores {
		if !strings.HasPrefix(id, "plugin:") {
			errs[id] = "configuration requires a plugin step"
			continue
		}
		d, ok := r.Get(extend.KindRankingStep, id)
		if !ok {
			continue
		}
		if err := ValidateStepConfig(d.Meta, bytes.TrimSpace(raw)); err != nil {
			errs[id] = err.Error()
		}
	}
	return errs
}
