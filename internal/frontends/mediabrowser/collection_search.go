package mediabrowser

import (
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

const collectionsViewNumber int64 = 1<<61 + 1

func (i *instance) collectionsFolder(r *http.Request, v *frontend.Viewer) (map[string]any, bool) {
	page, err := i.host.Experience.Collections(r.Context(), v, device(r), frontend.CollectionQuery{ExistsOnly: true, Limit: 1})
	if err != nil || page.Total == 0 {
		return nil, false
	}
	id, _ := ids.GUID(ids.View, collectionsViewNumber)
	item := map[string]any{"Id": id, "Name": "Collections", "Type": "CollectionFolder", "CollectionType": "boxsets", "IsFolder": true, "ServerId": i.info.ID, "DisplayPreferencesId": id, "SortName": "Collections"}
	if i.protocol == "jellyfin" {
		i.jellyfinCollectionFolder(item, id)
		item["ParentId"] = jellyfinRootID()
	}
	if i.protocol == "emby" {
		i.embyViewDTO(item)
	}
	return item, true
}

// searchPage shares type-aware search between Items and Search/Hints. Collections
// use the same authorized complete source as the BoxSet library listing.
func (i *instance) searchPage(r *http.Request, v *frontend.Viewer, term string, q frontend.ItemQuery) (frontend.SearchPage, error) {
	box := false
	for _, value := range route.Query(r.URL.Query(), "IncludeItemTypes") {
		for _, kind := range splitCSV(value) {
			if strings.EqualFold(kind, "BoxSet") {
				box = true
			}
		}
	}
	for _, kind := range q.ExcludedTypes {
		if kind == "boxset" {
			box = false
		}
	}
	if !box {
		return i.host.Experience.Search(r.Context(), v, device(r), term, q)
	}
	offset, limit := max(q.Offset, 0), q.Limit
	if limit <= 0 {
		limit = 50
	}
	window := min(offset+limit, 1000)
	titleQuery := q
	titleQuery.Offset, titleQuery.Limit = 0, window
	var result frontend.SearchPage
	var err error
	if len(q.Types) > 0 {
		result, err = i.host.Experience.Search(r.Context(), v, device(r), term, titleQuery)
		if err != nil {
			return result, err
		}
	}
	collections, err := i.host.Experience.Collections(r.Context(), v, device(r), frontend.CollectionQuery{Query: term, Limit: window})
	if err != nil && err != frontend.ErrNotAvailable {
		return result, err
	}
	for _, c := range collections.Items {
		result.Page.Items = append(result.Page.Items, frontend.Title{Key: c.Key, Title: c.Title, Summary: c.Summary, Type: "boxset"})
	}
	result.Page.Total += collections.Total
	start := min(offset, min(window, len(result.Page.Items)))
	end := min(window, len(result.Page.Items))
	result.Page.Items = result.Page.Items[start:end]
	return result, nil
}
