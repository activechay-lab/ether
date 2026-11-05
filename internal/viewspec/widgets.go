package viewspec

// The closed Ether widget and frame registry (SPEC §5.6). A plugin document
// may only name these; anything else is a rejection. The console registry in
// web/src/viewspec/registry.ts mirrors this list and its prop shapes.
var (
	EtherWidgets = []string{
		"ether.posterGrid",
		"ether.posterShelf",
		"ether.titleCard",
		"ether.liveChart",
		"ether.settingsForm",
		"ether.actionButton",
		"ether.titleMatch",
		"ether.healthStrip",
		"ether.channelRow",
		"ether.badge",
	}
	EtherFrames = []string{
		"ether.section",
		"ether.tabs",
		"ether.drawer",
	}
)

func inSet(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// IsEtherWidget reports whether name is a registered Ether widget.
func IsEtherWidget(name string) bool { return inSet(EtherWidgets, name) }

// IsEtherFrame reports whether name is a registered Ether frame.
func IsEtherFrame(name string) bool { return inSet(EtherFrames, name) }

// propShape describes one widget's props: names that must be present (unless
// bound through a field reference) and names that may be present. A prop
// whose value is a field reference cannot be type-checked against the spec
// alone, so shape checks cover presence, unknown names, and literal types.
type propShape struct {
	required []string
	optional []string
	// oneOf groups: at least one name from the group must be present.
	oneOf [][]string
	// literals maps a prop name to the JSON kinds a literal value may have:
	// "string", "number", "bool", "array", "object".
	literals map[string][]string
}

var widgetShapes = map[string]propShape{
	"ether.posterGrid": {
		required: []string{"items"},
		optional: []string{"empty"},
		literals: map[string][]string{"items": {"array"}, "empty": {"string"}},
	},
	"ether.posterShelf": {
		required: []string{"items"},
		optional: []string{"label", "empty"},
		literals: map[string][]string{"items": {"array"}, "label": {"string"}, "empty": {"string"}},
	},
	"ether.titleCard": {
		oneOf:    [][]string{{"item", "ratingKey", "tmdbId"}},
		optional: []string{"item", "ratingKey", "tmdbId", "mediaType", "reason"},
		literals: map[string][]string{
			"item": {"object"}, "ratingKey": {"string", "number"},
			"tmdbId": {"number"}, "mediaType": {"string"}, "reason": {"string"},
		},
	},
	"ether.liveChart": {
		required: []string{"series"},
		optional: []string{"times", "max", "unit", "label", "area"},
		literals: map[string][]string{
			"series": {"array"}, "times": {"array"}, "max": {"number"},
			"unit": {"string"}, "label": {"string"}, "area": {"bool"},
		},
	},
	"ether.settingsForm": {
		required: []string{"groups", "values", "action"},
		optional: []string{"submitLabel"},
		literals: map[string][]string{
			"groups": {"array"}, "values": {"object"},
			"action": {"string"}, "submitLabel": {"string"},
		},
	},
	"ether.actionButton": {
		required: []string{"label", "action"},
		optional: []string{"params", "variant"},
		literals: map[string][]string{
			"label": {"string"}, "action": {"string"},
			"params": {"object"}, "variant": {"string"},
		},
	},
	"ether.titleMatch": {
		required: []string{"action"},
		optional: []string{"label", "placeholder", "params", "mediaType"},
		literals: map[string][]string{
			"action": {"string"}, "label": {"string"}, "placeholder": {"string"},
			"params": {"object"}, "mediaType": {"string"},
		},
	},
	"ether.healthStrip": {
		required: []string{"items"},
		literals: map[string][]string{"items": {"array"}},
	},
	"ether.channelRow": {
		required: []string{"name"},
		optional: []string{"number", "group", "logo", "nowPlaying", "level"},
		literals: map[string][]string{
			"name": {"string"}, "number": {"string", "number"}, "group": {"string"},
			"logo": {"string"}, "nowPlaying": {"string"}, "level": {"string"},
		},
	},
	"ether.badge": {
		required: []string{"label"},
		optional: []string{"variant"},
		literals: map[string][]string{"label": {"string"}, "variant": {"string"}},
	},
}

var frameShapes = map[string]propShape{
	"ether.section": {
		optional: []string{"title", "description"},
		literals: map[string][]string{"title": {"string"}, "description": {"string"}},
	},
	"ether.tabs": {
		required: []string{"tabs"},
		optional: []string{"param"},
		literals: map[string][]string{"tabs": {"array"}, "param": {"string"}},
	},
	"ether.drawer": {
		required: []string{"title"},
		optional: []string{"openLabel"},
		literals: map[string][]string{"title": {"string"}, "openLabel": {"string"}},
	},
}
