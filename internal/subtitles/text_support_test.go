package subtitles

import ()

// Normalize converts raw downloaded subtitle bytes to UTF-8 SRT and WebVTT,
// guessing a legacy encoding as Windows-1252.
func Normalize(raw []byte) (srt, vtt string, err error) { return NormalizeLang(raw, "") }
