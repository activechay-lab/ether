package core

// CopyTimeline is a source-owner packet-scan fact, bound to the immutable source
// revision by cluster source authorization. Version 1 admits only a complete,
// regular no-reorder H264/AAC TS title, with IDR + SPS/PPS at every cut.
// It is not a caller assertion: workers authenticate it against the source owner.
// All clocks use 90kHz ticks. AAC access units have 1024 samples at 48kHz.
type CopyTimeline struct {
	Version                  int
	Origin, Step, End        int64
	VideoStream, AudioStream int
	VideoAUs, AudioAUs       int64
	// Ordered packet payloads AND stream/PTS/DTS are committed by this digest.
	PacketSHA256 [32]byte
}
