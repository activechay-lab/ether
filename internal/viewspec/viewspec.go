// Package viewspec is the Go port of AppKit ViewSpec validation
// (@braedonsaunders/appkit-viewspec). Plugin pages arrive as untrusted JSON
// documents (docs/extend/SPEC.md §5.6); Ether validates them server-side with
// this package and again in the console before rendering. The two validators
// share the same rules so they can never disagree:
//
//   - The block and cell unions are closed. An unknown kind or a stray
//     property is a rejection, never a best-effort render.
//   - Block nesting depth is measured on the raw document before any
//     structural parse, so an over-deep document is refused cheaply.
//   - Field paths may not name __proto__, constructor or prototype.
//
// ValidatePlugin adds the Ether plugin rules on top: class-name fields are
// rejected (plugins cannot restyle the console), widget and frame names must
// come from the closed Ether registry, filter bars may only use the controls
// that make sense for Ether, and tables use the app variant.
package viewspec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// SpecVersion is the only ViewSpec version Ether accepts.
const SpecVersion = 1

// MaxBlockDepth caps nested block containers (paper, grid, panel, repeat,
// frame), mirroring MAX_BLOCK_DEPTH in the TypeScript package.
const MaxBlockDepth = 6

// Result is the outcome of validation. When OK is false, Errors names every
// problem found, each prefixed with the document path that caused it.
type Result struct {
	OK     bool
	Errors []string
}

func ok() Result { return Result{OK: true} }

func fail(errs ...string) Result { return Result{OK: false, Errors: errs} }

// ValidatePlugin checks a document that came from a plugin. On top of the
// base language it rejects class-name fields, requires widget and frame
// names from the Ether registry (with prop shape checks), limits filter-bar
// controls to search, and requires the app table variant.
func ValidatePlugin(raw []byte) Result {
	return validate(raw, true)
}

func validate(raw []byte, plugin bool) Result {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fail("(root): invalid JSON: " + err.Error())
	}
	root, isObj := doc.(map[string]any)
	if !isObj {
		return fail("(root): spec must be an object")
	}
	// Depth is walked on the raw value before structural validation, exactly
	// as the TypeScript validator does: the block union is recursive, so an
	// over-deep document must be refused before the expensive walk.
	if rawDepth(root["header"], 1) > MaxBlockDepth || rawDepth(root["body"], 1) > MaxBlockDepth {
		return fail(fmt.Sprintf("block nesting exceeds the maximum depth of %d", MaxBlockDepth))
	}
	v := &validator{plugin: plugin}
	if plugin {
		v.rejectClassFields(doc, "")
	}
	v.page(root, "")
	if len(v.errors) > 0 {
		return fail(v.errors...)
	}
	return ok()
}

// rawDepth measures nesting through "blocks" arrays without validating
// anything, stopping as soon as the cap is exceeded.
func rawDepth(value any, depth int) int {
	arr, isArr := value.([]any)
	if !isArr {
		return depth
	}
	deepest := depth
	for _, entry := range arr {
		obj, isObj := entry.(map[string]any)
		if !isObj {
			continue
		}
		if nested, isArr := obj["blocks"].([]any); isArr {
			if found := rawDepth(nested, depth+1); found > deepest {
				deepest = found
			}
			if deepest > MaxBlockDepth {
				return deepest
			}
		}
	}
	return deepest
}

var forbiddenSegments = map[string]bool{
	"__proto__":   true,
	"constructor": true,
	"prototype":   true,
}

// validFieldPath reports whether a dot path stays off the prototype chain.
func validFieldPath(path string) bool {
	if len(path) < 1 || len(path) > 200 {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if forbiddenSegments[segment] {
			return false
		}
	}
	return true
}

var (
	routeRe     = regexp.MustCompile(`^/[A-Za-z0-9\-_/\[\]().]*$`)
	widgetSlug  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	classFields = map[string]bool{
		"className":         true,
		"bodyClassName":     true,
		"itemClassName":     true,
		"actionsClassName":  true,
		"headerClassName":   true,
		"fallbackClassName": true,
		"labelClassName":    true,
	}
)
