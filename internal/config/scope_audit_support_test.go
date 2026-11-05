package config

func (s Scope) String() string {
	if s == Local {
		return "local"
	}
	return "shared"
}

// ScopeOf reports the scope of a setting by its JSON path: a top-level key
// ("usenet") or a section and key ("server.port"). Unknown paths are
// shared, the safe default for a cluster (one value everywhere).
func ScopeOf(path string) Scope {
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			if sub, ok := mixedSections[path[:i]]; ok {
				return sub[path[i+1:]]
			}
			return sectionScopes[path[:i]]
		}
	}
	return sectionScopes[path]
}
