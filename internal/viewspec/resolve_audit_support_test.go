package viewspec

import (
	"fmt"
	"strings"
)

// ResolvePath reads a dot path off a scope value, mirroring resolvePath in
// the TypeScript package. A missing path returns (nil, nil); a path that
// names __proto__, constructor or prototype is an error. The guard lives
// here, not only in validation, so it holds for every caller.
func ResolvePath(scope any, path string) (any, error) {
	current := scope
	for _, segment := range strings.Split(path, ".") {
		if forbiddenSegments[segment] {
			return nil, fmt.Errorf("illegal field path segment: %s", segment)
		}
		if current == nil {
			return nil, nil
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, nil
		}
		current = obj[segment]
	}
	return current, nil
}
