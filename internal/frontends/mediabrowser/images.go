package mediabrowser

import (
	"context"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
	"net/http"
	"strconv"
	"strings"
)

func (i *instance) gridImagesFor(r *http.Request, titles []frontend.Title) map[frontend.RatingKey]map[string]any {
	return i.gridImageRows(r, titles, nil, nil)
}

// gridListImages signs the poster a library row shows. Logos, backdrops and
// thumbs stay off the page so fifty SortName rows do not carry every grant.
func (i *instance) gridListImages(r *http.Request, titles []frontend.Title) map[frontend.RatingKey]map[string]any {
	return i.gridImageRows(r, titles, nil, []string{frontend.ImagePoster, frontend.ImageStill})
}

func (i *instance) gridImageRows(r *http.Request, titles []frontend.Title, keys []frontend.RatingKey, kinds []string) map[frontend.RatingKey]map[string]any {
	out := make(map[frontend.RatingKey]map[string]any)
	token := authhdr.MediaBrowserToken(r)
	if len(titles) > 0 {
		if signer, ok := i.host.ImageGrants.(frontend.ImagePageSigner); ok {
			for start := 0; start < len(titles); start += 50 {
				end := min(start+50, len(titles))
				rows, err := signer.SignPage(r.Context(), frontend.ImagePageRequest{Credential: token, Device: device(r), Titles: titles[start:end], Kinds: gridKinds(kinds)})
				if err != nil {
					continue
				}
				i.collectGridImages(out, rows)
			}
			return out
		}
		if len(keys) == 0 {
			keys = make([]frontend.RatingKey, len(titles))
			for n, title := range titles {
				keys[n] = title.Key
			}
		}
	}
	batch, ok := i.host.ImageGrants.(frontend.ImageGrantBatch)
	if !ok {
		return out
	}
	for start := 0; start < len(keys); start += 50 {
		end := min(start+50, len(keys))
		rows, err := batch.IssueBatch(r.Context(), frontend.ImageBatchRequest{Credential: token, Device: device(r), Items: keys[start:end], Kinds: gridKinds(kinds)})
		if err != nil {
			continue
		}
		i.collectGridImages(out, rows)
	}
	return out
}

func gridKinds(kinds []string) []string {
	if len(kinds) > 0 {
		return kinds
	}
	return []string{frontend.ImagePoster, frontend.ImageStill, frontend.ImageBackdrop, frontend.ImageLogo, frontend.ImageThumb, frontend.ImageBanner}
}

func (i *instance) collectGridImages(out map[frontend.RatingKey]map[string]any, rows []frontend.ImageBatchItem) {
	for _, row := range rows {
		if !row.Authoritative {
			continue
		}
		hasPoster := false
		for _, image := range row.Images {
			if image.Image.Kind == frontend.ImagePoster && image.Secret != "" {
				hasPoster = true
				break
			}
		}
		tags := map[string]string{}
		backdrops := []string{}
		result := map[string]any{}
		primarySelected := false
		for _, img := range row.Images {
			if img.Secret == "" {
				continue
			}
			switch img.Image.Kind {
			case frontend.ImagePoster:
				tags["Primary"] = img.Secret
				primarySelected = true
				i.addImageMetadata(result, "Primary", img.Secret, img.Image)
			case frontend.ImageStill:
				if !primarySelected && !hasPoster {
					tags["Primary"] = img.Secret
					primarySelected = true
					i.addImageMetadata(result, "Primary", img.Secret, img.Image)
				}
			case frontend.ImageBackdrop:
				backdrops = append(backdrops, img.Secret)
				i.addImageMetadata(result, "Backdrop", img.Secret, img.Image)
			case frontend.ImageLogo:
				tags["Logo"] = img.Secret
				i.addImageMetadata(result, "Logo", img.Secret, img.Image)
			case frontend.ImageThumb:
				tags["Thumb"] = img.Secret
				i.addImageMetadata(result, "Thumb", img.Secret, img.Image)
			case frontend.ImageBanner:
				tags["Banner"] = img.Secret
				i.addImageMetadata(result, "Banner", img.Secret, img.Image)
			}
		}
		if len(tags) > 0 {
			result["ImageTags"] = tags
		}
		if len(backdrops) > 0 {
			result["BackdropImageTags"] = backdrops
		}
		out[row.Item] = result
	}
}

func (i *instance) imageTags(r *http.Request, detail frontend.ItemDetail, token string) (map[string]any, []string, map[string]any) {
	tags := map[string]any{}
	backdrops := []string{}
	metadata := map[string]any{}
	if i.host.ImageGrants == nil || !detail.ImagesAuthoritative {
		return tags, backdrops, metadata
	}
	if signer, ok := i.host.ImageGrants.(frontend.ImagePageSigner); ok {
		rows, err := signer.SignPage(r.Context(), frontend.ImagePageRequest{
			Credential: token, Device: device(r), Titles: []frontend.Title{detail.Title},
			Kinds: []string{frontend.ImagePoster, frontend.ImageStill, frontend.ImageBackdrop, frontend.ImageLogo, frontend.ImageThumb, frontend.ImageBanner},
		})
		if err == nil && len(rows) > 0 && rows[0].Authoritative && rows[0].Item == detail.Title.Key {
			images := make([]frontend.Image, len(rows[0].Images))
			secrets := make([]string, len(rows[0].Images))
			for n, granted := range rows[0].Images {
				images[n] = granted.Image
				secrets[n] = granted.Secret
			}
			tags, backdrops, metadata = i.tagGrantedImages(images, secrets)
			return tags, backdrops, metadata
		}
	}
	hasPoster := false
	for _, img := range detail.Images {
		switch strings.ToLower(img.Kind) {
		case "poster", "primary":
			hasPoster = true
		}
	}
	primarySelected := false
	for _, img := range detail.Images {
		kind := ""
		switch strings.ToLower(img.Kind) {
		case "poster", "primary":
			kind = "Primary"
		case "logo":
			kind = "Logo"
		case "thumb":
			kind = "Thumb"
		case "still":
			// An episode still is the primary image when the title has no poster.
			if hasPoster || primarySelected {
				kind = "Thumb"
			} else {
				kind = "Primary"
			}
		case "banner":
			kind = "Banner"
		case "backdrop":
			kind = "Backdrop"
		}
		if kind == "" {
			continue
		}
		if _, exists := tags[kind]; exists && kind != "Backdrop" {
			continue
		}
		grant, err := i.host.ImageGrants.Issue(r.Context(), frontend.ImageGrantRequest{Credential: token, Device: device(r), Item: detail.Title.Key, Image: img})
		if err == nil && grant != "" {
			if kind == "Backdrop" {
				backdrops = append(backdrops, grant)
			} else {
				tags[kind] = grant
			}
			if kind == "Primary" {
				primarySelected = true
			}
			i.addImageMetadata(metadata, kind, grant, img)
		}
	}
	return tags, backdrops, metadata
}

func (i *instance) tagGrantedImages(images []frontend.Image, secrets []string) (map[string]any, []string, map[string]any) {
	tags := map[string]any{}
	backdrops := []string{}
	metadata := map[string]any{}
	hasPoster := false
	for _, img := range images {
		switch strings.ToLower(img.Kind) {
		case "poster", "primary":
			hasPoster = true
		}
	}
	primarySelected := false
	for n, img := range images {
		kind := ""
		switch strings.ToLower(img.Kind) {
		case "poster", "primary":
			kind = "Primary"
		case "logo":
			kind = "Logo"
		case "thumb":
			kind = "Thumb"
		case "still":
			if hasPoster || primarySelected {
				kind = "Thumb"
			} else {
				kind = "Primary"
			}
		case "banner":
			kind = "Banner"
		case "backdrop":
			kind = "Backdrop"
		}
		if kind == "" || n >= len(secrets) || secrets[n] == "" {
			continue
		}
		if _, exists := tags[kind]; exists && kind != "Backdrop" {
			continue
		}
		if kind == "Backdrop" {
			backdrops = append(backdrops, secrets[n])
		} else {
			tags[kind] = secrets[n]
		}
		if kind == "Primary" {
			primarySelected = true
		}
		i.addImageMetadata(metadata, kind, secrets[n], img)
	}
	return tags, backdrops, metadata
}

// addImageMetadata attaches facts only after the matching grant was issued.
func (i *instance) addImageMetadata(dto map[string]any, kind, tag string, img frontend.Image) {
	if tag == "" {
		return
	}
	if kind == "Primary" && img.Width > 0 && img.Height > 0 {
		dto["PrimaryImageAspectRatio"] = float64(img.Width) / float64(img.Height)
	}
	if i.protocol != "jellyfin" || img.BlurHash == "" {
		return
	}
	hashes, _ := dto["ImageBlurHashes"].(map[string]any)
	if hashes == nil {
		hashes = map[string]any{}
		dto["ImageBlurHashes"] = hashes
	}
	byTag, _ := hashes[kind].(map[string]string)
	if byTag == nil {
		byTag = map[string]string{}
		hashes[kind] = byTag
	}
	byTag[tag] = img.BlurHash
}
func (i *instance) image(w http.ResponseWriter, r *http.Request) {
	if i.host.ImageGrants == nil {
		http.NotFound(w, r)
		return
	}
	tags := route.Query(r.URL.Query(), "tag")
	if len(tags) == 1 && tags[0] != "" {
		i.host.ImageGrants.Serve(w, r, tags[0])
		return
	}
	if len(tags) > 1 {
		http.NotFound(w, r)
		return
	}
	// Jellyfin treats tag as an optional cache-buster, and clients such as
	// Moonfin omit it (#405). Serve those requests in serveTaglessImage.
	i.serveTaglessImage(w, r)
}

// serveTaglessImage answers an image request with no tag. A tag is a
// short-lived capability minted for one viewer after that viewer was
// shown the item; dropping the requirement outright would let anyone who
// can guess an item id fetch its artwork and probe for hidden titles.
// The equivalent that weakens nothing: require the client's own live
// credential (header or api_key, as Jellyfin clients send on image
// fetches), resolve the item and image exactly as that viewer, then mint
// the same scoped grant in-process and serve through it. Grant Issue and
// Serve re-authorize against current policy, so this path can only ever
// return artwork the credential holder could already have obtained a tag
// for. Anonymous requests, unresolvable ids and hidden items all get the
// same 404, so the endpoint is not an oracle for what exists.
func (i *instance) serveTaglessImage(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.authenticate(r)
	if err != nil || v == nil {
		http.NotFound(w, r)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{viewer: v, token: token}))
	if v, token, err = i.viewer(r); err != nil || v == nil {
		http.NotFound(w, r)
		return
	}
	wireID, imageType := r.PathValue("itemId"), r.PathValue("imageType")
	index := 0
	if raw := r.PathValue("index"); raw != "" {
		if index, err = strconv.Atoi(raw); err != nil || index < 0 {
			http.NotFound(w, r)
			return
		}
	}
	primaryOnly := strings.EqualFold(imageType, "Primary") && index == 0
	if _, err := ids.ParseGUID(ids.Channel, wireID); err == nil {
		if !primaryOnly || i.host.LiveTV == nil {
			http.NotFound(w, r)
			return
		}
		ch, err := i.host.LiveTV.ResolveChannelGUID(r.Context(), v, wireID)
		if err != nil || ch.ID == "" {
			http.NotFound(w, r)
			return
		}
		for _, img := range ch.Images {
			if img.Kind != frontend.ImageChannel {
				continue
			}
			grant, err := i.host.ImageGrants.Issue(r.Context(), frontend.ImageGrantRequest{Credential: token, Device: device(r), Channel: ch.ID, Image: img})
			if err == nil && grant != "" {
				i.host.ImageGrants.Serve(w, r, grant)
				return
			}
			break
		}
		http.NotFound(w, r)
		return
	}
	if viewNum, err := ids.ParseGUID(ids.View, wireID); err == nil {
		if !primaryOnly {
			http.NotFound(w, r)
			return
		}
		secret, _, ok := i.libraryCollageGrant(r, v, viewNum, wireID)
		if !ok || secret == "" {
			http.NotFound(w, r)
			return
		}
		i.host.ImageGrants.Serve(w, r, secret)
		return
	}
	key, err := ids.ParseRatingKeyGUID(wireID)
	if err != nil || i.host.Experience == nil {
		http.NotFound(w, r)
		return
	}
	detail, err := i.host.Experience.Item(i.identityContext(r), frontend.ItemRequest{Viewer: v, Device: device(r), Key: key})
	if err != nil || !detail.ImagesAuthoritative {
		http.NotFound(w, r)
		return
	}
	img, ok := taglessImage(detail.Images, imageType, index)
	if !ok {
		http.NotFound(w, r)
		return
	}
	item := detail.Title.Key
	if item == 0 {
		item = key
	}
	grant, err := i.host.ImageGrants.Issue(r.Context(), frontend.ImageGrantRequest{Credential: token, Device: device(r), Item: item, Image: img})
	if err != nil || grant == "" {
		http.NotFound(w, r)
		return
	}
	i.host.ImageGrants.Serve(w, r, grant)
}

// taglessImage picks the index-th descriptor for a Jellyfin/Emby image
// type, in the same priority imageTags uses when it mints tags: a poster
// is Primary, an episode still stands in when there is no poster, and a
// still is also the Thumb fallback.
func taglessImage(images []frontend.Image, imageType string, index int) (frontend.Image, bool) {
	var kinds []string
	switch strings.ToLower(imageType) {
	case "primary":
		kinds = []string{frontend.ImagePoster, "primary", frontend.ImageStill}
	case "art", "backdrop":
		kinds = []string{frontend.ImageBackdrop}
	case "logo":
		kinds = []string{frontend.ImageLogo}
	case "thumb":
		kinds = []string{frontend.ImageThumb, frontend.ImageStill}
	case "banner":
		kinds = []string{frontend.ImageBanner}
	default:
		return frontend.Image{}, false
	}
	var candidates []frontend.Image
	for _, kind := range kinds {
		for _, img := range images {
			if strings.EqualFold(img.Kind, kind) && img.Path != "" {
				candidates = append(candidates, img)
			}
		}
	}
	if index >= len(candidates) {
		return frontend.Image{}, false
	}
	return candidates[index], true
}
