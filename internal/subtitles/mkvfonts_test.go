package subtitles

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func buildTestSFNTFont(tag string, records []struct {
	platform uint16
	nameID   uint16
	value    string
}) []byte {
	if len(tag) != 4 {
		tag = "\x00\x01\x00\x00"
	}
	var stringStorage []byte
	nameHeader := make([]byte, 6+len(records)*12)
	binary.BigEndian.PutUint16(nameHeader[0:2], 0)
	binary.BigEndian.PutUint16(nameHeader[2:4], uint16(len(records)))
	binary.BigEndian.PutUint16(nameHeader[4:6], uint16(len(nameHeader)))

	for i, r := range records {
		var encoded []byte
		if r.platform == 3 || r.platform == 0 {
			u16 := utf16.Encode([]rune(r.value))
			encoded = make([]byte, len(u16)*2)
			for j, ch := range u16 {
				binary.BigEndian.PutUint16(encoded[j*2:], ch)
			}
		} else {
			encoded = []byte(r.value)
		}
		off := len(stringStorage)
		stringStorage = append(stringStorage, encoded...)
		rec := nameHeader[6+i*12 : 6+(i+1)*12]
		binary.BigEndian.PutUint16(rec[0:2], r.platform)
		binary.BigEndian.PutUint16(rec[2:4], 1)
		binary.BigEndian.PutUint16(rec[4:6], 0x0409)
		binary.BigEndian.PutUint16(rec[6:8], r.nameID)
		binary.BigEndian.PutUint16(rec[8:10], uint16(len(encoded)))
		binary.BigEndian.PutUint16(rec[10:12], uint16(off))
	}
	nameTable := append(nameHeader, stringStorage...)
	sfntHeader := make([]byte, 12+16)
	copy(sfntHeader[0:4], []byte(tag))
	binary.BigEndian.PutUint16(sfntHeader[4:6], 1) // 1 table ("name")
	copy(sfntHeader[12:16], []byte("name"))
	binary.BigEndian.PutUint32(sfntHeader[20:24], uint32(len(sfntHeader)))
	binary.BigEndian.PutUint32(sfntHeader[24:28], uint32(len(nameTable)))
	return append(sfntHeader, nameTable...)
}

func buildMKVAttachedFile(fileName, mimeType string, data []byte) []byte {
	var body []byte
	if fileName != "" {
		body = append(body, mkvS(mkvFileNameID, fileName)...)
	}
	if mimeType != "" {
		body = append(body, mkvS(mkvFileMimeTypeID, mimeType)...)
	}
	body = append(body, mkvEl(mkvFileDataID, data)...)
	return mkvEl(mkvAttachedFileID, body)
}

func buildTestMKVWithASSAndFonts(codecPriv string, blockEvents []string, attachedFiles [][]byte) []byte {
	trackBody := append(append(append(
		mkvU(mkvTrackNumberID, 1),
		mkvU(mkvTrackTypeID, 0x11)...),
		mkvS(mkvCodecIDID, "S_TEXT/ASS")...),
		mkvEl(mkvCodecPrivateID, []byte(codecPriv))...,
	)
	tracks := mkvEl(mkvTracksID, mkvEl(mkvTrackEntryID, trackBody))
	info := mkvEl(mkvInfoID, mkvU(mkvTimecodeScaleID, 1000000))

	var attPayload []byte
	for _, af := range attachedFiles {
		attPayload = append(attPayload, af...)
	}
	attachments := mkvEl(mkvAttachmentsID, attPayload)

	var clusters []byte
	for i, ev := range blockEvents {
		ms := uint64((i + 1) * 1000)
		block := append(vintBytes(1, false), 0, 0, 0x80)
		block = append(block, []byte(ev)...)
		group := mkvEl(mkvBlockGroupID, append(mkvEl(mkvBlockID, block), mkvU(mkvBlockDurationID, 1500)...))
		clusters = append(clusters, mkvEl(mkvClusterID, append(mkvU(mkvTimestampID, ms), group...))...)
	}

	seekHead := mkvEl(mkvSeekHeadID, append(
		mkvSeek(mkvTracksID, 0),
		mkvSeek(mkvAttachmentsID, 0)...,
	))
	base := uint64(len(seekHead))
	seekHead = mkvEl(mkvSeekHeadID, append(
		mkvSeek(mkvTracksID, base+uint64(len(info))),
		mkvSeek(mkvAttachmentsID, base+uint64(len(info)+len(tracks)+len(clusters)))...,
	))

	segBody := append(append(append(append(seekHead, info...), tracks...), clusters...), attachments...)
	ebml := mkvEl(mkvEBMLID, mkvS(0x4282, "matroska"))
	return append(ebml, mkvEl(mkvSegmentID, segBody)...)
}

func TestParseASSFontFamiliesAndSFNTMatching(t *testing.T) {
	script := []byte(`[Script Info]
ScriptType: v4.00+

[V4+ Styles]
Format: Name, Fontsize, Fontname, PrimaryColour
Style: Default,20,Futura Medium,&H00FFFFFF
Style: Vertical,20,@Noto Sans CJK JP,&H00FFFFFF
Style: Duplicate,18,futura medium,&H00FFFFFF

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\fnOpen Sans\b1}Hello{\fnSource Han Sans\i1} world
`)
	families := ParseASSFontFamilies(script)
	want := []string{"Futura Medium", "Noto Sans CJK JP", "Open Sans", "Source Han Sans"}
	if !slices.Equal(families, want) {
		t.Fatalf("ParseASSFontFamilies() = %v, want %v", families, want)
	}

	futuraFont := buildTestSFNTFont("\x00\x01\x00\x00", []struct {
		platform uint16
		nameID   uint16
		value    string
	}{
		{platform: 3, nameID: 1, value: "Futura"},
		{platform: 3, nameID: 4, value: "Futura Medium"},
	})
	openSansFont := buildTestSFNTFont("OTTO", []struct {
		platform uint16
		nameID   uint16
		value    string
	}{
		{platform: 1, nameID: 16, value: "OpenSans"},
	})
	unusedFont := buildTestSFNTFont("\x00\x01\x00\x00", []struct {
		platform uint16
		nameID   uint16
		value    string
	}{
		{platform: 3, nameID: 1, value: "Comic Papyrus"},
	})

	if names := EmbeddedFontFamilyNames(futuraFont); !slices.Contains(names, "Futura") || !slices.Contains(names, "Futura Medium") {
		t.Fatalf("EmbeddedFontFamilyNames(futuraFont) = %v", names)
	}

	fonts := []FontAttachment{
		{Index: 0, Name: "font0.ttf", MIMEType: "font/ttf", Data: futuraFont},
		{Index: 1, Name: "font1.otf", MIMEType: "font/otf", Data: openSansFont},
		{Index: 2, Name: "font2.ttf", MIMEType: "font/ttf", Data: unusedFont},
	}

	// Under threshold: all 3 attachments returned unchanged.
	unfiltered, err := SelectReferencedFontAttachments(fonts, script, MaxUnfilteredFontBytes)
	if err != nil || len(unfiltered) != 3 {
		t.Fatalf("unfiltered got %d fonts, err=%v", len(unfiltered), err)
	}

	// Above threshold (set maxBytes below 3-font total, above 2-font total):
	// selects only the 2 referenced whole font attachments (Futura & OpenSans)
	// and drops Comic Papyrus.
	twoFontLimit := int64(len(futuraFont) + len(openSansFont) + 8)
	selected, err := SelectReferencedFontAttachments(fonts, script, twoFontLimit)
	if err != nil {
		t.Fatalf("SelectReferencedFontAttachments() unexpected error: %v", err)
	}
	if len(selected) != 2 || selected[0].Name != "font0.ttf" || selected[1].Name != "font1.otf" {
		t.Fatalf("SelectReferencedFontAttachments() = %+v, want font0.ttf and font1.otf", selected)
	}
}

func TestSanitizeFontAttachmentNameAndUntrustedBounds(t *testing.T) {
	t.Run("artifact scan budgets survive metadata resets", func(t *testing.T) {
		for _, limits := range []struct {
			reads int
			bytes int64
		}{{2, 64}, {64, 2}} {
			r := newRangeReader(context.Background(), bytes.NewReader(make([]byte, 64)), 64)
			r.maxReads, r.maxReadBytes = limits.reads, limits.bytes
			for n := 0; n < 2; n++ {
				r.segs = rangeMaxSegments
				if _, err := r.readFontBytes(int64(n), 1); err != nil {
					t.Fatal(err)
				}
			}
			r.segs = rangeMaxSegments
			if _, err := r.readFontBytes(2, 1); !errors.Is(err, errRangeBudget) {
				t.Fatalf("scan bypassed total budget: %v", err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := ReadMKVEmbeddedASS(ctx, bytes.NewReader(make([]byte, 64)), 64, 0); !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	})
	cases := []struct {
		raw      string
		mime     string
		idx      int
		wantName string
		wantMIME string
	}{
		{"../../etc/passwd", "application/x-truetype-font", 0, "attachment-0.ttf", "font/ttf"},
		{`..\..\Windows\Fonts\Futura.ttf`, "font/ttf", 1, "Futura.ttf", "font/ttf"},
		{".hidden.ttf", "font/ttf", 2, "attachment-2.ttf", "font/ttf"},
		{"malicious.sh", "font/otf", 3, "attachment-3.otf", "font/otf"},
		{"Valid-Font_Name.woff2", "font/woff2", 4, "Valid-Font_Name.woff2", "font/woff2"},
	}
	for _, tc := range cases {
		gotName := SanitizeFontAttachmentName(tc.raw, tc.mime, tc.idx)
		if gotName != tc.wantName {
			t.Errorf("SanitizeFontAttachmentName(%q, %q, %d) = %q, want %q", tc.raw, tc.mime, tc.idx, gotName, tc.wantName)
		}
		if strings.ContainsAny(gotName, `/\`) || strings.Contains(gotName, "..") {
			t.Errorf("unsafe attachment name %q", gotName)
		}
		gotMIME := FontAttachmentMIMEType(gotName)
		if gotMIME != tc.wantMIME {
			t.Errorf("FontAttachmentMIMEType(%q) = %q, want %q", gotName, gotMIME, tc.wantMIME)
		}
	}

	// Malformed SFNT / TTC inputs must never panic or allocate unboundedly.
	malformedInputs := [][]byte{
		nil,
		[]byte("short"),
		[]byte("ttcf\x00\x01\x00\x00\xff\xff\xff\xff"),
		[]byte("\x00\x01\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00name\x00\x00\x00\x00\xff\xff\xff\xff\x00\x00\x01\x00"),
	}
	for _, bad := range malformedInputs {
		if names := EmbeddedFontFamilyNames(bad); len(names) != 0 {
			t.Fatalf("expected nil names for malformed font input, got %v", names)
		}
	}
}

func TestReadMKVFontAttachmentsAndEmbeddedASS(t *testing.T) {
	codecPriv := "[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize\nStyle: Main,Futura,24\n"
	events := []string{
		"0,0,Main,,0,0,0,,{\\fnOpen Sans}Styled ASS line",
	}
	futura := buildTestSFNTFont("\x00\x01\x00\x00", []struct {
		platform uint16
		nameID   uint16
		value    string
	}{{platform: 3, nameID: 1, value: "Futura"}})
	openSans := buildTestSFNTFont("OTTO", []struct {
		platform uint16
		nameID   uint16
		value    string
	}{{platform: 3, nameID: 1, value: "Open Sans"}})

	mkvBytes := buildTestMKVWithASSAndFonts(codecPriv, events, [][]byte{
		buildMKVAttachedFile("../../Futura-Medium.ttf", "application/x-truetype-font", futura),
		buildMKVAttachedFile("cover.jpg", "image/jpeg", []byte("not-a-font")),
		buildMKVAttachedFile("OpenSans.otf", "font/otf", openSans),
	})

	reader := bytes.NewReader(mkvBytes)
	assOut, err := ReadMKVEmbeddedASS(context.Background(), reader, int64(len(mkvBytes)), 0)
	if err != nil {
		t.Fatalf("ReadMKVEmbeddedASS() unexpected error: %v", err)
	}
	assText := string(assOut)
	if !strings.Contains(assText, "Style: Main,Futura,24") || !strings.Contains(assText, `Dialogue: 0,0:00:01.00,0:00:02.50,Main,,0,0,0,,{\fnOpen Sans}Styled ASS line`) {
		t.Fatalf("unexpected ASS output:\n%s", assText)
	}

	fonts, err := ReadMKVFontAttachments(context.Background(), reader, int64(len(mkvBytes)), nil)
	if err != nil {
		t.Fatalf("ReadMKVFontAttachments() unexpected error: %v", err)
	}
	if len(fonts) != 2 {
		t.Fatalf("ReadMKVFontAttachments() got %d fonts, want 2", len(fonts))
	}
	if fonts[0].Name != "Futura-Medium.ttf" || fonts[0].MIMEType != "font/ttf" || !bytes.Equal(fonts[0].Data, futura) {
		t.Fatalf("font[0] mismatch: %+v", fonts[0])
	}
	if fonts[1].Name != "OpenSans.otf" || fonts[1].MIMEType != "font/otf" || !bytes.Equal(fonts[1].Data, openSans) {
		t.Fatalf("font[1] mismatch: %+v", fonts[1])
	}

	// Verify oversized pack failure when no ASS script references match.
	hugeUnused := FontAttachment{Index: 0, Name: "Unused.ttf", MIMEType: "font/ttf", Data: make([]byte, MaxUnfilteredFontBytes+1024)}
	copy(hugeUnused.Data, buildTestSFNTFont("\x00\x01\x00\x00", []struct {
		platform uint16
		nameID   uint16
		value    string
	}{{platform: 3, nameID: 1, value: "Unreferenced Family"}}))
	_, err = SelectReferencedFontAttachments([]FontAttachment{hugeUnused}, assOut, MaxUnfilteredFontBytes)
	if !errors.Is(err, ErrFontAttachmentSelectionUnavailable) || !errors.Is(err, ErrFontPackExceedsLimit) {
		t.Fatalf("expected ErrFontAttachmentSelectionUnavailable + ErrFontPackExceedsLimit, got %v", err)
	}

	// Multi-track stream-index namespace invariant + >4 MiB cluster + truncation check:
	// Track entries:
	//   streamIndex 0 -> TrackNumber 1 (video)
	//   streamIndex 1 -> TrackNumber 2 (audio)
	//   streamIndex 2 -> TrackNumber 3 (subtitle A: StyleSubA)
	//   streamIndex 3 -> TrackNumber 4 (subtitle B: StyleSubB)
	// Requesting streamIndex=3 must select subtitle B (TrackNumber 4), never subtitle A (TrackNumber 3).
	videoTrack := mkvEl(mkvTrackEntryID, append(append(
		mkvU(mkvTrackNumberID, 1),
		mkvU(mkvTrackTypeID, 0x01)...),
		mkvS(mkvCodecIDID, "V_MPEG4/ISO/AVC")...,
	))
	audioTrack := mkvEl(mkvTrackEntryID, append(append(
		mkvU(mkvTrackNumberID, 2),
		mkvU(mkvTrackTypeID, 0x02)...),
		mkvS(mkvCodecIDID, "A_AAC")...,
	))
	subATrack := mkvEl(mkvTrackEntryID, append(append(append(
		mkvU(mkvTrackNumberID, 3),
		mkvU(mkvTrackTypeID, 0x11)...),
		mkvS(mkvCodecIDID, "S_TEXT/ASS")...),
		mkvEl(mkvCodecPrivateID, []byte("[Script Info]\nScriptType: v4.00+\n[V4+ Styles]\nFormat: Name, Fontname\nStyle: StyleSubA,FontA\n"))...,
	))
	subBTrack := mkvEl(mkvTrackEntryID, append(append(append(
		mkvU(mkvTrackNumberID, 4),
		mkvU(mkvTrackTypeID, 0x11)...),
		mkvS(mkvCodecIDID, "S_TEXT/ASS")...),
		mkvEl(mkvCodecPrivateID, []byte("[Script Info]\nScriptType: v4.00+\n[V4+ Styles]\nFormat: Name, Fontname\nStyle: StyleSubB,FontB\n"))...,
	))
	multiTracks := mkvEl(mkvTracksID, append(append(append(videoTrack, audioTrack...), subATrack...), subBTrack...))
	info := mkvEl(mkvInfoID, mkvU(mkvTimecodeScaleID, 1000000))

	// Build a >4 MiB cluster containing a 4.5 MiB video SimpleBlock (TrackNumber 1)
	// plus SimpleBlocks for subtitle A (TrackNumber 3) and subtitle B (TrackNumber 4).
	largeVideoPayload := make([]byte, (4<<20)+(512<<10))
	videoBlock := append(vintBytes(1, false), 0, 0, 0x80)
	videoBlock = append(videoBlock, largeVideoPayload...)
	subABlock := append(vintBytes(3, false), 0, 0, 0x80)
	subABlock = append(subABlock, []byte("0,0,StyleSubA,,0,0,0,,TrackA cue in large cluster")...)
	subBBlock := append(vintBytes(4, false), 0, 0, 0x80)
	subBBlock = append(subBBlock, []byte("0,0,StyleSubB,,0,0,0,,TrackB cue in large cluster")...)
	largeCluster := mkvEl(mkvClusterID, append(append(append(
		mkvU(mkvTimestampID, 2000),
		mkvEl(mkvSimpleBlockID, videoBlock)...),
		mkvEl(mkvSimpleBlockID, subABlock)...),
		mkvEl(mkvSimpleBlockID, subBBlock)...,
	))

	// Also append 520 additional small clusters so total segment elements > 512,
	// with a final cue for subtitle B at cluster #520.
	var tailClusters []byte
	for i := 0; i < 520; i++ {
		ts := uint64(3000 + i*100)
		if i == 519 {
			finalB := append(vintBytes(4, false), 0, 0, 0x80)
			finalB = append(finalB, []byte("0,0,StyleSubB,,0,0,0,,TrackB final cue beyond 512 elements")...)
			tailClusters = append(tailClusters, mkvEl(mkvClusterID, append(mkvU(mkvTimestampID, ts), mkvEl(mkvSimpleBlockID, finalB)...))...)
		} else {
			vblk := append(vintBytes(1, false), 0, 0, 0x80, 0x00)
			tailClusters = append(tailClusters, mkvEl(mkvClusterID, append(mkvU(mkvTimestampID, ts), mkvEl(mkvSimpleBlockID, vblk)...))...)
		}
	}

	multiMKV := append(
		mkvEl(mkvEBMLID, mkvS(0x4282, "matroska")),
		mkvEl(mkvSegmentID, append(append(append(info, multiTracks...), largeCluster...), tailClusters...))...,
	)
	multiReader := bytes.NewReader(multiMKV)

	// Requesting streamIndex=3 (subtitle B) must return only subtitle B's header and cues,
	// including both the >4 MiB cluster cue and the cue past 512 segment elements.
	subBOut, err := ReadMKVEmbeddedASS(context.Background(), multiReader, int64(len(multiMKV)), 3)
	if err != nil {
		t.Fatalf("ReadMKVEmbeddedASS(streamIndex=3) unexpected error: %v", err)
	}
	subBText := string(subBOut)
	if strings.Contains(subBText, "StyleSubA") || strings.Contains(subBText, "TrackA cue") {
		t.Fatalf("streamIndex=3 wrongly matched TrackNumber=3 (subtitle A):\n%s", subBText)
	}
	if !strings.Contains(subBText, "Style: StyleSubB,FontB") ||
		!strings.Contains(subBText, "TrackB cue in large cluster") ||
		!strings.Contains(subBText, "TrackB final cue beyond 512 elements") {
		t.Fatalf("streamIndex=3 missing expected subtitle B header or cues:\n%s", subBText)
	}

	// Requesting streamIndex=2 (subtitle A) must return only subtitle A.
	subAOut, err := ReadMKVEmbeddedASS(context.Background(), multiReader, int64(len(multiMKV)), 2)
	if err != nil {
		t.Fatalf("ReadMKVEmbeddedASS(streamIndex=2) unexpected error: %v", err)
	}
	if !strings.Contains(string(subAOut), "TrackA cue in large cluster") || strings.Contains(string(subAOut), "StyleSubB") {
		t.Fatalf("streamIndex=2 mismatch:\n%s", string(subAOut))
	}

	// Requesting streamIndex=1 (audio track) must fail rather than selecting a subtitle track.
	if _, err := ReadMKVEmbeddedASS(context.Background(), multiReader, int64(len(multiMKV)), 1); err == nil {
		t.Fatalf("expected error when requesting non-subtitle streamIndex=1")
	}

	// Truncated Matroska container must fail closed with ErrASSTrackIncomplete
	// rather than returning header-only or partial dialogue as success.
	truncatedLen := int64(len(multiMKV) - 32)
	truncatedReader := bytes.NewReader(multiMKV[:truncatedLen])
	if _, err := ReadMKVEmbeddedASS(context.Background(), truncatedReader, int64(len(multiMKV)), 3); !errors.Is(err, ErrASSTrackIncomplete) {
		t.Fatalf("expected ErrASSTrackIncomplete on truncated MKV, got %v", err)
	}
}
