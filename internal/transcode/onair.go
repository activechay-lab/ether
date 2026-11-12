package transcode

// OnAirEncoderOptions shares the confirmed encoder and rate-control builder with
// playback. Inputs are software RGBA frames; VAAPI requires an explicit upload.
func (m *Manager) OnAirEncoderOptions(w, h int) (input, video []string) {
	enc, hw := videoEncoder("h264", m.HWAccel(), m.feat)
	if !hw {
		return nil, nil
	}
	return m.onAirOptions(enc, w, h)
}
func (m *Manager) onAirOptions(enc string, w, h int) ([]string, []string) {
	s := runSpec{Accel: m.HWAccel(), Features: m.feat}
	s.Decision.VideoCodec = "h264"
	s.Rate.TargetKbps = 4000
	args := planEncodeArgs(s, picturePlan{w: w, h: h}, enc, "h264", enc == "h264_vaapi")
	var input []string
	if enc == "h264_vaapi" {
		input = []string{"-vaapi_device", m.hw.Device}
		args = append(args, "-vf", "format=nv12,hwupload")
	}
	return input, args
}
