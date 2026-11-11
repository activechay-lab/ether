package diagnostics

import (
	"net/url"
	"regexp"
	"strings"
)

// Query values whose syntax and meaning are known not to contain credentials.
// Unknown keys and their values are masked: clients can send
// arbitrary credentials in extension parameters.
var supportQueryValue = regexp.MustCompile(`^[A-Za-z0-9_,-]{0,256}$`)
var supportDirectoryValue = regexp.MustCompile(`^(?:[0-9]+|playlists)(?:,(?:[0-9]+|playlists))*$`)
var supportNumericValue = regexp.MustCompile(`^[0-9]{1,10}$`)
var supportEnumList = regexp.MustCompile(`^[A-Za-z0-9]{1,40}(, ?[A-Za-z0-9]{1,40}){0,15}$`)

var supportQueryKeys = map[string]bool{
	"contentdirectoryid": true, "pinnedcontentdirectoryid": true,
	"excludecontinuewatching": true, "count": true, "excludeelements": true,
	"excludefields": true, "excludemusic": true, "excludephotos": true,
	"includerecentchannels": true, "includedetails": true, "includemeta": true,
	"includeondeck": true, "includecollections": true, "includestations": true,
	"includeexternalmetadata": true, "includelibraryplaylists": true,
	"x-plex-container-start": true, "x-plex-container-size": true,
	"startindex": true, "limit": true, "skip": true, "recursive": true,
	"includetotalrecordcount": true, "enableimages": true, "enableuserdata": true,
	"isplayed": true, "sortorder": true,
	"videobitrate": true, "maxvideobitrate": true, "videoquality": true, "videoresolution": true,
	"directplay": true, "directstream": true, "hasmde": true, "mediaindex": true, "partindex": true, "protocol": true,
	// Jellyfin/Emby listing shape: enumerations, never names or ids.
	"includeitemtypes": true, "excludeitemtypes": true, "mediatypes": true, "sortby": true, "fields": true,
	"filters": true, "isfavorite": true, "ismissing": true, "isunaired": true, "collapseboxsetitems": true,
	"enabletotalrecordcount": true, "imagetypelimit": true, "enableimagetypes": true,
}

var supportFieldNames = map[string]bool{
	"actor": true, "collection": true, "country": true, "director": true, "label": true,
	"location": true, "mood": true, "part": true, "producer": true, "similar": true,
	"photo": true, "vast": true, "topic": true, "writer": true, "file": true, "tagline": true,
	"genre": true, "guid": true, "media": true, "role": true, "studio": true,
}

func SafeQuery(q url.Values) string {
	out := url.Values{}
	unknown, truncated := false, false
	count := 0
	for key, values := range q {
		// Keys are untrusted too; do not reflect arbitrary text into the ZIP.
		if !supportQueryValue.MatchString(key) || len(key) > 80 {
			unknown = true
			continue
		}
		safe := supportQueryKeys[strings.ToLower(key)]
		if !safe {
			unknown = true
			continue
		}
		for _, v := range values {
			if count >= 32 {
				truncated = true
				break
			}
			name := strings.ToLower(key)
			valid := supportNumericValue.MatchString(v)
			switch name {
			case "recursive", "includetotalrecordcount", "enableimages", "enableuserdata", "isplayed":
				valid = v == "0" || v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "false")
			case "videoresolution":
				dims := strings.Split(v, "x")
				valid = len(dims) == 2 && supportNumericValue.MatchString(dims[0]) && supportNumericValue.MatchString(dims[1])
			case "protocol":
				valid = v == "hls" || v == "http" || v == "dash"
			case "sortorder":
				valid = strings.EqualFold(v, "ascending") || strings.EqualFold(v, "descending")
			case "includeitemtypes", "excludeitemtypes", "mediatypes", "sortby", "fields", "filters", "enableimagetypes",
				"isfavorite", "ismissing", "isunaired", "collapseboxsetitems", "enabletotalrecordcount":
				// Client enumerations and booleans: names of types, fields and
				// filters, never user text.
				valid = supportEnumList.MatchString(v)
			}
			if name == "contentdirectoryid" || name == "pinnedcontentdirectoryid" {
				valid = supportDirectoryValue.MatchString(v)
			}
			if name == "excludeelements" || name == "excludefields" {
				valid = len(v) <= 256
				for _, field := range strings.Split(v, ",") {
					if !supportFieldNames[strings.ToLower(field)] {
						valid = false
						break
					}
				}
			}
			if !safe || !valid {
				v = "[redacted]"
			}
			out.Add(key, v)
			if len(out.Encode()) > 3800 {
				items := out[key]
				if len(items) == 1 {
					delete(out, key)
				} else {
					out[key] = items[:len(items)-1]
				}
				truncated = true
				break
			}
			count++
		}
	}
	if unknown {
		out.Add("[redacted-key]", "[redacted]")
	}
	if truncated {
		out.Set("[truncated]", "1")
	}
	return out.Encode()
}
