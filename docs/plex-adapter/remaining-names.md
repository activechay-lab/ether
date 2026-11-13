# Remaining Prism name matches

Snapshot: `prism/combined`, production Go files under `internal`, excluding `internal/frontends` and tests. Exact expression: `"plex"|"jellyfin"|"emby"|Frontend(Plex|Jellyfin|Emby)\b|isPlex|IsPlex|plex[A-Z]`.

613 matching lines. This is an inventory, not a claim that all matches are justified wiring. Runtime gaps are called out below.

## internal/admin/admin.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/admin.go:523:	h.plexServerRoutes(api)
```

## internal/admin/auth.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/auth.go:173:		signInMethods = append(signInMethods, "plex")
internal/admin/auth.go:185:		"plexEnabled":      cfg.Frontends == nil || cfg.Frontends["plex"].Enabled,
```

## internal/admin/config.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/config.go:18:	"plexservers":  "plexServers",
internal/admin/config.go:20:	"markers":      "markers", "server": "server", "plex": "plex", "usenet": "usenet", "indexers": "indexers",
internal/admin/config.go:36:func (h *Handler) saveConfig(next, prev config.Config, plexFields map[string]json.RawMessage) ([]FieldError, error) {
internal/admin/config.go:49:		_, claimProvided := plexFields["claimed"]
internal/admin/config.go:57:		if raw, ok := plexFields["token"]; !ok || isRedactedJSON(raw) || staleEmptyIdentity || next.Plex.Token == prev.Plex.Token {
internal/admin/config.go:60:		if _, ok := plexFields["ownerplexid"]; !ok || (staleEmptyIdentity && next.Plex.OwnerPlexID == 0) || next.Plex.OwnerPlexID == prev.Plex.OwnerPlexID {
internal/admin/config.go:63:		if _, ok := plexFields["ownerusername"]; !ok || (staleEmptyIdentity && next.Plex.OwnerUsername == "") || next.Plex.OwnerUsername == prev.Plex.OwnerUsername {
internal/admin/config.go:112:	if err := rejectDuplicateKeys(body, "config", "plex"); err != nil {
internal/admin/config.go:120:			if err := rejectDuplicateKeys(envelope, "plex"); err != nil {
internal/admin/config.go:129:	if _, present := fields["plexServers"]; !present {
internal/admin/config.go:132:	var plexRaw json.RawMessage
internal/admin/config.go:135:		if strings.EqualFold(key, "plex") {
internal/admin/config.go:140:			plexRaw, hasPlex = value, true
internal/admin/config.go:146:		present, err = validatePlexClaimInput(plexRaw)
internal/admin/config.go:162:	errs, err := h.saveConfig(next, prev, identityFields(plexRaw))
internal/admin/config.go:236:	if field == "plex" {
internal/admin/config.go:282:	if field == "plex" {
```

## internal/admin/connect_apps.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/connect_apps.go:19:var connectableSurfaces = []string{"jellyfin", "emby", "silo", "stremio", "webdav", "m3u", "xtream"}
internal/admin/connect_apps.go:87:		if enabled["plex"] {
internal/admin/connect_apps.go:88:			out = append(out, "plex")
```

## internal/admin/frontend_wiring.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/frontend_wiring.go:10:const accountSetupID = "plex" // Wire the owner account setup identity.
internal/admin/frontend_wiring.go:15:	case "plex": // Wire the externally managed account and main listener.
internal/admin/frontend_wiring.go:17:	case "jellyfin", "emby": // Wire native API connection options.
```

## internal/admin/handlers.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/handlers.go:39:	plexDone := cfg.Plex.Claimed && cfg.Plex.Token != ""
internal/admin/handlers.go:40:	plexDetail := "not signed in"
internal/admin/handlers.go:41:	if plexDone {
internal/admin/handlers.go:42:		plexDetail = "claimed"
internal/admin/handlers.go:44:			plexDetail = "claimed by " + cfg.Plex.OwnerUsername
internal/admin/handlers.go:47:		plexDetail = "signed in, not claimed"
internal/admin/handlers.go:75:		{ID: accountSetupID, Title: "Sign in to plex.tv and claim", Complete: plexDone, Required: true, Detail: plexDetail},
```

## internal/admin/lifecycle_api.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/lifecycle_api.go:27:	Source     string `json:"source"` // owner | local | invite | plex | plexHome | dlnaGuest
internal/admin/lifecycle_api.go:29:	PlexShared bool   `json:"plexShared"`
internal/admin/lifecycle_api.go:33:	ManagedBy  string `json:"managedBy"` // "plex" or ""
internal/admin/lifecycle_api.go:46:		lc.Source = "plexHome"
internal/admin/lifecycle_api.go:48:		lc.Source = "plex"
internal/admin/lifecycle_api.go:56:		lc.ManagedBy = "plex"
```

## internal/admin/plexservers_api.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/plexservers_api.go:34:func (h *Handler) plexServerRoutes(api func(string, http.HandlerFunc)) {
internal/admin/plexservers_api.go:65:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:71:func (h *Handler) plexServerError(w http.ResponseWriter, err error) {
internal/admin/plexservers_api.go:111:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:117:type plexServerBody struct {
internal/admin/plexservers_api.go:124:	var body plexServerBody
internal/admin/plexservers_api.go:126:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:131:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:140:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:143:	var body plexServerBody
internal/admin/plexservers_api.go:145:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:150:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:159:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:166:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:171:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:191:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:200:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:204:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:213:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:218:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:228:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:238:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:245:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:255:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:260:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:270:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:275:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:285:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:290:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:299:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:304:		h.plexServerError(w, err)
internal/admin/plexservers_api.go:313:		h.plexServerError(w, core.ErrNotFound)
internal/admin/plexservers_api.go:319:		h.plexServerError(w, err)
```

## internal/admin/plexwatch_api.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/plexwatch_api.go:15:	PlexID         int64  `json:"plexId,omitempty"`
internal/admin/plexwatch_api.go:46:	Status(ctx context.Context, userID, plexID int64) (PlexWatchStatus, error)
internal/admin/plexwatch_api.go:47:	BeginPIN(ctx context.Context, userID, plexID int64) (PlexWatchStatus, error)
internal/admin/plexwatch_api.go:48:	FinishPIN(ctx context.Context, userID, plexID int64) (PlexWatchStatus, error)
internal/admin/plexwatch_api.go:49:	SetDirection(ctx context.Context, userID, plexID int64, direction, interval int) (PlexWatchStatus, error)
internal/admin/plexwatch_api.go:50:	Disconnect(ctx context.Context, userID, plexID int64) (PlexWatchStatus, error)
internal/admin/plexwatch_api.go:51:	Retry(ctx context.Context, userID, plexID int64) (PlexWatchStatus, error)
internal/admin/plexwatch_api.go:52:	ExportCurrent(ctx context.Context, userID, plexID int64) (PlexWatchStatus, error)
internal/admin/plexwatch_api.go:53:	ImportCurrent(ctx context.Context, userID, plexID int64) (PlexWatchStatus, error)
internal/admin/plexwatch_api.go:56:func (h *Handler) plexWatchViewer(w http.ResponseWriter, r *http.Request) (*ViewerRecord, bool) {
internal/admin/plexwatch_api.go:91:// plexWatchCall runs one viewer action with the viewer's stored Plex id.
internal/admin/plexwatch_api.go:92:func (h *Handler) plexWatchCall(w http.ResponseWriter, r *http.Request, call func(PlexWatch, context.Context, int64, int64) (PlexWatchStatus, error)) {
internal/admin/plexwatch_api.go:93:	v, ok := h.plexWatchViewer(w, r)
internal/admin/plexwatch_api.go:102:	h.plexWatchCall(w, r, PlexWatch.Status)
internal/admin/plexwatch_api.go:106:	h.plexWatchCall(w, r, PlexWatch.BeginPIN)
internal/admin/plexwatch_api.go:110:	h.plexWatchCall(w, r, PlexWatch.FinishPIN)
internal/admin/plexwatch_api.go:114:	h.plexWatchCall(w, r, PlexWatch.Disconnect)
internal/admin/plexwatch_api.go:118:	h.plexWatchCall(w, r, PlexWatch.Retry)
internal/admin/plexwatch_api.go:122:	h.plexWatchCall(w, r, PlexWatch.ExportCurrent)
internal/admin/plexwatch_api.go:126:	h.plexWatchCall(w, r, PlexWatch.ImportCurrent)
internal/admin/plexwatch_api.go:130:	v, ok := h.plexWatchViewer(w, r)
```

## internal/admin/stream.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/stream.go:246:	plexEnabled := cfg.Frontends == nil || cfg.Frontends["plex"].Enabled
internal/admin/stream.go:247:	if !plexEnabled {
internal/admin/stream.go:260:		"plexEnabled": plexEnabled, "serverName": cfg.Server.FriendlyName,
```

## internal/admin/viewers_api.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/viewers_api.go:145:	PlexID          int64           `json:"plexId"`
```

## internal/admin/viewers_plex_import.go

Owner administration of named protocol settings and account integrations; identity and API vocabulary.

```text
internal/admin/viewers_plex_import.go:187:		h.plexServerError(w, err)
internal/admin/viewers_plex_import.go:271:		h.plexServerError(w, err)
internal/admin/viewers_plex_import.go:360:		h.plexServerError(w, err)
internal/admin/viewers_plex_import.go:408:		h.plexServerError(w, err)
internal/admin/viewers_plex_import.go:439:		h.plexServerError(w, err)
internal/admin/viewers_plex_import.go:449:	var isPlexViewer bool
internal/admin/viewers_plex_import.go:453:				isPlexViewer = v.PlexID > 0
internal/admin/viewers_plex_import.go:458:	if !isOwner && isPlexViewer && acctName != "" {
internal/admin/viewers_plex_import.go:490:		h.plexServerError(w, err)
```

## internal/app/admin.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/admin.go:74:		PlexAuth:         &plexAuth{app: a, pins: map[string]*plextv.PIN{}},
internal/app/admin.go:75:		SignIn:           &plexSignIn{app: a, pins: map[string]*plextv.PIN{}},
internal/app/admin.go:239:		PlexWatch:       a.plexWatchAPI(),
internal/app/admin.go:241:		PlexServers:     a.plexServersAPI(),
internal/app/admin.go:294:func (a *App) plexWatchAPI() admin.PlexWatch {
internal/app/admin.go:295:	if a.plexWatch == nil {
internal/app/admin.go:298:	return plexWatchAdmin{a.plexWatch}
internal/app/admin.go:336:// plexAuth adapts the plex.tv PIN sign-in and claim flow to the wizard.
internal/app/admin.go:337:type plexAuth struct {
internal/app/admin.go:344:func (p *plexAuth) client() *plextv.Client { return p.app.registrar.SignInClient() }
internal/app/admin.go:346:func (p *plexAuth) StartPIN(ctx context.Context) (id, code, authURL string, err error) {
internal/app/admin.go:358:func (p *plexAuth) PollPIN(ctx context.Context, id string) (bool, error) {
internal/app/admin.go:407:func (p *plexAuth) Claim(ctx context.Context) error {
internal/app/admin.go:428:func (p *plexAuth) Status() any { return p.app.registrar.Status() }
internal/app/admin.go:430:func (p *plexAuth) SyncFriends(ctx context.Context) error {
internal/app/admin.go:434:// plexSignIn is the admin login: a plex.tv PIN whose token the admin
internal/app/admin.go:435:// handler accepts only if it belongs to the owner. Unlike plexAuth it
internal/app/admin.go:437:type plexSignIn struct {
internal/app/admin.go:443:func (p *plexSignIn) StartPIN(ctx context.Context, forwardURL string) (id, code, authURL string, err error) {
internal/app/admin.go:456:func (p *plexSignIn) PINToken(ctx context.Context, id string) (string, error) {
```

## internal/app/agent_tools.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/agent_tools.go:771:	for _, id := range []string{"plex", "jellyfin", "emby", "stremio", "dlna", "webdav", "m3u"} {
internal/app/agent_tools.go:789:				c.Frontends["plex"] = config.Frontend{Enabled: true} // Preserve the default main-listener registration.
```

## internal/app/app.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/app.go:232:	plexGUID            *plexguid.Service
internal/app/app.go:233:	plexMeta            *plexmeta.Service
internal/app/app.go:234:	plexWatch           *plexwatch.Service
internal/app/app.go:235:	plexServers         *plexwatch.Servers
internal/app/app.go:269:	plexMetaMu        sync.Mutex
internal/app/app.go:270:	plexMetaClose     func()
internal/app/app.go:782:	if a.plexWatch != nil {
internal/app/app.go:783:		background.start(func() { a.plexWatch.Run(ctx) })
internal/app/app.go:784:		background.start(func() { a.cluster.RunAsLeader(ctx, "plex watch imports", a.plexWatch.RunImports) })
internal/app/app.go:786:	if a.plexServers != nil {
internal/app/app.go:787:		background.start(func() { a.cluster.RunAsLeader(ctx, "plex media server sync", a.plexServers.Run) })
```

## internal/app/build_identity.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/build_identity.go:102:	if cfg.Plex.Claimed && cfg.Plex.Token != "" && (cfg.Frontends == nil || cfg.Frontends["plex"].Enabled) {
```

## internal/app/build_integrations.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/build_integrations.go:107:	mux.Handle("/", a.frontends.plexHandler())
```

## internal/app/build_playback.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/build_playback.go:131:				Source:           "plex", // Account provenance identity, not a behavior switch.
```

## internal/app/compat_catalog.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/compat_catalog.go:63:		{Protocol: "plex", Name: "Plex Web", Devices: []string{"Browser"}, DirectPlay: "browser", Certification: "builtin"},
internal/app/compat_catalog.go:64:		{Protocol: "plex", Name: "Plex for Android phones", Product: "Plex for Android (Mobile)", Devices: []string{"Android"}, DirectPlay: "reported", Certification: "builtin"},
internal/app/compat_catalog.go:65:		{Protocol: "plex", Name: "Plex for Android TV", Product: "Plex for Android (TV)", Devices: []string{"Android TV"}, DirectPlay: "reported", Certification: "builtin"},
internal/app/compat_catalog.go:66:		{Protocol: "plex", Name: "Plex for iOS", Devices: []string{"iOS"}, DirectPlay: "reported", Certification: "builtin"},
internal/app/compat_catalog.go:67:		{Protocol: "plex", Name: "Plex for Apple TV", Devices: []string{"Apple TV"}, DirectPlay: "reported", Certification: "builtin"},
internal/app/compat_catalog.go:68:		{Protocol: "plex", Name: "Plex HTPC", Devices: []string{"Home theater PC"}, DirectPlay: "reported", Certification: "builtin"},
internal/app/compat_catalog.go:69:		{Protocol: "plex", Name: "Plex for Windows", Devices: []string{"Home theater PC"}, DirectPlay: "reported", Certification: "builtin"},
internal/app/compat_catalog.go:70:		{Protocol: "plex", Name: "Plex for Mac", Devices: []string{"Mac"}, DirectPlay: "reported", Certification: "builtin"},
internal/app/compat_catalog.go:71:		{Protocol: "plex", Name: "Plex for Kodi", Product: "PM4K", Devices: []string{"Kodi"}, DirectPlay: "reported", Certification: "builtin"},
internal/app/compat_catalog.go:72:		{Protocol: "jellyfin", Name: "Swiftfin", Devices: []string{"Apple TV", "iOS"}, DirectPlay: "reported", Certification: "pending"},
internal/app/compat_catalog.go:73:		{Protocol: "jellyfin", Name: "Findroid", Devices: []string{"Android"}, Subtitles: []string{"srt", "ass"}, DirectPlay: "findroid", Certification: "pending"},
internal/app/compat_catalog.go:74:		{Protocol: "jellyfin", Name: "Streamyfin", Devices: []string{"Android"}, VideoCodecs: []string{"h264", "hevc", "vp8", "vp9", "av1"}, Subtitles: []string{"srt", "vtt", "ttml"}, DirectPlay: "listed", Note: "no-dv5", Certification: "pending"},
internal/app/compat_catalog.go:75:		{Protocol: "jellyfin", Name: "Jellyfin Android TV", Devices: []string{"Android TV", "Google TV", "NVIDIA Shield"}, DirectPlay: "reported", Certification: "pending"},
internal/app/compat_catalog.go:76:		{Protocol: "jellyfin", Name: "JellyCon", Devices: []string{"Kodi"}, DirectPlay: "open", Note: "h264-ac3", Certification: "pending"},
internal/app/compat_catalog.go:77:		{Protocol: "jellyfin", Name: "Infuse", Devices: []string{"Apple TV", "iOS"}, DirectPlay: "reported", Note: "capture", Certification: "pending"},
internal/app/compat_catalog.go:78:		{Protocol: "jellyfin", Name: "Jellyfin for Roku", Devices: []string{"Roku"}, DirectPlay: "reported", Certification: "pending"},
internal/app/compat_catalog.go:79:		{Protocol: "emby", Name: "Emby Web", Devices: []string{"Browser"}, DirectPlay: "browser", Certification: "pending"},
internal/app/compat_catalog.go:80:		{Protocol: "emby", Name: "Emby for Android TV", Devices: []string{"Android TV"}, DirectPlay: "reported", Certification: "pending"},
internal/app/compat_catalog.go:81:		{Protocol: "emby", Name: "Emby for TV", Devices: []string{"Apple TV"}, DirectPlay: "reported", Note: "unsupported-shell", Certification: "unsupported"},
internal/app/compat_catalog.go:82:		{Protocol: "emby", Name: "Infuse", Devices: []string{"Apple TV", "iOS"}, DirectPlay: "reported", Note: "capture", Certification: "pending"},
internal/app/compat_catalog.go:83:		{Protocol: "emby", Name: "Emby SDK", Devices: []string{}, DirectPlay: "reported", Note: "sdk", Certification: "pending"},
```

## internal/app/discovery.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/discovery.go:49:			if a.plexGUID == nil {
internal/app/discovery.go:52:			return a.plexGUID.Lookup(ctx, guid)
```

## internal/app/frontend_admin.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/frontend_admin.go:84:	case "plex":
internal/app/frontend_admin.go:86:	case "jellyfin":
internal/app/frontend_admin.go:88:	case "emby":
internal/app/frontend_admin.go:186:			c.Frontends["plex"] = config.Frontend{Enabled: true} // Preserve the default main-listener registration.
internal/app/frontend_admin.go:221:				c.Frontends["plex"] = config.Frontend{Enabled: true} // Preserve the default main-listener registration.
```

## internal/app/frontend_capabilities.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/frontend_capabilities.go:54:	declared.PreserveEmptyVideoCodecs = s.surface == "plex"
```

## internal/app/frontend_experience.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/frontend_experience.go:201:		if protocol == "plex" {
internal/app/frontend_experience.go:538:		a.plexMetaMu.Lock()
internal/app/frontend_experience.go:539:		defer a.plexMetaMu.Unlock()
internal/app/frontend_experience.go:540:		if a.plexGUID == nil {
internal/app/frontend_experience.go:543:		return a.plexGUID
internal/app/frontend_experience.go:546:		a.plexMetaMu.Lock()
internal/app/frontend_experience.go:547:		defer a.plexMetaMu.Unlock()
internal/app/frontend_experience.go:548:		if a.plexMeta == nil {
internal/app/frontend_experience.go:551:		return a.plexMeta
```

## internal/app/frontend_host_policy.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/frontend_host_policy.go:12:	case "plex": // Wire the account-managed frontend's host options.
internal/app/frontend_host_policy.go:14:	case "jellyfin", "emby": // Wire the two native API frontends' host options.
```

## internal/app/frontend_library_presentation.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/frontend_library_presentation.go:7:	case "plex":
internal/app/frontend_library_presentation.go:12:	case "jellyfin", "emby", "silo":
internal/app/frontend_library_presentation.go:40:	case "plex":
internal/app/frontend_library_presentation.go:52:	case "jellyfin", "emby":
```

## internal/app/frontend_listeners.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/frontend_listeners.go:84:			if id != "plex" {
internal/app/frontend_listeners.go:347:	if id == "plex" {
internal/app/frontend_listeners.go:352:		for _, other := range []frontend.Protocol{"plex", "jellyfin", "emby", "silo"} {
internal/app/frontend_listeners.go:461:	if next == nil || m.log == nil || id == "plex" {
internal/app/frontend_listeners.go:481:		if (id == "jellyfin" || id == "emby") && status != 0 && status != http.StatusUnauthorized && (status < 200 || status >= 300) {
```

## internal/app/frontend_portal.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/frontend_portal.go:281:	if id == "plex" {
```

## internal/app/frontend_runtime.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/frontend_runtime.go:41:// plexDiscovery is the injected discovery responder for the main listener.
internal/app/frontend_runtime.go:42:type plexDiscovery interface {
internal/app/frontend_runtime.go:68:	discovery      plexDiscovery
internal/app/frontend_runtime.go:69:	newDiscovery   func() plexDiscovery
internal/app/frontend_runtime.go:100:	r := &frontendRuntime{app: a, ctx: ctx, cancel: cancel, events: a.sessionEvents, discovery: a.gdm, newDiscovery: func() plexDiscovery { return a.newGDMResponder() }}
internal/app/frontend_runtime.go:128:	for _, protocol := range []frontend.Protocol{"plex", "jellyfin", "emby", "silo"} {
internal/app/frontend_runtime.go:233:			Platform:     plexPlatform(),
internal/app/frontend_runtime.go:432:	if cfg.Frontends == nil || cfg.Frontends["plex"].Enabled {
internal/app/frontend_runtime.go:433:		if err := r.control.listeners.start(ctx, "plex"); err != nil {
internal/app/frontend_runtime.go:441:			if err := receiver.BindMediaBrowserDelivery(r.plexHandler()); err != nil {
internal/app/frontend_runtime.go:448:			if err := receiver.BindPlexDelivery(r.plexHandler()); err != nil {
internal/app/frontend_runtime.go:477:// plexHandler wires the main protocol mount into the public dispatcher.
internal/app/frontend_runtime.go:478:func (r *frontendRuntime) plexHandler() http.Handler {
internal/app/frontend_runtime.go:487:		inst, ok := r.registry.Instance("plex")
internal/app/frontend_runtime.go:492:		r.control.listeners.observe("plex", inst.Handler()).ServeHTTP(w, req)
internal/app/frontend_runtime.go:504:		*status = r.plexStatus(context.Background())
internal/app/frontend_runtime.go:531:// plexStatus wires account publication and main-listener reachability into status.
internal/app/frontend_runtime.go:532:func (r *frontendRuntime) plexStatus(ctx context.Context) frontend.Status {
internal/app/frontend_runtime.go:571:		st.LiveStreams, st.LiveStreamsKnown = snapshot.counts["plex"], snapshot.known
internal/app/frontend_runtime.go:573:		st.LiveStreams = countFrontendStreams((ownerPlayback{Playback: r.app.playback, app: r.app}).Sessions())["plex"]
internal/app/frontend_runtime.go:586:			protocol = "plex"
internal/app/frontend_runtime.go:604:		desired := cfg.Frontends == nil || cfg.Frontends["plex"].Enabled
internal/app/frontend_runtime.go:605:		_, mounted := r.registry.Instance("plex")
internal/app/frontend_runtime.go:607:			_ = r.control.listeners.start(r.ctx, "plex")
internal/app/frontend_runtime.go:610:			_ = r.control.listeners.stop(r.ctx, "plex")
internal/app/frontend_runtime.go:641:	_, enabled := r.registry.Instance("plex")
internal/app/frontend_runtime.go:710:	inst, ok := r.registry.Instance("plex")
internal/app/frontend_runtime.go:757:		if inst, ok := r.registry.Instance("plex"); ok {
```

## internal/app/maintenance.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/maintenance.go:49:	add("plexmeta", a.plexMeta != nil, func(ctx context.Context) (int64, error) { return a.plexMeta.Prune(ctx) })
internal/app/maintenance.go:71:	add("plex", a.frontends != nil, func(ctx context.Context) (int64, error) { return a.frontends.prune(ctx, "plex") })
```

## internal/app/nodefacts.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/nodefacts.go:53:		} `json:"plex"`
```

## internal/app/onair.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/onair.go:227:	if path != plexmeta.RemoteArt || a.plexMeta == nil || a.plexGUID == nil {
internal/app/onair.go:230:	row, ok := a.plexMeta.Cached(ctx, key)
internal/app/onair.go:231:	if !ok || row.Empty() || row.GUID == "" || row.GUID != a.plexGUID.Cached(ctx, key) {
```

## internal/app/personal_home_store.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/personal_home_store.go:70:	for _, id := range []frontend.Protocol{"jellyfin", "emby"} {
```

## internal/app/plex.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/plex.go:28:	a.plexMetaMu.Lock()
internal/app/plex.go:29:	if a.plexGUID != nil {
internal/app/plex.go:30:		a.plexMetaMu.Unlock()
internal/app/plex.go:37:	plexToken := func() string {
internal/app/plex.go:39:		if !cfg.Plex.Claimed || (cfg.Frontends != nil && !cfg.Frontends["plex"].Enabled) {
internal/app/plex.go:46:		Token:            plexToken,
internal/app/plex.go:58:		Token:            plexToken,
internal/app/plex.go:68:			return cfg.Plex.Claimed && cfg.Plex.UsePlexMetadata && (cfg.Frontends == nil || cfg.Frontends["plex"].Enabled)
internal/app/plex.go:72:	a.plexGUID = guidService
internal/app/plex.go:73:	a.plexMeta = metadataService
internal/app/plex.go:75:		a.plexGUID = nil
internal/app/plex.go:76:		a.plexMeta = nil
internal/app/plex.go:77:		a.plexMetaMu.Unlock()
internal/app/plex.go:82:	a.plexMetaClose = func() {
internal/app/plex.go:86:	a.plexMetaMu.Unlock()
internal/app/plex.go:94:	a.plexMetaMu.Lock()
internal/app/plex.go:95:	fn := a.plexMetaClose
internal/app/plex.go:96:	a.plexMetaClose = nil
internal/app/plex.go:97:	a.plexMetaMu.Unlock()
internal/app/plex.go:112:func plexPlatform() string {
```

## internal/app/plexwatch.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/plexwatch.go:20:	if a.plexWatch != nil || a.db == nil || a.state == nil || a.plexGUID == nil || a.cfg == nil {
internal/app/plexwatch.go:27:		GUID:     a.plexGUID,
internal/app/plexwatch.go:57:	a.plexWatch = svc
internal/app/plexwatch.go:75:// plexWatchAdmin adapts the service to the admin interface and maps
internal/app/plexwatch.go:77:type plexWatchAdmin struct{ svc *plexwatch.Service }
internal/app/plexwatch.go:79:func (p plexWatchAdmin) status(st plexwatch.Status) admin.PlexWatchStatus {
internal/app/plexwatch.go:108:func (p plexWatchAdmin) Status(ctx context.Context, userID, plexID int64) (admin.PlexWatchStatus, error) {
internal/app/plexwatch.go:109:	st, err := p.svc.Status(ctx, userID, plexID)
internal/app/plexwatch.go:112:func (p plexWatchAdmin) BeginPIN(ctx context.Context, userID, plexID int64) (admin.PlexWatchStatus, error) {
internal/app/plexwatch.go:113:	st, err := p.svc.BeginPIN(ctx, userID, plexID)
internal/app/plexwatch.go:116:func (p plexWatchAdmin) FinishPIN(ctx context.Context, userID, plexID int64) (admin.PlexWatchStatus, error) {
internal/app/plexwatch.go:117:	st, err := p.svc.FinishPIN(ctx, userID, plexID)
internal/app/plexwatch.go:120:func (p plexWatchAdmin) SetDirection(ctx context.Context, userID, plexID int64, direction, interval int) (admin.PlexWatchStatus, error) {
internal/app/plexwatch.go:121:	st, err := p.svc.SetDirection(ctx, userID, plexID, direction, interval)
internal/app/plexwatch.go:124:func (p plexWatchAdmin) Disconnect(ctx context.Context, userID, plexID int64) (admin.PlexWatchStatus, error) {
internal/app/plexwatch.go:125:	st, err := p.svc.Disconnect(ctx, userID, plexID)
internal/app/plexwatch.go:128:func (p plexWatchAdmin) Retry(ctx context.Context, userID, plexID int64) (admin.PlexWatchStatus, error) {
internal/app/plexwatch.go:129:	st, err := p.svc.Retry(ctx, userID, plexID)
internal/app/plexwatch.go:132:func (p plexWatchAdmin) ExportCurrent(ctx context.Context, userID, plexID int64) (admin.PlexWatchStatus, error) {
internal/app/plexwatch.go:133:	st, err := p.svc.ExportCurrent(ctx, userID, plexID)
internal/app/plexwatch.go:136:func (p plexWatchAdmin) ImportCurrent(ctx context.Context, userID, plexID int64) (admin.PlexWatchStatus, error) {
internal/app/plexwatch.go:137:	st, err := p.svc.ImportCurrent(ctx, userID, plexID)
internal/app/plexwatch.go:146:	if a.plexServers != nil || a.db == nil || a.state == nil || a.cfg == nil {
internal/app/plexwatch.go:172:	a.plexServers = svc
internal/app/plexwatch.go:181:	g.a.plexMetaMu.Lock()
internal/app/plexwatch.go:182:	defer g.a.plexMetaMu.Unlock()
internal/app/plexwatch.go:183:	return g.a.plexGUID
internal/app/plexwatch.go:200:func (a *App) plexServersAPI() admin.PlexServers {
internal/app/plexwatch.go:201:	if a.plexServers == nil {
internal/app/plexwatch.go:204:	return a.plexServers
```

## internal/app/quality_explain.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/quality_explain.go:44:		{ID: "jellyfin", Name: "Jellyfin", Product: "Jellyfin", Protocol: "jellyfin"},
internal/app/quality_explain.go:45:		{ID: "emby", Name: "Emby", Product: "Emby", Protocol: "emby"},
internal/app/quality_explain.go:129:	if client.Protocol == "jellyfin" || client.Protocol == "emby" {
```

## internal/app/recovery.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/recovery.go:70:	// plexFor builds the plex.tv sign-in for the settings (a test seam).
internal/app/recovery.go:71:	plexFor func(nodeFacts) recoveryPlex
internal/app/recovery.go:124:		plexFor: func(f nodeFacts) recoveryPlex {
internal/app/recovery.go:500:		"plex":      f.OwnerPlexID != 0 && rc.plexFor(f) != nil,
internal/app/recovery.go:586:	plex := rc.plexFor(f)
internal/app/recovery.go:630:	plex := rc.plexFor(f)
internal/app/recovery.go:652:	rc.log.Info("recovery: owner signed in on the startup page", "method", "plex", "account", strconv.FormatInt(acct.ID, 10))
```

## internal/app/scorecard_serve.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/scorecard_serve.go:179:		c.Frontends = map[string]config.Frontend{"plex": {Enabled: false}} // Wire the isolated scorecard frontend selection.
internal/app/scorecard_serve.go:377:	protocols := []core.Protocol{"jellyfin", "emby", "silo", "webdav", "stremio", "dlna", "m3u"}
internal/app/scorecard_serve.go:379:		protocols = append([]core.Protocol{"plex"}, protocols...)
```

## internal/app/session_broker.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/session_broker.go:57:		Logger:         a.log.With("component", "plex"),
```

## internal/app/session_live.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/session_live.go:191:	if caps.Protocol == "jellyfin" || caps.Protocol == "emby" {
```

## internal/app/session_policy.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/session_policy.go:13:	case "plex":
internal/app/session_policy.go:15:	case "jellyfin", "emby":
```

## internal/app/specialist_public.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/specialist_public.go:126:	if a.plexWatch != nil {
internal/app/specialist_public.go:127:		g.start(func() { a.plexWatch.Run(ctx) })
internal/app/specialist_public.go:128:		g.start(func() { a.cluster.RunAsLeader(ctx, "plex watch imports", a.plexWatch.RunImports) })
```

## internal/app/specialist_server.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/specialist_server.go:69:	mux.Handle("/", a.frontends.plexHandler())
```

## internal/app/step_aside.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/step_aside.go:15:	if a == nil || protocol != "plex" {
internal/app/step_aside.go:24:			if a.plexServers == nil {
internal/app/step_aside.go:27:			snap := a.plexServers.Held()
```

## internal/app/streams.go

Application wiring, account integrations, listener management and client catalogue. Some protocol decisions remain in session_live, streams, quality_explain and frontend_library_presentation; these require further extraction.

```text
internal/app/streams.go:159:	if surface != "" && surface != "plex" {
```

## internal/artwork/service/palette.go

Historical migration/table identity only.

```text
internal/artwork/service/palette.go:27:	store.Register("plex", 1, `CREATE TABLE plex_ultrablur (
```

## internal/auth/auth.go

Plex account linking, sharing and defaults integration; account-source behavior, not requesting-frontend dispatch.

```text
internal/auth/auth.go:359:	plexDefaults map[int64]PlexViewerDefaults
internal/auth/auth.go:484:	var isOwner, home, restricted, active, ownerEnabled, plexShared int
internal/auth/auth.go:485:	var plexSecs, subs, auds, secs string
internal/auth/auth.go:489:		&restricted, &active, &r.Origin, &plexSecs, &r.StreamLimit, &r.MaxContentRating, &subs, &auds, &secs, &lastSeen, &created, &updated, &ownerEnabled, &plexShared, &rawPlexID); err != nil {
internal/auth/auth.go:493:	r.OwnerEnabled, r.PlexShared, r.HasPlex = ownerEnabled == 1, plexShared == 1, rawPlexID.Valid
internal/auth/auth.go:494:	_ = json.Unmarshal([]byte(plexSecs), &r.PlexSections)
internal/auth/auth.go:638:	changed := !plexDefaultsEqual(s.plexDefaults, defaults)
internal/auth/auth.go:639:	s.users, s.byPlex, s.tokens, s.ownerPlexID, s.plexDefaults = users, byPlex, tokens, owner, defaults
internal/auth/auth.go:906:			s.log.Info("token without access to this server", "plexId", acct.ID, "username", acct.Username)
internal/auth/auth.go:1304:			plexID := f.PlexID
internal/auth/auth.go:1306:				if err := tx.QueryRowContext(ctx, `SELECT COALESCE(plex_id,0) FROM auth_users WHERE id=?`, uid).Scan(&plexID); err != nil {
internal/auth/auth.go:1310:			if err := upsertPlexDefaultsTx(ctx, tx, uid, plexID, now, defaultsFromFriend(f)); err != nil {
internal/auth/auth.go:1311:				return fmt.Errorf("auth: save plex defaults %d: %w", plexID, err)
internal/auth/auth.go:1437:	PlexSections []int     `json:"plexSections"`
internal/auth/auth.go:1443:	PlexShared   bool `json:"plexShared"`
internal/auth/auth.go:1502:// plexShareEcho reports sections that are only the plex.tv share effective()
internal/auth/auth.go:1504:func (s *Service) plexShareEcho(id int64, secs []core.LibraryID) bool {
internal/auth/auth.go:1545:	if s.plexShareEcho(u.ID, secs) {
```

## internal/auth/lifecycle.go

Plex account linking, sharing and defaults integration; account-source behavior, not requesting-frontend dispatch.

```text
internal/auth/lifecycle.go:22:// plexManaged reports whether r is a Plex viewer managed on plex.tv.
internal/auth/lifecycle.go:23:func (r *row) plexManaged() bool { return !r.IsOwner && r.HasPlex }
internal/auth/lifecycle.go:41:	if r.plexManaged() {
```

## internal/auth/plex_defaults.go

Plex account linking, sharing and defaults integration; account-source behavior, not requesting-frontend dispatch.

```text
internal/auth/plex_defaults.go:97:func upsertPlexDefaultsTx(ctx context.Context, tx *sql.Tx, userID, plexID, now int64, d PlexViewerDefaults) error {
internal/auth/plex_defaults.go:116:	if haveID == plexID && raw == string(body) {
internal/auth/plex_defaults.go:121:		userID, plexID, string(body), now)
internal/auth/plex_defaults.go:154:	d, ok := s.plexDefaults[userID]
internal/auth/plex_defaults.go:177:			"plexSubtitleMode":      d.SubtitleMode,
internal/auth/plex_defaults.go:191:func plexDefaultsEqual(a, b map[int64]PlexViewerDefaults) bool {
```

## internal/auth/plex_link.go

Plex account linking, sharing and defaults integration; account-source behavior, not requesting-frontend dispatch.

```text
internal/auth/plex_link.go:20:func upsertPlexLink(ctx context.Context, tx *sql.Tx, plexID int64, display string, now int64) error {
internal/auth/plex_link.go:24: WHERE identity_links.viewer_id=excluded.viewer_id`, strconv.FormatInt(plexID, 10), display, now, plexID)
```

## internal/config/config.go

Configuration schema and protocol identities.

```text
internal/config/config.go:34:	Plex        Plex      `json:"plex"`
internal/config/config.go:51:	PlexServers   PlexServers   `json:"plexServers"`
internal/config/config.go:958:	if plex, ok := file["plex"].(map[string]any); ok {
internal/config/config.go:1011:	if server, ok := file["plex"].(map[string]any); ok {
```

## internal/config/schema/builtins.go

Configuration schema and protocol identities.

```text
internal/config/schema/builtins.go:35:		{ID: "plex", ConfigKey: "plex", Label: extend.T("Remote access"), Schema: Schema{Groups: []Group{
```

## internal/config/scope.go

Configuration schema and protocol identities.

```text
internal/config/scope.go:43:	"plexServers":    Shared,
internal/config/scope.go:83:	"plex": {
```

## internal/conform/capture/decision.go

Reference capture and protocol-specific conformance tooling, not shared request runtime.

```text
internal/conform/capture/decision.go:15:// plexClientHeaders is the order official Plex apps send identity headers.
internal/conform/capture/decision.go:16:var plexClientHeaders = []string{
internal/conform/capture/decision.go:66:		if entry.Protocol != "" && entry.Protocol != "plex" {
internal/conform/capture/decision.go:108:		out = filepath.Join("testdata", "conform", "plex", "decisions", "corpus.json")
internal/conform/capture/decision.go:123:	for _, k := range plexClientHeaders {
```

## internal/conform/capture/fixtures.go

Reference capture and protocol-specific conformance tooling, not shared request runtime.

```text
internal/conform/capture/fixtures.go:24:	if protocol != "jellyfin" && protocol != "emby" && protocol != "silo" {
```

## internal/conform/capture/profile.go

Reference capture and protocol-specific conformance tooling, not shared request runtime.

```text
internal/conform/capture/profile.go:41:	case "jellyfin", "emby", "silo":
```

## internal/conform/capture/proxy.go

Reference capture and protocol-specific conformance tooling, not shared request runtime.

```text
internal/conform/capture/proxy.go:75:	case "jellyfin", "emby", "silo", "plex":
internal/conform/capture/proxy.go:309:	if r.protocol == "plex" {
internal/conform/capture/proxy.go:373:			value = plexLibraryPath(value)
internal/conform/capture/proxy.go:374:		case plexIdentityPlaceholder(name) != "":
internal/conform/capture/proxy.go:375:			value = plexIdentityPlaceholder(name)
internal/conform/capture/proxy.go:389:func plexIdentityPlaceholder(name string) string {
internal/conform/capture/proxy.go:405:func plexLibraryPath(v string) string {
```

## internal/conform/mediabrowser/protocol.go

Reference capture and protocol-specific conformance tooling, not shared request runtime.

```text
internal/conform/mediabrowser/protocol.go:63:	if dialect != "jellyfin" && dialect != "emby" && dialect != "silo" {
internal/conform/mediabrowser/protocol.go:180:	if p.Dialect == "jellyfin" && p.profiles[profile].isCaptured() {
internal/conform/mediabrowser/protocol.go:199:	if p.Dialect == "jellyfin" {
internal/conform/mediabrowser/protocol.go:263:	if p.Dialect == "jellyfin" && p.profiles[profile].isCaptured() && strings.HasPrefix(r.Name, "captured_") {
internal/conform/mediabrowser/protocol.go:337:	if p.Dialect == "jellyfin" && token != "" {
internal/conform/mediabrowser/protocol.go:349:		} else if p.Dialect == "jellyfin" {
internal/conform/mediabrowser/protocol.go:418:			if p.Dialect != "emby" || r.RangeKind != "suffix" || resp.StatusCode != http.StatusPartialContent || len(body) != 65537 || resp.Header.Get("Content-Length") != "65537" || parseErr != nil || first != 0 || last != 65536 || total <= 65537 {
internal/conform/mediabrowser/protocol.go:457:	if p.Dialect == "emby" && emptyEmbyReferenceAddresses(a, b) {
internal/conform/mediabrowser/protocol.go:505:				if p.Dialect == "jellyfin" {
internal/conform/mediabrowser/protocol.go:521:	if p.Dialect == "jellyfin" {
internal/conform/mediabrowser/protocol.go:541:	if p.Dialect == "emby" {
internal/conform/mediabrowser/protocol.go:556:	if len(parts) == 4 && (strings.EqualFold(parts[0], "jellyfin") || strings.EqualFold(parts[0], "emby")) {
internal/conform/mediabrowser/protocol.go:581:	if len(parts) > 0 && (strings.EqualFold(parts[0], "jellyfin") || strings.EqualFold(parts[0], "emby")) {
```

## internal/conform/openapi/cache.go

Reference capture and protocol-specific conformance tooling, not shared request runtime.

```text
internal/conform/openapi/cache.go:36:	names := map[string]string{"jellyfin": "jellyfin-openapi-12.4.json", "emby": "emby-openapi-v2.json", "silo": "silo-openapi.json"}
internal/conform/openapi/cache.go:52:	if protocol == "jellyfin" && version != "" {
internal/conform/openapi/cache.go:65:		if protocol == "jellyfin" && version != "" {
```

## internal/conform/plex/protocol.go

Reference capture and protocol-specific conformance tooling, not shared request runtime.

```text
internal/conform/plex/protocol.go:49:		r.Differences[i].Blocking = plexBlocking(r.Differences[i])
internal/conform/plex/protocol.go:54:func plexBlocking(d conform.Difference) bool {
```

## internal/core/live_app.go

Public contract and context marker names; part/rating markers still expose Plex vocabulary.

```text
internal/core/live_app.go:7:// WithLiveApp names the client app (frontend surface id such as "plex" or
internal/core/live_app.go:8:// "jellyfin") a Live TV request is served to, so the owner's per-app channel
```

## internal/core/personalize_owner.go

Public contract and context marker names; part/rating markers still expose Plex vocabulary.

```text
internal/core/personalize_owner.go:76:	RowSourcePlex   = "plex"   // built by the Plex front end (Continue Watching, playlists)
internal/core/personalize_owner.go:140:	// "household", "collections" or "plex".
```

## internal/core/plex_part.go

Public contract and context marker names; part/rating markers still expose Plex vocabulary.

```text
internal/core/plex_part.go:5:type plexPartKey struct{}
internal/core/plex_part.go:11:	return context.WithValue(ctx, plexPartKey{}, true)
internal/core/plex_part.go:14:// IsPlexPart reports whether ctx carries the Plex part marker.
internal/core/plex_part.go:15:func IsPlexPart(ctx context.Context) bool {
internal/core/plex_part.go:16:	v, _ := ctx.Value(plexPartKey{}).(bool)
```

## internal/core/safer_rating.go

Public contract and context marker names; part/rating markers still expose Plex vocabulary.

```text
internal/core/safer_rating.go:5:type plexSaferRatingKey struct{}
internal/core/safer_rating.go:13:	return context.WithValue(ctx, plexSaferRatingKey{}, true)
internal/core/safer_rating.go:22:	v, _ := ctx.Value(plexSaferRatingKey{}).(bool)
```

## internal/core/types.go

Public contract and context marker names; part/rating markers still expose Plex vocabulary.

```text
internal/core/types.go:194:	PlexID    int64  `json:"plexId"`
```

## internal/frontend/defaults.go

Frontend registry defaults and protocol-specific admission/serve context contracts; the context contracts still need neutralization.

```text
internal/frontend/defaults.go:7:	case "plex":
internal/frontend/defaults.go:9:	case "jellyfin":
internal/frontend/defaults.go:11:	case "emby":
```

## internal/frontend/host_sessions_experience.go

Frontend registry defaults and protocol-specific admission/serve context contracts; the context contracts still need neutralization.

```text
internal/frontend/host_sessions_experience.go:1114:type plexAdmissionKey struct{}
internal/frontend/host_sessions_experience.go:1121:	return context.WithValue(ctx, plexAdmissionKey{}, admit)
internal/frontend/host_sessions_experience.go:1126:	admit, ok := ctx.Value(plexAdmissionKey{}).(func(context.Context) (PlanAdmission, error))
internal/frontend/host_sessions_experience.go:1130:type plexServe struct {
internal/frontend/host_sessions_experience.go:1135:type plexServeKey struct{}
internal/frontend/host_sessions_experience.go:1143:	return context.WithValue(ctx, plexServeKey{}, plexServe{remote: remote, format: format})
internal/frontend/host_sessions_experience.go:1151:	v, _ := ctx.Value(plexServeKey{}).(plexServe)
```

## internal/identity/image_grants.go

Account identity/linking plus remaining runtime protocol decisions in resolver and image grants; extraction remains.

```text
internal/identity/image_grants.go:101:	if db == nil || surface == "" || surface == "plex" || grants == nil || experience == nil || images == nil || ttl < time.Second {
```

## internal/identity/image_page_grant.go

Account identity/linking plus remaining runtime protocol decisions in resolver and image grants; extraction remains.

```text
internal/identity/image_page_grant.go:606:	if (s.surface == "jellyfin" || s.surface == "emby") && authhdr.MediaBrowserToken(r) == "" {
```

## internal/identity/management.go

Account identity/linking plus remaining runtime protocol decisions in resolver and image grants; extraction remains.

```text
internal/identity/management.go:72:func (l *Local) LinkPlex(ctx context.Context, viewerID, plexID int64, display string) error {
internal/identity/management.go:73:	if viewerID <= 0 || plexID <= 0 {
internal/identity/management.go:76:	subject := strconv.FormatInt(plexID, 10)
```

## internal/identity/provider.go

Account identity/linking plus remaining runtime protocol decisions in resolver and image grants; extraction remains.

```text
internal/identity/provider.go:44:func (PlexProvider) Name() string { return "plex" }
```

## internal/identity/resolver.go

Account identity/linking plus remaining runtime protocol decisions in resolver and image grants; extraction remains.

```text
internal/identity/resolver.go:29:	if surface == "plex" {
```

## internal/identity/viewer_times.go

Account identity/linking plus remaining runtime protocol decisions in resolver and image grants; extraction remains.

```text
internal/identity/viewer_times.go:147:	var login, activity, plexSeen int64
internal/identity/viewer_times.go:150: WHERE u.id=? AND u.active=1 AND EXISTS (SELECT 1 FROM auth_users a WHERE a.id=? AND a.active=1 AND (a.id=u.id OR a.is_owner=1))`, viewerID, actorID).Scan(&login, &activity, &plexSeen)
internal/identity/viewer_times.go:157:	if plexSeen > 0 && plexSeen <= (1<<63-1)/1000 && plexSeen*1000 > activity {
internal/identity/viewer_times.go:158:		activity = plexSeen * 1000
```

## internal/livetv/provider_guide.go

Persisted provider kind/cursor, publication identities and historical publication migration; no requesting-frontend branch.

```text
internal/livetv/provider_guide.go:17:const providerGuideKindID = "plex"
```

## internal/livetv/publication.go

Persisted provider kind/cursor, publication identities and historical publication migration; no requesting-frontend branch.

```text
internal/livetv/publication.go:18:var Apps = []string{"plex", "jellyfin", "emby", "silo", "xtream", "m3u"}
internal/livetv/publication.go:42:// livetv/17 converts the Plex-only publication flag (plexHidden, never
internal/livetv/publication.go:50:	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM livetv_channels WHERE data LIKE '%"plexHidden"%'`)
internal/livetv/publication.go:72:		_ = json.Unmarshal(data["plexHidden"], &hidden)
internal/livetv/publication.go:73:		delete(data, "plexHidden")
internal/livetv/publication.go:78:			if !slices.Contains(apps, "plex") {
internal/livetv/publication.go:79:				apps = append(apps, "plex")
```

## internal/livetv/types.go

Persisted provider kind/cursor, publication identities and historical publication migration; no requesting-frontend branch.

```text
internal/livetv/types.go:228:	PlexCursor  int        `json:"plexCursor,omitempty"`
```

## internal/migrate/migrate.go

Imported account identity field.

```text
internal/migrate/migrate.go:93:	PlexID          int64  `json:"plexId"`
```

## internal/perfharness/scenarios.go

Protocol-specific load scenarios and wire clients, not shared request runtime.

```text
internal/perfharness/scenarios.go:120:	return []Client{plexClient("plex-appletv", "Plex for Apple TV", "tvOS", "Apple TV"), plexSecondDevice(), plexClient("plex-android", "Plex for Android (TV)", "Android", "BRAVIA 4K VH21"), plexClient("plex-web", "Plex Web", "Chrome", "Windows"), plexPlayback(),
internal/perfharness/scenarios.go:124:func plexHeaders(product, platform, device string) func(*session) http.Header {
internal/perfharness/scenarios.go:125:	return plexDeviceHeaders(product, platform, device, "")
internal/perfharness/scenarios.go:128:// plexDeviceHeaders are plexHeaders for one more device of the same kind:
internal/perfharness/scenarios.go:130:func plexDeviceHeaders(product, platform, device, suffix string) func(*session) http.Header {
internal/perfharness/scenarios.go:151:// plexSecondDevice is a viewer's second Apple TV, first seen right after
internal/perfharness/scenarios.go:154:func plexSecondDevice() Client {
internal/perfharness/scenarios.go:155:	return Client{Name: "plex-appletv second device", Surface: "plex", Headers: plexDeviceHeaders("Plex for Apple TV", "tvOS", "Apple TV", "-second"), Steps: []Step{
internal/perfharness/scenarios.go:163:func plexClient(name, product, platform, device string) Client {
internal/perfharness/scenarios.go:165:	return Client{Name: name, Surface: "plex", Headers: plexHeaders(product, platform, device), Steps: append([]Step{
internal/perfharness/scenarios.go:208:	}, plexLibrarySteps(page)...)}
internal/perfharness/scenarios.go:364:	return Client{Name: "infuse-jellyfin", Surface: "jellyfin", Port: 1, SharedPrefix: "/jellyfin", Headers: mediaBrowserHeaders("Infuse-Library", "8.1", "Apple TV", false), SignIn: mediaBrowserSignIn(), Steps: append([]Step{
internal/perfharness/scenarios.go:395:	return Client{Name: "swiftfin", Surface: "jellyfin", Port: 1, SharedPrefix: "/jellyfin", Headers: mediaBrowserHeaders("Swiftfin tvOS", "1.3", "Apple TV", false), SignIn: mediaBrowserSignIn(), Steps: []Step{
internal/perfharness/scenarios.go:407:	return Client{Name: "emby-sdk", Surface: "emby", Port: 2, SharedPrefix: "/emby", Headers: mediaBrowserHeaders("Emby Theater", "4.8", "etherperf", true), SignIn: mediaBrowserSignIn(), Steps: append([]Step{
internal/perfharness/scenarios.go:435:	return Client{Name: "moonfin", Surface: "jellyfin", Port: 1, SharedPrefix: "/jellyfin", Headers: mediaBrowserHeaders("Moonfin", "1.0", "Android TV", false), SignIn: mediaBrowserSignIn(), Steps: []Step{
internal/perfharness/scenarios.go:455:	return Client{Name: "infuse-emby", Surface: "emby", Port: 2, SharedPrefix: "/emby", Headers: mediaBrowserHeaders("Infuse-Library", "8.1", "Apple TV", true), SignIn: mediaBrowserSignIn(), Steps: []Step{
```

## internal/perfharness/scenarios_plex.go

Protocol-specific load scenarios and wire clients, not shared request runtime.

```text
internal/perfharness/scenarios_plex.go:18:// plexLibrarySteps are the Plex paths a viewer reaches past the first page
internal/perfharness/scenarios_plex.go:22:func plexLibrarySteps(page string) []Step {
internal/perfharness/scenarios_plex.go:75:// plexPlayback is Plex for Apple TV watching: it builds play queues, reports
internal/perfharness/scenarios_plex.go:80:func plexPlayback() Client {
internal/perfharness/scenarios_plex.go:89:	return Client{Name: "plex-appletv playback", Surface: "plex", Headers: plexDeviceHeaders("Plex for Apple TV", "tvOS", "Apple TV", "-player"), Steps: []Step{
internal/perfharness/scenarios_plex.go:114:func plexMetadata0(body []byte) map[string]json.RawMessage {
internal/perfharness/scenarios_plex.go:126:type plexTagJSON struct {
internal/perfharness/scenarios_plex.go:136:	m := plexMetadata0(body)
internal/perfharness/scenarios_plex.go:140:	first := func(field string) (plexTagJSON, bool) {
internal/perfharness/scenarios_plex.go:141:		var tags []plexTagJSON
internal/perfharness/scenarios_plex.go:143:			return plexTagJSON{}, false
```

## internal/personalize/owner.go

Historical row provenance/group identity and row builder names; names still expose Plex vocabulary.

```text
internal/personalize/owner.go:28:// marked source "plex". Rows hidden by a layout policy or absent despite an
internal/personalize/owner.go:71:			p.Rows = append([]core.HomeRow{plexRow("home.continue", "Continue Watching", KindContinue,
internal/personalize/owner.go:89:		head := []core.HomeRow{plexRow(prefix+".inprogress."+sid, "Continue Watching", KindContinue,
internal/personalize/owner.go:92:		head = append(head, plexRow(prefix+".recentlyadded."+sid, "Recently Added", KindRecentlyAdded,
internal/personalize/owner.go:463:func plexRow(hubID, title, kind, reason string, items []candItem) core.HomeRow {
internal/personalize/owner.go:501:	r := plexRow("home.playlists", "Recent Playlists", KindPlaylists,
```

## internal/personalize/personal.go

Historical row provenance/group identity and row builder names; names still expose Plex vocabulary.

```text
internal/personalize/personal.go:47:		rows = append(rows, plexRow(r.id, r.title, r.kind, r.reason, r.items))
```

## internal/personalize/row_behavior.go

Historical row provenance/group identity and row builder names; names still expose Plex vocabulary.

```text
internal/personalize/row_behavior.go:6:// a legacy row. Historical "plex" provenance is retained as an alias.
```

## internal/personalize/surfaces.go

Historical row provenance/group identity and row builder names; names still expose Plex vocabulary.

```text
internal/personalize/surfaces.go:31:	groupPlex        = "plex"
```

## internal/playback/cluster.go

Remaining runtime protocol decisions: buffering reports, selection/cluster surface defaults, and Plex part MIME/headers. Extraction remains.

```text
internal/playback/cluster.go:209:	if surface == "" || surface == "plex" {
```

## internal/playback/experience.go

Remaining runtime protocol decisions: buffering reports, selection/cluster surface defaults, and Plex part MIME/headers. Extraction remains.

```text
internal/playback/experience.go:52:func reportsBuffering(p core.Protocol) bool { return p == "" || p == "plex" }
internal/playback/experience.go:58:	case "", "plex", "jellyfin", "emby", "silo":
```

## internal/playback/kind.go

Remaining runtime protocol decisions: buffering reports, selection/cluster surface defaults, and Plex part MIME/headers. Extraction remains.

```text
internal/playback/kind.go:84:	for _, surface := range []string{"", playbackSurface("plex"), playbackSurface("jellyfin"), playbackSurface("emby")} {
```

## internal/playback/payload_ownership.go

Remaining runtime protocol decisions: buffering reports, selection/cluster surface defaults, and Plex part MIME/headers. Extraction remains.

```text
internal/playback/payload_ownership.go:309:		surface = "plex"
```

## internal/playback/plex_part.go

Remaining runtime protocol decisions: buffering reports, selection/cluster surface defaults, and Plex part MIME/headers. Extraction remains.

```text
internal/playback/plex_part.go:19:func plexPartMIME(name string) string {
```

## internal/playback/serve.go

Remaining runtime protocol decisions: buffering reports, selection/cluster surface defaults, and Plex part MIME/headers. Extraction remains.

```text
internal/playback/serve.go:64:	plexPart := core.IsPlexPart(r.Context())
internal/playback/serve.go:65:	if plexPart {
internal/playback/serve.go:66:		rep.contentType = plexPartMIME(s.name)
internal/playback/serve.go:79:	if plexPart {
```

## internal/plexguid/clientid.go

Explicit Plex GUID integration.

```text
internal/plexguid/clientid.go:13:func ClientIdentifier(plexClientID, machineID string) string {
internal/plexguid/clientid.go:14:	if id := strings.TrimSpace(plexClientID); id != "" {
```

## internal/plexmeta/overlay.go

Explicit Plex metadata integration.

```text
internal/plexmeta/overlay.go:111:const RemoteArt = "plex"
```

## internal/plextv/cert.go

Explicit Plex account/discovery integration.

```text
internal/plextv/cert.go:63:var plexDirectRe = regexp.MustCompile(`(?i)\*\.([0-9a-z]+)\.plex\.direct`)
internal/plextv/cert.go:112:	m := plexDirectRe.FindStringSubmatch(doc.CommonName)
internal/plextv/cert.go:114:		m = plexDirectRe.FindStringSubmatch(string(b))
```

## internal/plextv/certmanager.go

Explicit Plex account/discovery integration.

```text
internal/plextv/certmanager.go:299:		if mm := plexDirectRe.FindStringSubmatch(n); mm != nil {
```

## internal/plextv/defaults.go

Explicit Plex account/discovery integration.

```text
internal/plextv/defaults.go:33:const plexForcedOnly = 2
internal/plextv/defaults.go:76:		ForcedOnly:       flexIntFromString(n.attr("defaultSubtitleForced")) == plexForcedOnly,
internal/plextv/defaults.go:141:		ForcedOnly:       forced == plexForcedOnly,
```

## internal/plextv/devices.go

Explicit Plex account/discovery integration.

```text
internal/plextv/devices.go:348:	*r = Resource{Name: v.Name, Product: v.Product, ProductVersion: v.ProductVersion, Platform: v.Platform, PlatformVersion: v.PlatformVersion, Device: v.Device, ClientIdentifier: v.ClientIdentifier, CreatedAt: v.CreatedAt, LastSeenAt: v.LastSeenAt, Provides: v.Provides, OwnerID: v.OwnerID, SourceTitle: v.SourceTitle, PublicAddress: v.PublicAddress, AccessToken: v.AccessToken, Owned: plexBool(v.Owned), Home: plexBool(v.Home), Synced: plexBool(v.Synced), Relay: plexBool(v.Relay), Presence: plexBool(v.Presence), HTTPSRequired: plexBool(v.HTTPSRequired), PublicAddressMatches: plexBool(v.PublicAddressMatches), DNSRebindingProtection: plexBool(v.DNSRebindingProtection), NatLoopbackSupported: plexBool(v.NatLoopbackSupported), Connections: v.Connections}
internal/plextv/devices.go:378:	*c = ResourceConnection{Protocol: v.Protocol, Address: v.Address, Port: v.Port, URI: v.URI, Local: plexBool(v.Local), Relay: plexBool(v.Relay), IPv6: plexBool(v.IPv6)}
internal/plextv/devices.go:382:func plexBool(raw json.RawMessage) bool {
```

## internal/plextv/friends.go

Explicit Plex account/discovery integration.

```text
internal/plextv/friends.go:19:	PlexID     int64    `json:"plexId"`
```

## internal/plextv/registrar.go

Explicit Plex account/discovery integration.

```text
internal/plextv/registrar.go:68:	PlexPass            bool                 `json:"plexPass"`                // owner subscription active
internal/plextv/registrar.go:82:	PlexReachable          *bool     `json:"plexReachable,omitempty"`
internal/plextv/registrar.go:83:	PlexReachableCheckedAt time.Time `json:"plexReachableCheckedAt,omitzero"`
internal/plextv/registrar.go:273:		publicationEnabled: plexAppsEnabled(o.Config.Get()),
internal/plextv/registrar.go:324:func plexAppsEnabled(c config.Config) bool {
internal/plextv/registrar.go:325:	return c.Frontends == nil || c.Frontends["plex"].Enabled
internal/plextv/registrar.go:341:		if plexAppsEnabled(r.o.Config.Get()) && cancel == nil {
internal/plextv/registrar.go:346:		} else if !plexAppsEnabled(r.o.Config.Get()) {
internal/plextv/registrar.go:699:	if !claimed || !plexAppsEnabled(cfg) {
internal/plextv/registrar.go:707:		if err := r.PublishNow(ctx); err != nil && ctx.Err() == nil && plexAppsEnabled(r.o.Config.Get()) {
internal/plextv/registrar.go:749:	want := plexAppsEnabled(cfg) && cfg.Plex.RemoteAccess && cfg.Plex.UseUPnP
internal/plextv/registrar.go:830:	if plexAppsEnabled(cfg) && cfg.Plex.RemoteAccess {
internal/plextv/registrar.go:1262:	if !plexAppsEnabled(r.o.Config.Get()) {
internal/plextv/registrar.go:1344:	if !force && !plexAppsEnabled(cfg) {
internal/plextv/registrar.go:1583:	enabled := plexAppsEnabled(c)
```

## internal/plexwatch/client.go

Explicit Plex account watch-state synchronization integration.

```text
internal/plexwatch/client.go:23:	plexOrigin     = "https://plex.tv"
internal/plexwatch/client.go:78:		if c.plex, err = url.Parse(plexOrigin); err != nil {
internal/plexwatch/client.go:319:		rating, _ := plexRatingFromRemote(it.UserRating)
internal/plexwatch/client.go:432:// plexRating maps Ether's 0–10 rating onto Plex's whole 1–10 scale (five
internal/plexwatch/client.go:435:func plexRating(local float64) int {
internal/plexwatch/client.go:449:// plexRatingFromRemote reads Plex's userRating. ok is false for no rating.
internal/plexwatch/client.go:450:func plexRatingFromRemote(v float64) (int, bool) {
internal/plexwatch/client.go:454:	return plexRating(v), true
```

## internal/plexwatch/deliver.go

Explicit Plex account watch-state synchronization integration.

```text
internal/plexwatch/deliver.go:110:	if !ok || acct.revoked || acct.token == "" || acct.direction&1 == 0 || acct.plexID <= 0 {
internal/plexwatch/deliver.go:137:		w.rating = plexRating(row.Rating)
internal/plexwatch/deliver.go:436:func recordPosition(ctx context.Context, q execQuerier, userID int64, key core.RatingKey, offsetMS int64, viewState string, plexWatched *bool, now time.Time) error {
internal/plexwatch/deliver.go:437:	known := plexWatched != nil
internal/plexwatch/deliver.go:439:	if known && *plexWatched {
internal/plexwatch/deliver.go:456:func (s *Service) notePosition(ctx context.Context, row state.SyncRow, offsetMS int64, plexWatched *bool) error {
internal/plexwatch/deliver.go:457:	return recordPosition(ctx, s.db, row.UserID, row.Key, offsetMS, "progress", plexWatched, s.now())
internal/plexwatch/deliver.go:463:// one during the request: it is what Plex holds. plexWatched is Plex's
internal/plexwatch/deliver.go:465:func (s *Service) sendDone(ctx context.Context, row state.SyncRow, attempt string, w wish, plexWatched *bool) error {
internal/plexwatch/deliver.go:493:		err = recordPosition(ctx, tx, row.UserID, row.Key, w.offsetMS, viewState, plexWatched, now)
```

## internal/plexwatch/export.go

Explicit Plex account watch-state synchronization integration.

```text
internal/plexwatch/export.go:23:func (s *Service) ExportCurrent(ctx context.Context, userID, plexID int64) (Status, error) {
internal/plexwatch/export.go:24:	acct, err := s.requireMatch(ctx, userID, plexID)
internal/plexwatch/export.go:66:		if plexRating(rating) > 0 {
internal/plexwatch/export.go:81:	return s.Status(ctx, userID, plexID)
```

## internal/plexwatch/import.go

Explicit Plex account watch-state synchronization integration.

```text
internal/plexwatch/import.go:466:		if !local.ratingAt.After(prev.observedAt) && plexRating(local.rating) != cur.Rating && (cur.Rating > 0 || local.rating > 0) {
```

## internal/plexwatch/schedule.go

Explicit Plex account watch-state synchronization integration.

```text
internal/plexwatch/schedule.go:70:func (s *Service) ImportCurrent(ctx context.Context, userID, plexID int64) (Status, error) {
internal/plexwatch/schedule.go:71:	acct, err := s.requireMatch(ctx, userID, plexID)
internal/plexwatch/schedule.go:85:	userID, plexID := acct.userID, acct.plexID
internal/plexwatch/schedule.go:89:		lease, userID, plexID, acct.tokenGeneration, now.UnixNano(), now.UnixNano())
internal/plexwatch/schedule.go:96:		return s.Status(ctx, userID, plexID)
internal/plexwatch/schedule.go:162:	st, statusErr := s.Status(ctx, userID, plexID)
```

## internal/plexwatch/service.go

Explicit Plex account watch-state synchronization integration.

```text
internal/plexwatch/service.go:286:	PlexID         int64  `json:"plexId,omitempty"`
internal/plexwatch/service.go:311:	plexID          int64
internal/plexwatch/service.go:324:func (s *Service) Status(ctx context.Context, userID, plexID int64) (Status, error) {
internal/plexwatch/service.go:325:	if err := viewerIDs(userID, plexID); err != nil {
internal/plexwatch/service.go:335:		st.PlexID = row.plexID
internal/plexwatch/service.go:354:		st.AccountMatch = row.plexID == plexID
internal/plexwatch/service.go:355:		if row.plexID != plexID {
internal/plexwatch/service.go:379:// BeginPIN starts a sign-in that must be approved as plexID, not as whatever
internal/plexwatch/service.go:381:func (s *Service) BeginPIN(ctx context.Context, userID, plexID int64) (Status, error) {
internal/plexwatch/service.go:382:	if err := viewerIDs(userID, plexID); err != nil {
internal/plexwatch/service.go:399:	return s.Status(ctx, userID, plexID)
internal/plexwatch/service.go:402:// FinishPIN stores the account only when its Plex id equals plexID. The
internal/plexwatch/service.go:406:func (s *Service) FinishPIN(ctx context.Context, userID, plexID int64) (Status, error) {
internal/plexwatch/service.go:407:	if err := viewerIDs(userID, plexID); err != nil {
internal/plexwatch/service.go:422:		return s.Status(ctx, userID, plexID)
internal/plexwatch/service.go:431:	if acct.ID != plexID {
internal/plexwatch/service.go:446:		case oldPlex != plexID:
internal/plexwatch/service.go:464:			userID, plexID, acct.Username, sealed, now, now, gen); err != nil {
internal/plexwatch/service.go:475:	st, err := s.Status(ctx, userID, plexID)
internal/plexwatch/service.go:482:func (s *Service) SetDirection(ctx context.Context, userID, plexID int64, direction, interval int) (Status, error) {
internal/plexwatch/service.go:486:	row, err := s.requireMatch(ctx, userID, plexID)
internal/plexwatch/service.go:516:			userID, plexID, current)
internal/plexwatch/service.go:530:	return s.Status(ctx, userID, plexID)
internal/plexwatch/service.go:535:func (s *Service) Disconnect(ctx context.Context, userID, plexID int64) (Status, error) {
internal/plexwatch/service.go:536:	if err := viewerIDs(userID, plexID); err != nil {
internal/plexwatch/service.go:543:		res, err := tx.ExecContext(ctx, `DELETE FROM plexwatch_account WHERE user_id=? AND plex_id=?`, userID, plexID)
internal/plexwatch/service.go:562:	return s.Status(ctx, userID, plexID)
internal/plexwatch/service.go:567:func (s *Service) Retry(ctx context.Context, userID, plexID int64) (Status, error) {
internal/plexwatch/service.go:568:	row, err := s.requireMatch(ctx, userID, plexID)
internal/plexwatch/service.go:580:	return s.Status(ctx, userID, plexID)
internal/plexwatch/service.go:620:func (s *Service) requireMatch(ctx context.Context, userID, plexID int64) (accountRow, error) {
internal/plexwatch/service.go:621:	if err := viewerIDs(userID, plexID); err != nil {
internal/plexwatch/service.go:631:	if row.plexID != plexID {
internal/plexwatch/service.go:640:func viewerIDs(userID, plexID int64) error {
internal/plexwatch/service.go:641:	if userID <= 0 || plexID <= 0 {
internal/plexwatch/service.go:653:		Scan(&row.plexID, &row.username, &sealed, &row.direction, &revoked, &row.cursor, &row.tokenGeneration, &row.nextAllowed, &row.consent, &row.consentChecked, &row.activeAfter)
```

## internal/plugins/studio/replay_kinds.go

Plugin projection schema and owner studio presentation still include Plex-specific projection vocabulary.

```text
internal/plugins/studio/replay_kinds.go:193:		summary := fmt.Sprintf("Shows as a %s library", plexBaseName(p.Plex.Base))
internal/plugins/studio/replay_kinds.go:197:		out = append(out, KindProjection{Protocol: "plex", Summary: summary})
internal/plugins/studio/replay_kinds.go:221:func plexBaseName(base string) string {
```

## internal/plugins/studio/templates_kinds.go

Plugin projection schema and owner studio presentation still include Plex-specific projection vocabulary.

```text
internal/plugins/studio/templates_kinds.go:48:							"plex":         map[string]any{"base": "show"},
```

## internal/safety/confirm.go

Account sharing integration and historical column names.

```text
internal/safety/confirm.go:113:	var rating, allowed, plexSecs, raw string
internal/safety/confirm.go:114:	err := tx.QueryRowContext(ctx, `SELECT active, is_owner, stream_limit, max_content_rating, allowed_libraries, plex_sections, COALESCE((SELECT policy FROM safety_policies WHERE user_id=auth_users.id), '') FROM auth_users WHERE id=?`, u.ID).Scan(&active, &owner, &streams, &rating, &allowed, &plexSecs, &raw)
internal/safety/confirm.go:137:	_ = json.Unmarshal([]byte(plexSecs), &plex)
```

## internal/safety/policy.go

Account sharing integration and historical column names.

```text
internal/safety/policy.go:58:	plexShare        func(*core.User) PlexShare
```

## internal/safety/share.go

Account sharing integration and historical column names.

```text
internal/safety/share.go:50:	s.plexShare = fn
internal/safety/share.go:66:	fn := s.plexShare
```

## internal/sessions/schema.go

Historical migration identities only.

```text
internal/sessions/schema.go:7:	store.RegisterAfter("sessions", "plex") // Historical migration identity.
internal/sessions/schema.go:10:	store.Register("plex", 2 /* Historical migration identity. */, `
```

## internal/spike/spike.go

Test-server adapter wiring selects protocol policy and binds Plex surface.

```text
internal/spike/spike.go:239:		if protocol == "plex" {
internal/spike/spike.go:263:		Sessions:   sessions.BindSurface(broker, "plex"),
```

## internal/state/plex_timeline.go

Plex timeline state machine remains in shared state; extraction remains.

```text
internal/state/plex_timeline.go:14:type plexStatusKey struct{}
internal/state/plex_timeline.go:21:type plexPlayID struct {
internal/state/plex_timeline.go:26:type plexPlay struct {
internal/state/plex_timeline.go:33:type plexTimelineStore struct {
internal/state/plex_timeline.go:35:	plays map[plexPlayID]plexPlay
internal/state/plex_timeline.go:42:	ctx = context.WithValue(ctx, plexStatusKey{}, status)
internal/state/plex_timeline.go:43:	s.plexTimeline.Lock()
internal/state/plex_timeline.go:44:	defer s.plexTimeline.Unlock()
internal/state/plex_timeline.go:73:	id := plexPlayID{user, origin.ClientID, origin.SessionID, key.Kind() == core.KindPlaylist}
internal/state/plex_timeline.go:74:	if s.plexTimeline.plays == nil {
internal/state/plex_timeline.go:75:		s.plexTimeline.plays = make(map[plexPlayID]plexPlay)
internal/state/plex_timeline.go:77:	play := s.plexTimeline.plays[id]
internal/state/plex_timeline.go:80:		play = plexPlay{key: key, queue: queue}
internal/state/plex_timeline.go:128:	s.plexTimeline.plays[id] = play
internal/state/plex_timeline.go:129:	if len(s.plexTimeline.plays) > 4096 {
internal/state/plex_timeline.go:130:		var oldest plexPlayID
internal/state/plex_timeline.go:132:		for k, p := range s.plexTimeline.plays {
internal/state/plex_timeline.go:137:		delete(s.plexTimeline.plays, oldest)
internal/state/plex_timeline.go:146:func (s *State) plexProgressAt(ctx context.Context, user int64, key core.RatingKey, w progressWrite) error {
```

## internal/state/state.go

Plex timeline state machine remains in shared state; extraction remains.

```text
internal/state/state.go:160:	plexTimeline plexTimelineStore
internal/state/state.go:729:	w.timelineState, _ = ctx.Value(plexStatusKey{}).(string)
internal/state/state.go:753:		return s.plexProgressAt(ctx, userID, key, w)
```

## internal/transcode/manager.go

Detects the installed ffmpeg build/vendor capability, not the requesting frontend.

```text
internal/transcode/manager.go:236:	m.feat.jellyfinSegmentClock = strings.Contains(strings.ToLower(m.version), "jellyfin")
```

## internal/watchlist/community_job.go

External Plex account/provider identities and persisted settings vocabulary, not requesting-frontend behavior.

```text
internal/watchlist/community_job.go:31:	Plex       string     `json:"plex"` // External account source identity.
```

## internal/watchlist/watchlist.go

External Plex account/provider identities and persisted settings vocabulary, not requesting-frontend behavior.

```text
internal/watchlist/watchlist.go:56:	PlexFriendsStatus    []FriendFeedStatus `json:"plexFriendsStatus,omitempty"`
internal/watchlist/watchlist.go:58:	Plex                 bool               `json:"plex"`
internal/watchlist/watchlist.go:59:	PlexFriendsRSSURL    string             `json:"plexFriendsRssUrl"`
internal/watchlist/watchlist.go:60:	PlexOwnerRSSURL      string             `json:"plexOwnerRssUrl"`
internal/watchlist/watchlist.go:61:	PlexFriendsRSSStatus *RSSFeedStatus     `json:"plexFriendsRssStatus,omitempty"`
internal/watchlist/watchlist.go:62:	PlexOwnerRSSStatus   *RSSFeedStatus     `json:"plexOwnerRssStatus,omitempty"`
internal/watchlist/watchlist.go:163:func friendsRSSContext() []string { return []string{"watchlist_settings", "plexFriendsRssUrl", "1"} }
internal/watchlist/watchlist.go:164:func ownerRSSContext() []string   { return []string{"watchlist_settings", "plexOwnerRssUrl", "1"} }
internal/watchlist/watchlist.go:508:	Plex  string `json:"plex"`
internal/watchlist/watchlist.go:554:				e = s.replace(ctx, u, "plex", ts)
```

## internal/watchparty/engine.go

Jellyfin command selection remains in shared watch-party engine; extraction remains.

```text
internal/watchparty/engine.go:865:		if m.protocol == "jellyfin" {
internal/watchparty/engine.go:936:		if m.protocol == "jellyfin" {
internal/watchparty/engine.go:1012:		if m.protocol != "jellyfin" {
internal/watchparty/engine.go:1124:		if m.protocol == "jellyfin" {
internal/watchparty/engine.go:1148:		if m.protocol == "jellyfin" {
internal/watchparty/engine.go:1160:	if m.protocol == "jellyfin" {
internal/watchparty/engine.go:1182:		if o.protocol == "jellyfin" {
internal/watchparty/engine.go:1192:		if o.protocol == "jellyfin" {
internal/watchparty/engine.go:1208:	if m.protocol == "jellyfin" {
internal/watchparty/engine.go:1238:		if m.protocol == "jellyfin" {
```
