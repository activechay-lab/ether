// Schema and purge assertions used by offline cross-package acceptance tests.
package store

// HasDependency reports whether component declared a RegisterAfter dependency
// that is present. Absent dependencies are ignored, matching Migrate.
func HasDependency(component, dependency string) bool {
	regMu.Lock()
	defer regMu.Unlock()
	return dependencies[component][dependency]
}

// ComponentOrder is the migration component sequence Migrate will apply.
func ComponentOrder() []string {
	var order []string
	seen := map[string]bool{}
	for _, m := range registered() {
		if seen[m.component] {
			continue
		}
		seen[m.component] = true
		order = append(order, m.component)
	}
	return order
}

// ViewerCoverage reports the registered per-viewer tables (table ->
// component) and the exemptions (table -> reason).
func ViewerCoverage() (covered, exempt map[string]string) {
	viewerMu.Lock()
	defer viewerMu.Unlock()
	covered = make(map[string]string, len(viewerTables))
	for table, component := range viewerTables {
		covered[table] = component
	}
	exempt = make(map[string]string, len(viewerExempts))
	for table, reason := range viewerExempts {
		exempt[table] = reason
	}
	return covered, exempt
}
