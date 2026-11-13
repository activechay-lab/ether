package caps

import (
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// deriveVideo evaluates the advertised direct-play alternatives before release
// selection. Detailed codec predicates remain attached for playback planning.
func deriveVideo(c *core.ClientCaps, p DeviceProfile) {
	declared := false
	profileDV := false
	profileDVEnhancement := false
	height := 0
	for _, dp := range p.DirectPlayProfiles {
		if !strings.EqualFold(dp.Type, "video") && dp.Type != "" {
			continue
		}
		for _, codec := range add(nil, dp.VideoCodec) {
			declared = true
			limit := 0
			dvDeclared := false
			ranges := map[string]bool{"SDR": true, "HDR10": true, "HDR10PLUS": true, "HLG": true, "DOVI": true, "DOVIWITHHDR10": true, "DOVIWITHHLG": true, "DOVIWITHSDR": true, "DOVIWITHEL": true, "DOVIWITHELHDR10PLUS": true}
			for _, cp := range p.CodecProfiles {
				if !strings.EqualFold(cp.Type, "video") || len(cp.ApplyConditions) > 0 {
					continue
				}
				if cp.Container != "" {
					containerMatch := false
					for _, container := range add(nil, cp.Container) {
						for _, direct := range add(nil, dp.Container) {
							if container == direct {
								containerMatch = true
							}
						}
					}
					if !containerMatch {
						continue
					}
				}
				matches := cp.Codec == ""
				for _, name := range add(nil, cp.Codec) {
					if name == codec || (name == "h265" && codec == "hevc") {
						matches = true
					}
				}
				if !matches {
					continue
				}
				for _, cond := range cp.Conditions {
					prop, op := strings.ToLower(cond.Property), strings.ToLower(cond.Condition)
					if (prop == "height" || prop == "videoheight" || prop == "width" || prop == "videowidth") && (op == "lessthanequal" || op == "equals") {
						n, _ := strconv.Atoi(cond.Value)
						if prop == "width" || prop == "videowidth" {
							n = n * 9 / 16
						}
						if n > 0 && (limit == 0 || n < limit) {
							limit = n
						}
					}
					if prop == "videoprofile" && (op == "equals" || op == "equalsany") && (codec == "hevc" || codec == "h265") && !strings.Contains(strings.ToLower(cond.Value), "main 10") && !strings.Contains(strings.ToLower(cond.Value), "main10") {
						for r := range ranges {
							if r != "SDR" {
								ranges[r] = false
							}
						}
					}
					if prop == "videobitdepth" && (op == "lessthanequal" || op == "equals") {
						if depth, err := strconv.Atoi(cond.Value); err == nil && depth < 10 {
							for r := range ranges {
								if r != "SDR" {
									ranges[r] = false
								}
							}
						}
					}
					if prop != "videorangetype" && prop != "videorange" {
						continue
					}
					if (op == "equals" || op == "equalsany") && strings.Contains(strings.ToUpper(cond.Value), "DOVI") {
						dvDeclared = true
					}
					for r := range ranges {
						match := false
						for _, v := range strings.FieldsFunc(strings.ToUpper(cond.Value), func(r rune) bool { return r == '|' || r == ',' }) {
							if strings.TrimSpace(v) == r || (prop == "videorange" && strings.TrimSpace(v) == "HDR" && r != "SDR") {
								match = true
							}
						}
						switch op {
						case "equals", "equalsany":
							ranges[r] = ranges[r] && match
						case "notequals":
							ranges[r] = ranges[r] && !match
						}
					}
				}
			}
			// An unrestricted alternative makes the aggregate height unrestricted.
			if limit == 0 {
				height = -1
			} else if height >= 0 && limit > height {
				height = limit
			}
			if codec == "hevc" || codec == "h265" || codec == "av1" || codec == "vp9" {
				c.HDR10 = c.HDR10 || ranges["HDR10"] || ranges["HDR10PLUS"] || ranges["HLG"] || (dvDeclared && (ranges["DOVIWITHHDR10"] || ranges["DOVIWITHHLG"]))
				if codec == "hevc" || codec == "h265" {
					profileDV = profileDV || (dvDeclared && (ranges["DOVI"] || ranges["DOVIWITHHDR10"] || ranges["DOVIWITHHLG"] || ranges["DOVIWITHSDR"] || ranges["DOVIWITHEL"] || ranges["DOVIWITHELHDR10PLUS"]))
					profileDVEnhancement = profileDVEnhancement || (dvDeclared && (ranges["DOVIWITHEL"] || ranges["DOVIWITHELHDR10PLUS"]))
				}
			}
		}
	}
	c.DolbyVision = c.DolbyVision || profileDV
	if profileDV && len(c.DVProfiles) == 0 {
		// Single-layer Dolby Vision evidence does not advertise Blu-ray's
		// enhancement layer. An HDR10-capable client can instead copy its
		// profile-7 base layer with the Dolby Vision signalling stripped.
		c.DVProfiles = []int{5, 8}
		if profileDVEnhancement {
			c.DVProfiles = append(c.DVProfiles, 7)
		}
	}
	if declared {
		c.ExplicitMaxHeight = true
		if height > 0 && (c.MaxHeight == 0 || height < c.MaxHeight) {
			c.MaxHeight = height
		}
	}
}
