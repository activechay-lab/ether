package viewspec

import ()

// Validate checks a raw ViewSpec document against the base language: closed
// unions, strict objects, depth cap and path guards. Class-name fields are
// allowed here; they are a built-in-spec privilege.
func Validate(raw []byte) Result {
	return validate(raw, false)
}
