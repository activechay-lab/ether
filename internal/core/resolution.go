package core

// ResolutionTier returns the nominal vertical tier, including cropped video.
// Zero means unknown and 240 means a known sub-480 SD picture.
func ResolutionTier(w, h int) int {
	switch {
	case w >= 3200 || h >= 1800:
		return 2160
	case w >= 1700 || h >= 1000:
		return 1080
	case w >= 1200 || h >= 700:
		return 720
	case w >= 960 || h >= 560:
		return 576
	case w >= 640 || h >= 400:
		return 480
	case w > 0 || h > 0:
		return 240
	default:
		return 0
	}
}

// ResolutionLabel is the presentation label for nominal dimensions.
func ResolutionLabel(w, h int) string {
	switch ResolutionTier(w, h) {
	case 2160:
		return "4K"
	case 1080:
		return "1080p"
	case 720:
		return "720p"
	case 576:
		return "576p"
	case 480:
		return "480p"
	default:
		return "SD"
	}
}
