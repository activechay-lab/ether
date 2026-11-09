package notify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestChannelAndEventDescriptorsValidate(t *testing.T) {
	r := extend.New()
	channels := ChannelKindDescriptors()
	events := EventKindDescriptors()
	if err := r.Register(append(channels, events...)...); err != nil {
		t.Fatal(err)
	}
	wantChannels := []string{"webpush", "webhook", "discord", "slack", "telegram", "smtp", "ntfy", "gotify"}
	gotChannels := r.List(extend.KindChannelKind)
	if len(gotChannels) != len(wantChannels) {
		t.Fatalf("channel kinds = %d, want %d", len(gotChannels), len(wantChannels))
	}
	for i, want := range wantChannels {
		d := gotChannels[i]
		if d.ID != want {
			t.Fatalf("channel kind %d = %q, want %q", i, d.ID, want)
		}
		meta, err := extend.MetaOf[extend.ChannelKindMeta](d)
		if err != nil {
			t.Fatalf("%s meta: %v", d.ID, err)
		}
		if len(meta.Fields) == 0 {
			t.Fatalf("%s has no fields", d.ID)
		}
		if meta.Selectable != (d.ID != "webpush") {
			t.Fatalf("%s selectable = %v", d.ID, meta.Selectable)
		}
		if !meta.SendTest {
			t.Fatalf("%s sendTest = false", d.ID)
		}
	}
	gotEvents := r.List(extend.KindEventKind)
	if len(gotEvents) != len(Kinds)-1 { // "test" has no descriptor
		t.Fatalf("event kinds = %d, want %d", len(gotEvents), len(Kinds)-1)
	}
	criticalCount := 0
	for _, d := range gotEvents {
		if d.Hint.Empty() {
			t.Fatalf("event %s missing description hint", d.ID)
		}
		meta, err := extend.MetaOf[extend.EventKindMeta](d)
		if err != nil {
			t.Fatalf("%s meta: %v", d.ID, err)
		}
		if meta.Critical {
			criticalCount++
		}
		if d.ID == "newsletter.weekly" && (len(meta.ChannelKinds) != 1 || meta.ChannelKinds[0] != "smtp") {
			t.Fatalf("newsletter.weekly channelKinds = %v", meta.ChannelKinds)
		}
		if (d.ID == "cert.renewed" || d.ID == "user.joined") && meta.Automatic {
			t.Fatalf("%s marked automatic", d.ID)
		}
	}
	if criticalCount != 7 {
		t.Fatalf("critical events = %d, want 7", criticalCount)
	}
}

func TestKindRegistryPluginRegistrationAndDelivery(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, Options{})

	if err := s.RegisterChannelKind(ChannelKind{}); err == nil {
		t.Fatal("accepted empty kind")
	}
	if err := s.RegisterChannelKind(ChannelKind{ID: "webhook", Validate: validateEndpoint, Deliver: deliverWebhook}); err == nil {
		t.Fatal("accepted duplicate kind")
	}

	var delivered Channel
	var deliveredEvent Event
	pluginKind := ChannelKind{
		ID:         extend.PluginID("community.gotify.plus", "push"),
		Owner:      "community.gotify.plus",
		Label:      extend.PluginText("community.gotify.plus", "name"),
		Selectable: true,
		SendTest:   true,
		Fields: []extend.SettingsField{
			{Key: "room", Kind: extend.FieldText, Label: extend.T("Room"), Required: true},
			{Key: "apiKey", Kind: extend.FieldSecret, Label: extend.T("API key"), Required: true, Secret: true},
		},
		Validate: func(c Channel) error {
			if c.Setting("room") == "" || c.Secret("apiKey") == "" {
				return errSecretNeedsDestination
			}
			return nil
		},
		Deliver: func(_ context.Context, _ *Service, c Channel, e Event) error {
			delivered = c
			deliveredEvent = e
			return nil
		},
	}
	if err := pluginKind.Descriptor().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterChannelKind(pluginKind); err != nil {
		t.Fatal(err)
	}
	ch := Channel{
		ID:       "p1",
		Name:     "Matrix room",
		Kind:     pluginKind.ID,
		Enabled:  true,
		Events:   []string{"playback.error"},
		Settings: map[string]string{"room": "!ops:example.org"},
		Secrets:  map[string]string{"apiKey": "s3cr3t"},
	}
	if err := s.Save(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if err := s.Emit(ctx, Event{Kind: "playback.error", Title: "Failed", Message: "boom"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if delivered.Setting("room") != "!ops:example.org" || delivered.Secret("apiKey") != "s3cr3t" || deliveredEvent.Message != "boom" {
		t.Fatalf("plugin delivery got %+v %+v", delivered, deliveredEvent)
	}
}

func TestEveryBuiltinKindValidation(t *testing.T) {
	validSub := &PushSubscription{
		Endpoint: "https://push.example.test/sub",
		Keys:     PushKeys{Auth: "AAAAAAAAAAAAAAAAAAAAAA", P256dh: validP256dh(t)},
	}
	pushChan := Channel{Kind: "webpush"}
	pushChan.SetSubscription(validSub)

	cases := []struct {
		name    string
		channel Channel
		wantErr string
	}{
		{"unknown kind", Channel{Kind: " carrier-pigeon "}, "unsupported channel kind"},
		{"webpush valid", pushChan, ""},
		{"webpush missing sub", Channel{Kind: "webpush"}, "browser subscription required"},
		{"webpush http sub", func() Channel {
			c := Channel{Kind: "webpush"}
			c.SetSubscription(&PushSubscription{Endpoint: "http://push.example.test/sub", Keys: validSub.Keys})
			return c
		}(), "HTTPS push endpoint required"},
		{"webhook valid", Channel{Kind: "webhook", Secrets: map[string]string{"endpoint": "https://hooks.example.test/a"}}, ""},
		{"webhook embedded user", Channel{Kind: "webhook", Secrets: map[string]string{"endpoint": "https://u:p@hooks.example.test/a"}}, "without embedded username"},
		{"discord valid", Channel{Kind: "discord", Secrets: map[string]string{"endpoint": "https://discord.example.test/a"}}, ""},
		{"discord missing scheme", Channel{Kind: "discord", Secrets: map[string]string{"endpoint": "discord.example.test/a"}}, "HTTP(S) endpoint"},
		{"slack valid", Channel{Kind: "slack", Secrets: map[string]string{"endpoint": "https://slack.example.test/a"}}, ""},
		{"slack newline", Channel{Kind: "slack", Secrets: map[string]string{"endpoint": "https://slack.example.test/a\r\n"}}, "HTTP(S) endpoint"},
		{"ntfy valid", Channel{Kind: "ntfy", Secrets: map[string]string{"endpoint": "https://ntfy.example.test/topic"}}, ""},
		{"gotify valid", Channel{Kind: "gotify", Secrets: map[string]string{"endpoint": "https://gotify.example.test/message", "token": "app"}}, ""},
		{"telegram valid", Channel{Kind: "telegram", Secrets: map[string]string{"token": "123:ABC"}, Settings: map[string]string{"target": "-100123"}}, ""},
		{"telegram missing target", Channel{Kind: "telegram", Secrets: map[string]string{"token": "123:ABC"}}, "Telegram bot token and chat ID required"},
		{"telegram bad token", Channel{Kind: "telegram", Secrets: map[string]string{"token": "123/ABC"}, Settings: map[string]string{"target": "1"}}, "Telegram bot token and chat ID required"},
		{"smtp valid", Channel{Kind: "smtp", Secrets: map[string]string{"endpoint": "smtp.example.test:587"}, Settings: map[string]string{"smtpMode": "starttls", "from": "a@example.test", "to": "b@example.test"}}, ""},
		{"smtp bad port", Channel{Kind: "smtp", Secrets: map[string]string{"endpoint": "smtp.example.test:99999"}, Settings: map[string]string{"from": "a@example.test", "to": "b@example.test"}}, "SMTP host and port"},
		{"smtp bad mode", Channel{Kind: "smtp", Secrets: map[string]string{"endpoint": "smtp.example.test:587"}, Settings: map[string]string{"smtpMode": "plain", "from": "a@example.test", "to": "b@example.test"}}, "STARTTLS or implicit TLS"},
		{"smtp password without user", Channel{Kind: "smtp", Secrets: map[string]string{"endpoint": "smtp.example.test:587", "password": "pw"}, Settings: map[string]string{"from": "a@example.test", "to": "b@example.test"}}, "SMTP username required"},
		{"bad event rule", Channel{Kind: "webhook", Secrets: map[string]string{"endpoint": "https://hooks.example.test/a"}, Events: []string{"not.an.event"}}, "unsupported event rule"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateChannel(tc.channel)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestLegacyFlatChannelInPlaceMigration(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sealer := upgradedSealer(t)

	// Seal values under the pre-registry flat-struct contexts, exactly as a
	// release before WS1c wrote them.
	ep, _ := config.SealWith(sealer, "smtp.legacy.test:587", "notify_channels", "endpoint", "legacy-smtp", "smtp")
	user, _ := config.SealWith(sealer, "legacy-user", "notify_channels", "username", "legacy-smtp", "smtp", "smtp.legacy.test:587")
	pass, _ := config.SealWith(sealer, "legacy-pass", "notify_channels", "password", "legacy-smtp", "smtp", "smtp.legacy.test:587")
	flatSMTP := `{"id":"legacy-smtp","name":"Old Mail","kind":"smtp","enabled":true,"events":["server.crash"],"smtpMode":"tls","endpoint":` + quoteJSON(ep) + `,"username":` + quoteJSON(user) + `,"password":` + quoteJSON(pass) + `,"from":"from@legacy.test","to":"to@legacy.test","perMinute":5,"digestMinutes":0}`

	p256 := validP256dh(t)
	subEP, _ := config.SealWith(sealer, "https://push.legacy.test/sub/1", "notify_channels", "subscription.endpoint", "legacy-push", "webpush")
	subAuth, _ := config.SealWith(sealer, "AAAAAAAAAAAAAAAAAAAAAA", "notify_channels", "subscription.auth", "legacy-push", "webpush", "https://push.legacy.test/sub/1")
	subP256, _ := config.SealWith(sealer, p256, "notify_channels", "subscription.p256dh", "legacy-push", "webpush", "https://push.legacy.test/sub/1")
	flatPush := `{"id":"legacy-push","name":"Old Phone","kind":"webpush","enabled":true,"events":["server.crash"],"subscription":{"endpoint":` + quoteJSON(subEP) + `,"keys":{"auth":` + quoteJSON(subAuth) + `,"p256dh":` + quoteJSON(subP256) + `}},"perMinute":5,"digestMinutes":0}`

	flatTG := `{"id":"legacy-tg","name":"Old TG","kind":"telegram","enabled":true,"events":["playback.error"],"token":"plain-bot-token","target":"-10042","perMinute":5,"digestMinutes":0}`

	for _, row := range []struct{ id, raw string }{
		{"legacy-smtp", flatSMTP},
		{"legacy-push", flatPush},
		{"legacy-tg", flatTG},
	} {
		if _, err := db.Exec(`INSERT INTO notify_channels(id, config) VALUES(?, ?)`, row.id, row.raw); err != nil {
			t.Fatal(err)
		}
	}

	s := New(db, Options{Sealer: sealer})
	n, err := s.MigrateSecrets(ctx)
	if err != nil || n != 3 {
		t.Fatalf("MigrateSecrets = %d, %v; want 3, nil", n, err)
	}
	for _, id := range []string{"legacy-smtp", "legacy-push", "legacy-tg"} {
		raw := rawChannel(t, db, id)
		for _, leaked := range []string{"smtp.legacy.test", "legacy-user", "legacy-pass", "push.legacy.test", "plain-bot-token"} {
			if strings.Contains(raw, leaked) {
				t.Fatalf("migrated row %s still contains plaintext secret %q: %s", id, leaked, raw)
			}
		}
		var top map[string]any
		if err := json.Unmarshal([]byte(raw), &top); err != nil {
			t.Fatal(err)
		}
		for _, flatKey := range []string{"endpoint", "token", "target", "username", "password", "from", "to", "smtpMode", "subscription"} {
			if _, ok := top[flatKey]; ok {
				t.Fatalf("migrated row %s still has top-level flat key %q: %s", id, flatKey, raw)
			}
		}
		if _, ok := top["secrets"]; !ok {
			t.Fatalf("migrated row %s not in {settings,secrets} shape: %s", id, raw)
		}
	}
	cs, err := s.channels(ctx)
	if err != nil || len(cs) != 3 {
		t.Fatalf("channels after migration: %+v %v", cs, err)
	}
	byID := map[string]Channel{}
	for _, c := range cs {
		byID[c.ID] = c
	}
	if byID["legacy-smtp"].Secret("password") != "legacy-pass" || byID["legacy-smtp"].Setting("from") != "from@legacy.test" || byID["legacy-smtp"].Setting("smtpMode") != "tls" {
		t.Fatalf("migrated smtp: %+v", byID["legacy-smtp"])
	}
	sub, err := byID["legacy-push"].Subscription()
	if err != nil || sub.Endpoint != "https://push.legacy.test/sub/1" || sub.Keys.P256dh != p256 {
		t.Fatalf("migrated push: %+v %v", sub, err)
	}
	if byID["legacy-tg"].Secret("token") != "plain-bot-token" || byID["legacy-tg"].Setting("target") != "-10042" {
		t.Fatalf("migrated tg: %+v", byID["legacy-tg"])
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
