package onair

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/llm"
	"strings"
)

// Chatter is the owner-configured model; drafts never save records.
type Chatter interface {
	Enabled() bool
	Chat(context.Context, []llm.Message, bool) (string, error)
}

func (s *Service) Draft(ctx context.Context, l Look) (Look, error) {
	if l.Moments == nil {
		l.Moments = map[string]Moment{}
		for _, m := range strings.Fields("ident upnext tonight break back offair welcome bug lowerthird") {
			l.Moments[m] = Moment{8, 4}
		}
	}
	if l.ChannelIDs == nil {
		l.ChannelIDs = []string{}
	}
	l.Pack = nil
	l.Render = nil
	return l, s.validateLook(ctx, l)
}

const lookGuide = `Return one complete look JSON object, no prose. Preserve the base id, channelIds, moments and layers unless asked to change them. Fields: id,name,family (clean,kids,lateNight,cinema,retro,personal), brand:{palette:{background,accent,text,glow} (#RRGGBB, text on background contrast >=4.5:1),type:{display,body},energy (0..1),textures (Grain,Flicker,Scanlines,Vignette),sting (empty or existing asset digest),volume (0..1),seed (uint32),tagline (at most 40 characters),words (map moment to on-air words),language (ISO code),logo (optional existing digest)}, moments:{ident,...:{duration (4,8,12,16,20,24 or28 seconds),poster (less than duration)}},channelIds:[] and optional layers. Fonts: Inter, Playfair Display, Special Elite, Fraunces, Bungee, Fredoka, Orbitron, VT323, Space Grotesk, IBM Plex Mono, Archivo Black, Monoton, Noto Sans. Never invent asset digests or programme titles. This is a draft: the owner saves it.`

func (s *Service) Generate(ctx context.Context, prompt, summary string, base Look) (Look, error) {
	if s.o.Chat == nil || !s.o.Chat.Enabled() {
		return Look{}, fmt.Errorf("connect a language model in Integrations to describe a look")
	}
	if len(prompt) == 0 || len(prompt) > 2000 {
		return Look{}, fmt.Errorf("prompt must be between 1 and 2000 characters")
	}
	if base.Family == "" {
		base = familyLook("clean", "New look")
		base.ID = ""
		base.Name = "New look"
	}
	b, _ := json.Marshal(base)
	msgs := []llm.Message{{Role: "system", Content: lookGuide}, {Role: "user", Content: fmt.Sprintf("Request: %s\nLocal channel summary: %s\nBase look: %s", prompt, summary, b)}}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		raw, e := s.o.Chat.Chat(ctx, msgs, true)
		if e != nil {
			return Look{}, e
		}
		var l Look
		err = json.Unmarshal([]byte(strings.TrimSpace(raw)), &l)
		if err == nil {
			l.ID = base.ID
			l.ChannelIDs = base.ChannelIDs
			l, err = s.Draft(ctx, l)
			if err == nil {
				return l, nil
			}
		}
		msgs = append(msgs, llm.Message{Role: "assistant", Content: raw}, llm.Message{Role: "user", Content: "Correct the draft: " + err.Error()})
	}
	return Look{}, fmt.Errorf("model returned an invalid look: %w", err)
}

// Template is an explicit, deterministic alternative to a connected model.
func (s *Service) Template(ctx context.Context, prompt string, base Look) (Look, error) {
	if len(strings.TrimSpace(prompt)) == 0 || len(prompt) > 2000 {
		return Look{}, fmt.Errorf("describe the style in 1 to 2000 characters")
	}
	words := strings.ToLower(prompt)
	family := "clean"
	for _, rule := range []struct {
		family   string
		keywords []string
	}{
		{"lateNight", []string{"night", "cosy", "cozy", "horror"}}, {"cinema", []string{"cinema", "movie", "gold"}}, {"retro", []string{"neon", "retro", "80", "vhs", "synth"}}, {"kids", []string{"kids", "cartoon", "playful"}}, {"personal", []string{"personal", "aurora"}},
	} {
		for _, k := range rule.keywords {
			if strings.Contains(words, k) {
				family = rule.family
				break
			}
		}
		if family != "clean" {
			break
		}
	}
	name := []rune(strings.TrimSpace(prompt))
	if len(name) > 80 {
		name = name[:80]
	}
	l := familyLook(family, string(name))
	l.ID = base.ID
	l.ChannelIDs = base.ChannelIDs
	if strings.Contains(words, "calm") || strings.Contains(words, "cosy") || strings.Contains(words, "cozy") {
		l.Brand.Energy = .2
	}
	return s.Draft(ctx, l)
}
