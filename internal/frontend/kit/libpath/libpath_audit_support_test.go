package libpath

import (
	"path"

	"github.com/braedonsaunders/ether/internal/core"
)

func Movie(title core.Title) string {
	name := MovieName(title)
	return path.Join("Movies", name, name+".mkv")
}
func Show(title core.Title) string { return path.Join("TV", Safe(title.Title)) }

// Collection places a safe filename beneath a collection directory.
func Collection(name, filename string) string {
	return path.Join("Collections", Safe(name), Safe(filename))
}
