package studio

import (
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/httpjson"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/braedonsaunders/ether/internal/plugins"
)

// Handler returns the HTTP handler for /api/plugins/studio/*.
func (s *Studio) Handler() http.Handler {
	return http.HandlerFunc(s.serveStudio)
}

// WrapPluginHandler wraps the plugin manager's /api/plugins/* handler so
// /api/plugins/studio/* routes to Studio and dev-instance view/action/call
// requests (e.g. /api/plugins/studio:{id}/views/{page}) are served directly
// by the Studio sandboxed dev instance.
func (s *Studio) WrapPluginHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/api/plugins")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "studio" || strings.HasPrefix(rel, "studio/") {
			s.serveStudio(w, r)
			return
		}
		if s.tryServeDevPlugin(w, r, rel) {
			return
		}
		if next != nil {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	})
}

func (s *Studio) tryServeDevPlugin(w http.ResponseWriter, r *http.Request, rel string) bool {
	head, tail, hasTail := strings.Cut(rel, "/")
	if !hasTail || head == "" {
		return false
	}
	devID := strings.TrimPrefix(head, "studio:")
	explicitStudio := strings.HasPrefix(head, "studio:")

	s.mu.Lock()
	proj, exists := s.lookupLocked(devID)
	s.mu.Unlock()
	if !exists {
		return false
	}

	// If not explicitly prefixed with "studio:" and the plugin is already installed
	// in the live manager, let the live manager serve it unless this is a Studio-only project.
	if !explicitStudio && s.mgr != nil {
		if _, err := s.mgr.Get(r.Context(), head); err == nil {
			return false
		}
	}

	subHead, subTail, _ := strings.Cut(tail, "/")
	switch {
	case r.Method == http.MethodGet && subHead == "views" && subTail != "":
		params := map[string]string{}
		for k, vals := range r.URL.Query() {
			if len(vals) > 0 {
				params[k] = vals[0]
			}
		}
		prev, err := s.PreviewProject(r.Context(), proj.ID, subTail, "", params)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return true
		}
		if len(prev.Spec) == 0 {
			writeError(w, http.StatusBadRequest, "bad_viewspec", "route did not return a ViewSpec document")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"plugin": proj.PluginID,
			"page":   subTail,
			"title":  prev.Title,
			"spec":   prev.Spec,
			"data":   prev.Data,
		})
		return true

	case r.Method == http.MethodPost && subHead == "actions" && subTail != "":
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		route := "/ether/action/" + subTail
		man, err := plugins.ParseManifest([]byte(proj.Files["manifest.json"]))
		if err == nil && man.Ether != nil {
			for _, cmd := range man.Ether.Contributes.Commands {
				if cmd.Key == subTail && cmd.Action != "" {
					if strings.HasPrefix(cmd.Action, "/") {
						route = cmd.Action
					} else {
						route = "/ether/action/" + cmd.Action
					}
					break
				}
			}
		}
		runResp, err := s.RunProject(r.Context(), proj.ID, RunRequest{
			Method: http.MethodPost,
			Path:   route,
			Body:   body,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return true
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if len(runResp.Body) == 0 {
			_, _ = w.Write([]byte("{}"))
		} else {
			_, _ = w.Write(runResp.Body)
		}
		return true

	case r.Method == http.MethodGet && subHead == "calls" && subTail == "":
		calls, hints, err := s.Calls(r.Context(), proj.ID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"calls": calls, "hints": hints})
		return true
	}
	return false
}

func (s *Studio) serveStudio(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	for _, prefix := range []string{"/api/plugins/studio", "/plugins/studio", "/studio"} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	path = strings.TrimPrefix(path, "/")

	switch {
	case r.Method == http.MethodGet && path == "templates":
		writeJSON(w, http.StatusOK, map[string]any{"templates": Templates()})
		return

	case r.Method == http.MethodGet && path == "projects":
		writeJSON(w, http.StatusOK, map[string]any{"projects": s.ListProjects(r.Context())})
		return

	case r.Method == http.MethodPost && path == "projects":
		var opts CreateProjectOptions
		if !decodeJSONBody(w, r, &opts) {
			return
		}
		proj, err := s.CreateProject(r.Context(), opts)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"project": proj})
		return

	case r.Method == http.MethodPost && path == "import":
		var body struct {
			PluginID string `json:"pluginId"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}
		proj, err := s.ImportPlugin(r.Context(), strings.TrimSpace(body.PluginID))
		if errors.Is(err, ErrNotEditable) {
			writeError(w, http.StatusUnprocessableEntity, "not_editable", "This plugin runs on another device, so its code is not on this server.")
			return
		}
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": proj})
		return

	case r.Method == http.MethodPost && path == "compile":
		var body struct {
			Files map[string]string `json:"files"`
			Entry string            `json:"entry"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}
		res := s.CompileFiles(body.Files, body.Entry)
		writeJSON(w, http.StatusOK, res)
		return

	case r.Method == http.MethodPost && path == "check":
		var body struct {
			ProjectID string            `json:"projectId"`
			Files     map[string]string `json:"files"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}
		res := s.CheckFiles(r.Context(), body.ProjectID, body.Files)
		writeJSON(w, http.StatusOK, res)
		return
	}

	if strings.HasPrefix(path, "projects/") {
		rest := strings.TrimPrefix(path, "projects/")
		id, action, _ := strings.Cut(rest, "/")
		if id == "" {
			writeError(w, http.StatusNotFound, "not_found", "project id required")
			return
		}
		s.serveProject(w, r, id, action)
		return
	}

	if strings.HasPrefix(path, "inspect/") {
		pluginID := strings.TrimPrefix(path, "inspect/")
		if pluginID == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "plugin id required")
			return
		}
		s.serveInspectPlugin(w, r, pluginID)
		return
	}

	writeError(w, http.StatusNotFound, "not_found", "route not found")
}

func (s *Studio) serveProject(w http.ResponseWriter, r *http.Request, id, action string) {
	ctx := r.Context()
	switch {
	case r.Method == http.MethodGet && action == "":
		p, err := s.GetProject(ctx, id)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": p})

	case r.Method == http.MethodDelete && action == "":
		if err := s.DeleteProject(ctx, id); err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})

	case r.Method == http.MethodPut && (action == "" || action == "files"):
		var body struct {
			Files    map[string]string `json:"files"`
			Shadow   *bool             `json:"shadow"`
			Settings map[string]any    `json:"settings"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}
		if len(body.Files) > 0 || body.Shadow != nil || body.Settings != nil {
			proj, comp, err := s.SaveFiles(ctx, id, body.Files, body.Shadow, body.Settings)
			if err != nil {
				writeStudioError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"project": proj,
				"compile": comp,
			})
			return
		}
		proj, err := s.GetProject(ctx, id)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": proj})

	case r.Method == http.MethodPost && action == "compile":
		var body struct {
			Files map[string]string `json:"files"`
			Entry string            `json:"entry"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body)
		if len(body.Files) > 0 {
			proj, comp, err := s.SaveFiles(ctx, id, body.Files, nil, nil)
			if err != nil {
				writeStudioError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"project":       proj,
				"ok":            comp.OK,
				"diagnostics":   comp.Diagnostics,
				"jsSize":        comp.JSSize,
				"sourcemapSize": comp.SourcemapSize,
			})
			return
		}
		p, err := s.GetProject(ctx, id)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		comp := s.CompileFiles(p.Files, body.Entry)
		writeJSON(w, http.StatusOK, comp)

	case r.Method == http.MethodPost && action == "check":
		var body struct {
			Files map[string]string `json:"files"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body)
		if len(body.Files) > 0 {
			if _, _, err := s.SaveFiles(ctx, id, body.Files, nil, nil); err != nil {
				writeStudioError(w, err)
				return
			}
		}
		rep, err := s.CheckProject(ctx, id)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rep)

	case r.Method == http.MethodPost && action == "run":
		var req RunRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		res, err := s.RunProject(ctx, id, req)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)

	case (r.Method == http.MethodGet || r.Method == http.MethodPost) && action == "preview":
		pageKey := r.URL.Query().Get("page")
		routeOverride := r.URL.Query().Get("route")
		params := map[string]string{}
		for k, vals := range r.URL.Query() {
			if len(vals) > 0 {
				params[k] = vals[0]
			}
		}
		if r.Method == http.MethodPost {
			var body struct {
				Page   string            `json:"page"`
				Route  string            `json:"route"`
				Params map[string]string `json:"params"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
			if body.Page != "" {
				pageKey = body.Page
			}
			if body.Route != "" {
				routeOverride = body.Route
			}
			for k, v := range body.Params {
				params[k] = v
			}
		}
		prev, err := s.PreviewProject(ctx, id, pageKey, routeOverride, params)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, prev)

	case r.Method == http.MethodPost && action == "install/preview":
		prev, err := s.PreviewInstallProject(ctx, id)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preview": prev})

	case r.Method == http.MethodPost && action == "install":
		var body struct {
			Grants                 plugins.Grants `json:"grants"`
			Settings               map[string]any `json:"settings,omitempty"`
			AutoUpdate             bool           `json:"autoUpdate"`
			Shadow                 bool           `json:"shadow"`
			AcceptAddedPermissions bool           `json:"acceptAddedPermissions"`
			// AsCopy installs beside the plugin the draft came from.
			AsCopy bool `json:"asCopy"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		install := s.InstallProject
		if body.AsCopy {
			install = s.InstallProjectCopy
		}
		rec, err := install(ctx, id, plugins.InstallOptions{
			Grants:                 body.Grants,
			Settings:               body.Settings,
			AutoUpdate:             body.AutoUpdate,
			Shadow:                 body.Shadow,
			AcceptAddedPermissions: body.AcceptAddedPermissions,
		})
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"plugin": rec})

	case r.Method == http.MethodGet && action == "shadow":
		entries, err := s.Journal(ctx, id, true)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		p, _ := s.GetProject(ctx, id)
		shadowOn := false
		if p != nil {
			shadowOn = p.Shadow
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"shadow":  shadowOn,
			"entries": entries,
		})

	case r.Method == http.MethodPost && action == "shadow":
		var body struct {
			Shadow bool `json:"shadow"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}
		p, err := s.SetShadow(ctx, id, body.Shadow)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": p, "shadow": p.Shadow})

	case r.Method == http.MethodGet && action == "journal":
		entries, err := s.Journal(ctx, id, r.URL.Query().Get("shadow") == "1")
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})

	case r.Method == http.MethodPost && action == "undo":
		n, err := s.Undo(ctx, id)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		entries, _ := s.Journal(ctx, id, false)
		writeJSON(w, http.StatusOK, map[string]any{"undone": n, "entries": entries})

	case r.Method == http.MethodGet && action == "calls":
		calls, hints, err := s.Calls(ctx, id)
		if err != nil {
			writeStudioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"calls": calls, "hints": hints})

	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func (s *Studio) serveInspectPlugin(w http.ResponseWriter, r *http.Request, pluginID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if calls, hints, err := s.Calls(r.Context(), pluginID); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"calls": calls, "hints": hints})
		return
	}
	if s.mgr == nil {
		writeError(w, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	subReq := httptest.NewRequest(http.MethodGet, "/"+pluginID+"/calls", nil).WithContext(r.Context())
	subRec := httptest.NewRecorder()
	s.mgr.Handler().ServeHTTP(subRec, subReq)
	if subRec.Code != http.StatusOK {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(subRec.Code)
		_, _ = w.Write(subRec.Body.Bytes())
		return
	}
	var payload struct {
		Calls []plugins.CallRecord `json:"calls"`
	}
	_ = json.Unmarshal(subRec.Body.Bytes(), &payload)
	redacted := make([]plugins.CallRecord, len(payload.Calls))
	for i, c := range payload.Calls {
		c.Request = RedactJSON([]byte(c.Request))
		c.Response = RedactJSON([]byte(c.Response))
		redacted[i] = c
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"calls": redacted,
		"hints": InspectCalls(redacted),
	})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return false
	}
	return true
}

func writeStudioError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrProjectNotFound), errors.Is(err, plugins.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		var ve *plugins.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error": map[string]any{
					"code":    "validation_failed",
					"message": ve.Error(),
					"fields":  ve.Fields,
				},
			})
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	httpjson.Write(w, status, v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	})
}
