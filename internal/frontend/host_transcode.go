package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

const (
	ContainerMPEGTS   = transcode.ContainerMPEGTS
	ContainerFMP4     = transcode.ContainerFMP4
	ContainerMatroska = transcode.ContainerMatroska

	InitSegment        = transcode.InitSegment
	InitSegmentName    = transcode.InitSegmentName
	MatroskaHeaderName = transcode.MatroskaHeaderName
)

var ErrTooManyTranscodes = transcode.ErrTooManyTranscodes

type TranscodePolicy = transcode.Policy
type ClientPolicy = transcode.ClientPolicy
type PlaybackDefaults = transcode.PlaybackDefaults
type BIF = transcode.BIF
type BIFFrame = transcode.BIFFrame

func ParseBIF(data []byte) (*BIF, error) { return transcode.ParseBIF(data) }

func MarshalBIF(frames []BIFFrame) ([]byte, error) { return transcode.MarshalBIF(frames) }

func ParseSegmentName(name string) (int, bool) { return transcode.ParseSegmentName(name) }

func IsImageSubtitle(codec string) bool { return transcode.IsImageSubtitle(codec) }

func IsTextSubtitle(codec string) bool { return transcode.IsTextSubtitle(codec) }

func WithSubtitleSource(ctx context.Context, source string) context.Context {
	return transcode.WithSubtitleSource(ctx, source)
}

func HasQuirk(product, platform, device, model, version, name string) bool {
	return transcode.HasQuirk(product, platform, device, model, version, name)
}

func PolicyStamp(product, platform, device, model, version string, overrides []core.ClientCapabilityOverride) (dv []int, subs []string, dvChanged, subsChanged bool) {
	return transcode.PolicyStamp(product, platform, device, model, version, overrides)
}

func PlaybackDefaultsFor(product, platform, device, model, version string, overrides []core.ClientCapabilityOverride) PlaybackDefaults {
	return transcode.PlaybackDefaultsFor(product, platform, device, model, version, overrides)
}

func ClientPolicyFor(c ClientCaps) ClientPolicy { return transcode.ClientPolicyFor(c) }

func SupportsDVProfile(c ClientCaps, profile int) bool {
	return transcode.SupportsDVProfile(c, profile)
}

func HasHDR10Base(v *StreamInfo, caps ClientCaps) bool {
	return transcode.HasHDR10Base(v, caps)
}

func BurnsSubtitle(sub StreamInfo, caps ClientCaps, opts DecideOptions) bool {
	return transcode.BurnsSubtitle(sub, caps, opts)
}

func DecideTranscode(info MediaInfo, caps ClientCaps, opts DecideOptions, pol TranscodePolicy) Decision {
	return transcode.Decide(info, caps, opts, pol)
}

func HLSContainer(caps ClientCaps, videoCodec, audioCodec string) string {
	return transcode.HLSContainer(caps, videoCodec, audioCodec)
}

func SegmentedVideoCopyOK(caps ClientCaps, codec string) bool {
	return transcode.SegmentedVideoCopyOK(caps, codec)
}

func SegmentedAudioCopyOK(caps ClientCaps, codec string) bool {
	return transcode.SegmentedAudioCopyOK(caps, codec)
}

func HLSCopyPeakKbps(info MediaInfo, segDur time.Duration) int {
	return transcode.HLSCopyPeakKbps(info, segDur)
}

func HLSVariantAttributes(info MediaInfo, d Decision) string {
	return transcode.HLSVariantAttributes(info, d)
}
