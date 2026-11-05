package transcode

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Interlaced input must become progressive before resizing on every encoder
// path, while clients that can decode it retain direct play.
func TestInterlacedPictureContract(t *testing.T) {
	info := mustParse(t, "mp4_h264_aac.json")
	info.Streams[0].Interlaced = true
	for _, platform := range []string{"Chrome", "Microsoft Edge", "Opera", "Safari"} {
		caps := plexWeb
		caps.Platform = platform
		caps.Containers = []string{"mp4"}
		caps.VideoCodecs = []string{"h264"}
		caps.AudioCodecs = []string{"aac"}
		d := Decide(info, caps, allow(), Policy{})
		if platform == "Safari" {
			if !d.VideoCopy {
				t.Fatal("capable client lost video copy")
			}
			continue
		}
		if d.VideoCopy {
			t.Fatalf("%s copied interlaced H.264", platform)
		}
		for _, accel := range []string{AccelNone, AccelNVENC, AccelVAAPI, AccelVideoToolbox, AccelQSV} {
			s := baseSpec(info, d)
			s.Accel = core.HWAccel{Name: accel, Encoders: []string{"h264_" + accel}}
			s.Features.hwaccels = map[string]bool{"cuda": true, "videotoolbox": true, "qsv": true}
			for _, f := range []string{"scale_cuda", "scale_vt", "vpp_qsv"} {
				s.Features.filters[f] = true
			}
			args := buildArgs(s)
			mustContain(t, args, "yadif=mode=send_frame:parity=auto:deint=all")
			mustNotContain(t, args, "-hwaccel_output_format", "scale_vaapi=", "scale_cuda=", "scale_vt=", "vpp_qsv=")
		}
	}
}

func TestGPUFramePipeline(t *testing.T) {
	for _, tc := range []struct {
		name, accel, filter, frameFormat string
	}{
		{"nvidia", AccelNVENC, "scale_cuda", "cuda"},
		{"apple", AccelVideoToolbox, "scale_vt", "videotoolbox_vld"},
		{"intel", AccelQSV, "vpp_qsv", "qsv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := mustParse(t, "mp4_h264_aac.json")
			opts := allow()
			opts.MaxHeight = 720
			s := baseSpec(info, Decide(info, appleTV, opts, Policy{}))
			s.Accel = core.HWAccel{Name: tc.accel, Encoders: []string{"h264_" + tc.accel, "hevc_" + tc.accel}}
			s.Features.hwaccels = map[string]bool{"cuda": true, "qsv": true, "videotoolbox": true}
			s.Features.filters[tc.filter] = true
			args := buildArgs(s)
			mustContain(t, args, "-hwaccel_output_format "+tc.frameFormat, tc.filter+"=w=1280:h=720:format=nv12")
			mustNotContain(t, args, "hwdownload", "hwupload", "scale=", ",format=yuv420p")

			// A custom/older FFmpeg missing the device scaler must keep a
			// usable software frame graph instead of failing at startup.
			delete(s.Features.filters, tc.filter)
			mustNotContain(t, buildArgs(s), "-hwaccel_output_format", tc.filter+"=")
		})
	}
}

func TestGPUHDRPipelineAndFallback(t *testing.T) {
	for _, accel := range []string{AccelNVENC, AccelVideoToolbox} {
		t.Run(accel, func(t *testing.T) {
			info := mustParse(t, "mkv_hevc_hdr10.json")
			s := baseSpec(info, Decide(info, plexWeb, allow(), Policy{}))
			s.Accel = core.HWAccel{Name: accel, Encoders: []string{"h264_" + accel, "hevc_" + accel}}
			s.Features.hwaccels = map[string]bool{"cuda": true, "videotoolbox": true}
			s.Features.filters = map[string]bool{"scale_cuda": true, "scale_vt": true, "tonemap_cuda": true, "tonemap_videotoolbox": true, "zscale": true, "tonemap": true}
			args := buildArgs(s)
			filter := "tonemap_cuda"
			if accel == AccelVideoToolbox {
				filter = "tonemap_videotoolbox"
			}
			mustContain(t, args, filter+"=tonemap=hable:format=nv12:t=bt709:m=bt709:p=bt709")
			mustNotContain(t, args, "zscale=", "hwdownload")

			// HDR passthrough preserves ten-bit pictures and does not tone
			// map just because a hardware filter is available.
			s.Decision.VideoCodec = "hevc"
			s.Decision.ToneMap = false
			args = buildArgs(s)
			mustContain(t, args, "format=p010le", "-profile:v main10")
			mustNotContain(t, args, "tonemap_")
		})
	}
}

func TestGPUPipelineDoesNotDiscardSubtitles(t *testing.T) {
	info := mustParse(t, "mkv_pgs.json")
	opts := allow()
	opts.SubtitleStream = 2
	s := baseSpec(info, Decide(info, appleTV, opts, Policy{}))
	s.Accel = core.HWAccel{Name: AccelNVENC, Encoders: []string{"h264_nvenc", "hevc_nvenc"}}
	s.Features.hwaccels = map[string]bool{"cuda": true}
	s.Features.filters["scale_cuda"] = true
	args := buildArgs(s)
	if !strings.Contains(argValue(args, "-filter_complex"), "overlay") {
		t.Fatal("hardware pipeline dropped bitmap subtitles")
	}
	mustNotContain(t, args, "-hwaccel_output_format cuda", "scale_cuda=")
}

func TestVideoToolboxScalerOptionCompatibility(t *testing.T) {
	info := mustParse(t, "mp4_h264_aac.json")
	opts := allow()
	opts.MaxHeight = 720
	s := baseSpec(info, Decide(info, appleTV, opts, Policy{}))
	s.Accel = core.HWAccel{Name: AccelVideoToolbox, Encoders: []string{"h264_videotoolbox"}}
	s.Features.scaleVTFormat = false
	args := buildArgs(s)
	mustContain(t, args, "scale_vt=w=1280:h=720[vout]", "-allow_sw 0")
	mustNotContain(t, args, "scale_vt=w=1280:h=720:format=", "hwdownload")
	// A filter name alone does not prove support for the format option.
	for _, tc := range []struct {
		help string
		want bool
	}{
		{"format <pix_fmt> Output format", true},
		{"w <string> Output width\nh <string> Output height", false},
		{"Unknown filter 'scale_vt'\nformat in explanatory prose", false},
	} {
		if got := filterHasOption([]byte(tc.help), "format"); got != tc.want {
			t.Errorf("filterHasOption(%q) = %v, want %v", tc.help, got, tc.want)
		}
	}
}

func TestHigh10KeepsNVENCWithoutCUDADecoding(t *testing.T) {
	info := mustParse(t, "mp4_h264_aac.json")
	for i := range info.Streams {
		if info.Streams[i].Type == "video" {
			info.Streams[i].BitDepth = 10
		}
	}
	opts := allow()
	opts.MaxHeight = 720
	s := baseSpec(info, Decide(info, appleTV, opts, Policy{}))
	s.Accel = core.HWAccel{Name: AccelNVENC, Encoders: []string{"h264_nvenc"}}
	s.Features.hwaccels = map[string]bool{"cuda": true}
	s.Features.filters["scale_cuda"] = true
	args := buildArgs(s)
	mustContain(t, args, "-c:v h264_nvenc", "scale=1280:720")
	mustNotContain(t, args, "-hwaccel cuda", "-hwaccel_output_format cuda", "scale_cuda=")
}

func TestCUDAP010ToneMapContractAndFallback(t *testing.T) {
	orig, _ := yellowstone()
	orig.Video().Width, orig.Video().Height = 3840, 1920
	orig.Video().ColorPrimaries, orig.Video().ColorSpace, orig.Video().ColorTransfer = "bt709", "bt709", "bt709"
	contract, ok := contractOf(orig, copyMKV())
	if !ok {
		t.Fatal("missing contract")
	}
	hdr := mustParse(t, "mkv_hevc_hdr10.json")
	hdr.Video().Width, hdr.Video().Height = 3840, 1920
	d := adaptToContract(copyMKV(), orig, hdr, ContainerMatroska, contract)
	s := contractSpec(t, hdr, d, contract)
	s.Accel = core.HWAccel{Name: AccelNVENC, Encoders: []string{"hevc_nvenc"}}
	s.Features.hwaccels = map[string]bool{"cuda": true}
	s.Features.filters["tonemap_cuda"] = true
	s.Features.filters["scale_cuda"] = true
	s.Features.cudaP010ToneMap = true
	args := buildArgs(s)
	mustContain(t, args, "-c:v hevc_nvenc", "-profile:v main10", "tonemap_cuda=tonemap=hable:format=p010le:t=bt709:m=bt709:p=bt709", "scale_cuda=w=3840:h=1920:format=p010le")
	mustNotContain(t, args, "hwdownload", "zscale=", "pad=")
	s.Features.cudaP010ToneMap = false
	args = buildArgs(s)
	mustNotContain(t, args, "tonemap_cuda=", "scale_cuda=")
	mustContain(t, args, "-c:v hevc_nvenc", "format=p010le", "zscale=")
	s.Features.cudaP010ToneMap = true
	hdr.Video().ColorRange = "pc"
	s.Info = hdr
	mustNotContain(t, buildArgs(s), "tonemap_cuda=")
	hdr.Video().ColorRange = "tv"
	s.Info = hdr
	s.Target = &videoContract{codec: "hevc", depth: 10, rng: rangeSDR, width: 3840, height: 1920, pic: picture{sarNum: 1, sarDen: 1, prim: "bt709", matrix: "bt709", full: true}}
	mustNotContain(t, buildArgs(s), "tonemap_cuda=")
}
