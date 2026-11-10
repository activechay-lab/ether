package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/textclip"
)

func init() {
	// v5: guided-setup step results (spec §8), one row per step the
	// owner has tested or skipped.
	store.Register("plugins", 5, `CREATE TABLE plugins_setup(plugin TEXT NOT NULL,step TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',detail TEXT NOT NULL DEFAULT '',at INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(plugin,step));`)
}

// Setup step statuses stored in plugins_setup.
const (
	SetupPending = "pending"
	SetupOK      = "ok"
	SetupFailed  = "failed"
	SetupSkipped = "skipped"
)

// SetupStepState is the stored result of one setup step.
type SetupStepState struct {
	Step   string    `json:"step"`
	Status string    `json:"status"` // pending|ok|failed|skipped
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// SetupTestResult is the plugin's answer to a setup test call.
type SetupTestResult struct {
	OK     bool              `json:"ok"`
	Detail string            `json:"detail,omitempty"`
	Fields map[string]string `json:"fields,omitempty"` // settings key -> problem
}

func validateSetup(e *EtherBlock, add func(string, string), label func(string, string, int)) {
	if e.Contributes.Setup == nil {
		return
	}
	steps := e.Contributes.Setup.Steps
	if len(steps) == 0 {
		add("ether.contributes.setup.steps", "must list at least one step")
	}
	schemaFields := settingsFieldKeys(e.Settings)
	seen := map[string]bool{}
	for i, s := range steps {
		base := fmt.Sprintf("ether.contributes.setup.steps[%d]", i)
		if !keyRe.MatchString(s.Key) || seen[s.Key] {
			add(base+".key", "must be a unique simple key")
		}
		seen[s.Key] = true
		label(base+".label", s.Label, 60)
		if s.Hint != "" && !strings.HasPrefix(s.Hint, "$t:") {
			if msg := LintCopy(s.Hint, 120); msg != "" {
				add(base+".hint", msg)
			}
			if strings.Count(strings.TrimRight(s.Hint, "."), ".") > 0 {
				add(base+".hint", "must be one sentence")
			}
		}
		for j, f := range s.Fields {
			if !keyRe.MatchString(f) {
				add(fmt.Sprintf("%s.fields[%d]", base, j), "must be a settings field key")
			} else if schemaFields != nil && !schemaFields[f] {
				add(fmt.Sprintf("%s.fields[%d]", base, j), "is not a field in the settings schema")
			}
		}
		if s.Test != "" && !strings.HasPrefix(s.Test, "/") {
			add(base+".test", "must be a plugin route starting with /")
		}
	}
}

// settingsFieldKeys extracts every "key" string from an inline settings
// schema. A "$ref" schema is resolved at install, so it returns nil
// and field membership is not checked here.
func settingsFieldKeys(raw json.RawMessage) map[string]bool {
	if len(raw) == 0 {
		return map[string]bool{}
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	if obj, ok := doc.(map[string]any); ok {
		if _, isRef := obj["$ref"]; isRef {
			return nil
		}
	}
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if k, ok := n["key"].(string); ok {
				out[k] = true
			}
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(doc)
	return out
}

func (s *storage) setupState(ctx context.Context, plugin string) ([]SetupStepState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT step,status,detail,at FROM plugins_setup WHERE plugin=? ORDER BY step`, plugin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SetupStepState
	for rows.Next() {
		var st SetupStepState
		var at int64
		if err := rows.Scan(&st.Step, &st.Status, &st.Detail, &at); err != nil {
			return nil, err
		}
		st.At = time.Unix(at, 0).UTC()
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *storage) putSetupState(ctx context.Context, plugin, step, status, detail string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO plugins_setup(plugin,step,status,detail,at) VALUES(?,?,?,?,?)
ON CONFLICT(plugin,step) DO UPDATE SET status=excluded.status,detail=excluded.detail,at=excluded.at`,
		plugin, step, status, detail, s.now().Unix())
	return err
}

// SetupState returns every recorded setup step result for a plugin,
// with unrecorded steps reported as pending, in manifest order.
func (m *Manager) SetupState(ctx context.Context, pluginID string) ([]SetupStepState, error) {
	r, err := m.Get(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	stored, err := m.store.setupState(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	byStep := map[string]SetupStepState{}
	for _, st := range stored {
		byStep[st.Step] = st
	}
	var out []SetupStepState
	if e := r.Manifest.EffectiveEther(); e != nil && e.Contributes.Setup != nil {
		for _, step := range e.Contributes.Setup.Steps {
			if st, ok := byStep[step.Key]; ok {
				out = append(out, st)
			} else {
				out = append(out, SetupStepState{Step: step.Key, Status: SetupPending})
			}
		}
	}
	return out, nil
}

// SetupBlockers lists the required setup steps still holding a plugin
// back: steps that have neither passed nor been skipped by the owner
// (spec §8). Content adapters keep libraries hidden and sync stopped
// while this is non-empty.
func (m *Manager) SetupBlockers(ctx context.Context, pluginID string) ([]string, error) {
	r, err := m.Get(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	e := r.Manifest.EffectiveEther()
	if e == nil || e.Contributes.Setup == nil {
		return nil, nil
	}
	state, err := m.SetupState(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	byStep := map[string]SetupStepState{}
	for _, st := range state {
		byStep[st.Step] = st
	}
	var blocked []string
	for _, step := range e.Contributes.Setup.Steps {
		if !step.Required {
			continue
		}
		if st := byStep[step.Key]; st.Status != SetupOK && st.Status != SetupSkipped {
			blocked = append(blocked, step.Key)
		}
	}
	return blocked, nil
}

func setupStepOf(r Record, key string) (SetupStep, bool) {
	if e := r.Manifest.EffectiveEther(); e != nil && e.Contributes.Setup != nil {
		for _, s := range e.Contributes.Setup.Steps {
			if s.Key == key {
				return s, true
			}
		}
	}
	return SetupStep{}, false
}

// TestSetupStep runs one setup step's test against the saved settings
// and records the result. Only the step's non-secret field values are
// sent; secrets reach the plugin through SecretGet, never in the test
// body (spec §8). A step with no test route passes once it is run.
func (m *Manager) TestSetupStep(ctx context.Context, pluginID, stepKey string) (SetupTestResult, error) {
	r, err := m.Get(ctx, pluginID)
	if err != nil {
		return SetupTestResult{}, err
	}
	step, ok := setupStepOf(r, stepKey)
	if !ok {
		return SetupTestResult{}, fmt.Errorf("%w: plugin has no setup step %q", ErrUnavailable, stepKey)
	}
	if step.Test == "" {
		res := SetupTestResult{OK: true}
		return res, m.store.putSetupState(ctx, pluginID, stepKey, SetupOK, "")
	}
	settings, err := m.HostFor(pluginID).Settings(ctx)
	if err != nil {
		return SetupTestResult{}, err
	}
	values := map[string]any{}
	for _, f := range step.Fields {
		if v, present := settings[f]; present {
			values[f] = v
		}
	}
	body, err := json.Marshal(map[string]any{"step": stepKey, "fields": values})
	if err != nil {
		return SetupTestResult{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := m.Call(callCtx, pluginID, Request{Method: "POST", Path: step.Test, Body: body})
	if err != nil {
		_ = m.store.putSetupState(ctx, pluginID, stepKey, SetupFailed, "The plugin did not answer")
		return SetupTestResult{OK: false, Detail: "The plugin did not answer"}, nil
	}
	var res SetupTestResult
	if len(resp.Body) > 0 {
		_ = json.Unmarshal(resp.Body, &res)
	}
	if resp.Status >= 300 {
		res.OK = false
		if res.Detail == "" {
			res.Detail = fmt.Sprintf("The plugin answered with status %d", resp.Status)
		}
	}
	res.Detail = textclip.Bytes(res.Detail, 500)
	status := SetupFailed
	if res.OK {
		status = SetupOK
	}
	if err := m.store.putSetupState(ctx, pluginID, stepKey, status, res.Detail); err != nil {
		return SetupTestResult{}, err
	}
	return res, nil
}

// SkipSetupStep records the owner's "skip for now" on one step, which
// unblocks the plugin while leaving the step visibly unfinished.
func (m *Manager) SkipSetupStep(ctx context.Context, pluginID, stepKey string) error {
	r, err := m.Get(ctx, pluginID)
	if err != nil {
		return err
	}
	if _, ok := setupStepOf(r, stepKey); !ok {
		return fmt.Errorf("%w: plugin has no setup step %q", ErrUnavailable, stepKey)
	}
	return m.store.putSetupState(ctx, pluginID, stepKey, SetupSkipped, "")
}
