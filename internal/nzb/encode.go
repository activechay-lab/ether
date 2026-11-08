package nzb

import (
	"bytes"
	"encoding/xml"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Subset returns the NZB restricted to the files keep accepts, keeping the
// document metadata (such as the password). Parsing the result gives the same
// file names, segments and repost alternates as the originals. ok is false
// when no file is kept.
func (n *NZB) Subset(keep func(*File) bool) (data []byte, ok bool) {
	return n.SubsetMeta(keep, nil)
}

// SubsetMeta is Subset with extra <meta> values added (type -> value). The
// extra values make the subset a different document, with its own digest.
func (n *NZB) SubsetMeta(keep func(*File) bool, extra map[string]string) (data []byte, ok bool) {
	var files []*File
	for _, f := range n.Files {
		if keep(f) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, false
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<nzb xmlns=\"http://www.newzbin.com/DTD/2003/nzb\">\n")
	if len(n.Meta) > 0 || len(extra) > 0 {
		b.WriteString("<head>\n")
		// Sorted, so one input always gives the same bytes: the stream
		// engine keys release state, plans and cached segments by the
		// digest of the document.
		for _, typ := range slices.Sorted(maps.Keys(n.Meta)) {
			if _, replaced := extra[typ]; replaced {
				continue
			}
			for _, v := range n.Meta[typ] {
				b.WriteString(`<meta type="` + escAttr(typ) + `">` + escText(v) + "</meta>\n")
			}
		}
		for _, typ := range slices.Sorted(maps.Keys(extra)) {
			b.WriteString(`<meta type="` + escAttr(typ) + `">` + escText(extra[typ]) + "</meta>\n")
		}
		b.WriteString("</head>\n")
	}
	for _, f := range files {
		b.WriteString(`<file poster="` + escAttr(f.Poster) + `" subject="` + escAttr(subjectFor(f)) + `"`)
		if !f.Date.IsZero() {
			b.WriteString(` date="` + strconv.FormatInt(f.Date.Unix(), 10) + `"`)
		}
		b.WriteString(">\n<groups>")
		for _, g := range f.Groups {
			b.WriteString("<group>" + escText(g) + "</group>")
		}
		b.WriteString("</groups>\n<segments>")
		for _, s := range f.Segments {
			for _, id := range append([]string{s.MessageID}, s.Alternates...) {
				b.WriteString(`<segment bytes="` + strconv.FormatInt(s.Bytes, 10) + `" number="` + strconv.Itoa(s.Number) + `">` + escText(id) + "</segment>")
			}
		}
		b.WriteString("</segments>\n</file>\n")
	}
	b.WriteString("</nzb>\n")
	return b.Bytes(), true
}

func escText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func escAttr(s string) string { return escText(s) }

// subjectFor is the subject to write for f. A name resolved after parsing
// (from PAR2 or a yEnc header) is written into the subject, so parsing the
// subset gives that name without the original probing.
func subjectFor(f *File) string {
	if f.Name == "" || f.Name == NameFromSubject(f.Subject) {
		return f.Subject
	}
	return `"` + f.Name + `" yEnc (1/` + strconv.Itoa(f.SegmentCount()) + `)`
}
