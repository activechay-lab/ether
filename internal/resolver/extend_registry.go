package resolver

import (
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

// Registry wiring for ranking and rules (WS1a): validation and rule
// compilation consult the descriptors the console renders from, so a
// registered plugin step or field is accepted with its declared shape and
// an unregistered one is rejected on registry-aware paths. Plugin steps
// and fields are evaluated by the optional hosted hot-path adapter.

type extendState struct {
	mu       sync.Mutex
	registry *extend.Registry
	hot      RankingHooks
	version  uint64
	fields   map[string]releasepolicy.FieldSpec
}

// SetExtendRegistry hands the resolver the descriptor registry. The app
// calls it when built-in descriptors register: the resolver is built
// before the extensibility runtime exists.
func (s *Service) SetExtendRegistry(r *extend.Registry) {
	s.extend.mu.Lock()
	s.extend.registry = r
	s.extend.fields = nil
	s.extend.mu.Unlock()
}

func (s *Service) extendRegistry() *extend.Registry {
	s.extend.mu.Lock()
	defer s.extend.mu.Unlock()
	return s.extend.registry
}

// fieldSpecs snapshots the rule fields the registry serves, merged over
// the built-in table so a partially built registry (tests) still resolves
// every built-in. The snapshot is rebuilt when the registry version moves.
func (s *Service) fieldSpecs() map[string]releasepolicy.FieldSpec {
	r := s.extendRegistry()
	if r == nil {
		return nil
	}
	s.extend.mu.Lock()
	defer s.extend.mu.Unlock()
	if s.extend.fields != nil && s.extend.version == r.Version() {
		return s.extend.fields
	}
	fields := map[string]releasepolicy.FieldSpec{}
	for _, f := range releasepolicy.BuiltinFields() {
		fields[f.ID] = f
	}
	for _, d := range r.List(extend.KindRuleField) {
		meta, err := extend.MetaOf[extend.RuleFieldMeta](d)
		if err != nil {
			continue
		}
		meta = meta.Resolved()
		fields[d.ID] = releasepolicy.FieldSpec{
			ID:              d.ID,
			ValueClass:      meta.ValueClass,
			Operators:       meta.Operators,
			DefaultOperator: meta.DefaultOperator,
			SingleValue:     meta.SingleValue,
			External:        d.Owner != extend.Builtin,
		}
	}
	s.extend.fields = fields
	s.extend.version = r.Version()
	return fields
}

// fieldLookup is the releasepolicy.FieldLookup for this service, or nil
// (built-in table) when no registry is wired.
func (s *Service) fieldLookup() releasepolicy.FieldLookup {
	fields := s.fieldSpecs()
	if fields == nil {
		return nil
	}
	return func(id string) (releasepolicy.FieldSpec, bool) {
		f, ok := fields[id]
		return f, ok
	}
}

// compileRules compiles profile rules against the registry's field table.
func (s *Service) compileRules(rules []core.ReleaseRule) (*releasepolicy.Policy, error) {
	return releasepolicy.CompileWith(rules, s.fieldLookup())
}
