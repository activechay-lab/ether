package artwork

import (
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AssetFolder rejects symlink traversal, including ancestors of a chosen folder.
func AssetFolder(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("choose an absolute folder")
	}
	path = filepath.Clean(path)
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("choose a folder without symbolic links")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return path, nil
}

type assetHit struct {
	path  string
	until time.Time
}
type LocalAssets struct {
	mu   sync.Mutex
	hits map[string]assetHit
}

// Path consults only two exact names. The bounded lazy index caches misses too.
func (a *LocalAssets) Path(folder string, t *core.Title) string {
	if folder == "" || t == nil || (t.Type != core.TypeMovie && t.Type != core.TypeShow) {
		return ""
	}
	key := fmt.Sprintf("%s:%s:%s:%d", folder, t.Key.String(), t.Title, t.Year)
	a.mu.Lock()
	defer a.mu.Unlock()
	if h, ok := a.hits[key]; ok && time.Now().Before(h.until) {
		return h.path
	}
	result := ""
	if root, err := AssetFolder(folder); err == nil {
		id := t.IDs.TMDB
		if id == 0 && (t.Key.Kind() == core.KindMovie || t.Key.Kind() == core.KindShow) {
			id = t.Key.TMDB()
		}
		names := []string{}
		if id > 0 {
			names = append(names, fmt.Sprintf("%d.jpg", id))
		}
		title := fmt.Sprintf("%s (%d)", t.Title, t.Year)
		if !strings.ContainsAny(title, `/\`) && title != ".." {
			names = append(names, filepath.Join(title, "poster.jpg"))
		}
		for _, name := range names {
			p := filepath.Join(root, name)
			if _, err := AssetFolder(filepath.Dir(p)); err != nil {
				continue
			}
			if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() && st.Size() > 0 && st.Size() <= 20<<20 {
				result = p
				break
			}
		}
	}
	if len(a.hits) >= 512 {
		clear(a.hits)
	}
	if a.hits == nil {
		a.hits = map[string]assetHit{}
	}
	a.hits[key] = assetHit{result, time.Now().Add(10 * time.Second)}
	return result
}
