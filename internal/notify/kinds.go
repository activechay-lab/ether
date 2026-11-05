package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/extend"
)

// ChannelKind is one way notifications can be delivered: a settings schema
// the console renders, validation, and delivery. Built-ins and (through
// Service.RegisterChannelKind) plugin kinds use the same shape; each kind
// becomes a channelKind descriptor in the extend registry.
type ChannelKind struct {
	ID    string
	Owner string // extend.Builtin or a plugin id
	Label extend.Text
	Hint  extend.Text
	Icon  string
	Order int

	Fields     []extend.SettingsField
	SendTest   bool
	Selectable bool
	Formats    []string
	MaxLength  int

	Validate func(c Channel) error
	Deliver  func(ctx context.Context, s *Service, c Channel, e Event) error
}

// Descriptor returns the kind as a channelKind descriptor.
func (k ChannelKind) Descriptor() extend.Descriptor {
	owner := k.Owner
	if owner == "" {
		owner = extend.Builtin
	}
	id := k.ID
	if owner != extend.Builtin {
		id = extend.PluginID(owner, k.ID)
	}
	return extend.Descriptor{
		Kind: extend.KindChannelKind, ID: id, Owner: owner,
		Label: k.Label, Hint: k.Hint, Icon: k.Icon, Order: k.Order,
	}.WithMeta(extend.ChannelKindMeta{
		Fields: k.Fields, SendTest: k.SendTest, Selectable: k.Selectable,
		Formats: k.Formats, MaxLength: k.MaxLength,
	})
}

// KindRegistry holds the channel kinds a service can validate and deliver.
// It is safe for concurrent use; the plugin host registers plugin kinds
// here as plugins are installed.
type KindRegistry struct {
	mu    sync.RWMutex
	kinds map[string]ChannelKind
}

// NewKindRegistry returns a registry holding kinds.
func NewKindRegistry(kinds ...ChannelKind) *KindRegistry {
	r := &KindRegistry{kinds: map[string]ChannelKind{}}
	for _, k := range kinds {
		_ = r.Register(k)
	}
	return r
}

// Register adds a kind. IDs must be unique; plugin kinds are namespaced
// by their descriptor but share this delivery table by full descriptor id.
func (r *KindRegistry) Register(k ChannelKind) error {
	if k.ID == "" {
		return errors.New("channel kind id required")
	}
	if k.Validate == nil || k.Deliver == nil {
		return fmt.Errorf("channel kind %q needs validate and deliver", k.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.kinds[k.ID]; ok {
		return fmt.Errorf("channel kind %q already registered", k.ID)
	}
	r.kinds[k.ID] = k
	return nil
}

// RemovePlugin removes a plugin's channel kind without affecting built-ins.
func (r *KindRegistry) RemovePlugin(id, owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.kinds[id]; ok && k.Owner == owner && owner != "" && owner != extend.Builtin {
		delete(r.kinds, id)
	}
}

// Get returns one kind.
func (r *KindRegistry) Get(id string) (ChannelKind, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.kinds[id]
	return k, ok
}

// List returns every kind, ordered for pickers.
func (r *KindRegistry) List() []ChannelKind {
	r.mu.RLock()
	out := make([]ChannelKind, 0, len(r.kinds))
	for _, k := range r.kinds {
		out = append(out, k)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// BuiltinChannelKinds returns Ether's own channel kinds, ordered as in the
// console picker.
func BuiltinChannelKinds() []ChannelKind {
	httpFields := func(endpointLabel, endpointHint extend.Text) []extend.SettingsField {
		return []extend.SettingsField{
			{Key: "endpoint", Kind: extend.FieldURL, Label: endpointLabel, Hint: endpointHint, Required: true, Secret: true, Wide: true},
			{Key: "token", Kind: extend.FieldSecret, Label: extend.T("Access token"), Hint: extend.T("Optional. A bearer token, or the app key for Gotify."), Secret: true},
		}
	}
	endpoint := extend.T("Endpoint URL")
	return []ChannelKind{
		{
			ID: "webpush", Owner: extend.Builtin, Label: extend.T("Browser push"), Icon: "bell-ring", Order: 0,
			Hint:       extend.T("This subscription belongs to one browser profile on one device. Use Connect this device on each device you want to receive alerts."),
			Fields:     []extend.SettingsField{{Key: "subscription", Kind: extend.FieldHidden, Label: extend.T("Browser subscription"), Required: true, Secret: true}},
			SendTest:   true,
			Selectable: false,
			Formats:    []string{"text"},
			MaxLength:  450,
			Validate: func(c Channel) error {
				sub, err := c.Subscription()
				if err != nil {
					return errors.New("browser subscription required; connect this device from Notifications")
				}
				return validatePush(sub)
			},
			Deliver: func(ctx context.Context, s *Service, c Channel, e Event) error { return s.sendPush(ctx, c, e) },
		},
		{
			ID: "webhook", Owner: extend.Builtin, Label: extend.T("Webhook"), Icon: "webhook", Order: 10,
			Hint:       extend.T("Ether sends a JSON POST with kind, title, message and at. Optionally add a bearer token. Only HTTP 2xx responses count as accepted."),
			Fields:     httpFields(endpoint, extend.Text{}),
			SendTest:   true,
			Selectable: true,
			Formats:    []string{"text", "json"},
			Validate:   validateEndpoint,
			Deliver:    deliverWebhook,
		},
		{
			ID: "discord", Owner: extend.Builtin, Label: extend.T("Discord"), Icon: "message-circle", Order: 20,
			Hint:       extend.T("Create an incoming webhook in the destination Discord channel and paste its full webhook URL. Mentions are disabled."),
			Fields:     httpFields(endpoint, extend.Text{}),
			SendTest:   true,
			Selectable: true,
			Formats:    []string{"text"},
			MaxLength:  1900,
			Validate:   validateEndpoint,
			Deliver:    deliverDiscord,
		},
		{
			ID: "slack", Owner: extend.Builtin, Label: extend.T("Slack"), Icon: "hash", Order: 30,
			Hint:       extend.T("Create an incoming webhook for the destination Slack channel and paste its full URL."),
			Fields:     httpFields(endpoint, extend.Text{}),
			SendTest:   true,
			Selectable: true,
			Formats:    []string{"text"},
			MaxLength:  3900,
			Validate:   validateEndpoint,
			Deliver:    deliverSlack,
		},
		{
			ID: "telegram", Owner: extend.Builtin, Label: extend.T("Telegram"), Icon: "send", Order: 40,
			Hint: extend.T("Create a bot with BotFather, message it first, then enter its bot token and the destination chat ID. Group chat IDs may be negative."),
			Fields: []extend.SettingsField{
				{Key: "token", Kind: extend.FieldSecret, Label: extend.T("Bot token"), Required: true, Secret: true},
				{Key: "target", Kind: extend.FieldText, Label: extend.T("Chat ID"), Required: true},
			},
			SendTest:   true,
			Selectable: true,
			Formats:    []string{"text"},
			MaxLength:  4000,
			Validate: func(c Channel) error {
				if c.Secret("token") == "" || c.Setting("target") == "" || strings.ContainsAny(c.Secret("token"), "/?#\r\n") {
					return errors.New("Telegram bot token and chat ID required")
				}
				return nil
			},
			Deliver: deliverTelegram,
		},
		{
			ID: "smtp", Owner: extend.Builtin, Label: extend.T("Email (SMTP)"), Icon: "mail", Order: 50,
			Hint: extend.T("Use your mail provider’s SMTP server and app password. STARTTLS usually uses port 587; implicit TLS uses 465. Authentication is optional for a trusted TLS relay. Recipients can be separated by commas."),
			Fields: []extend.SettingsField{
				{Key: "endpoint", Kind: extend.FieldText, Label: extend.T("SMTP host:port"), Hint: extend.T("The server must support TLS."), Required: true, Secret: true, Wide: true},
				{Key: "smtpMode", Kind: extend.FieldSelect, Label: extend.T("Connection security"), Default: "starttls", Options: []extend.FieldOption{
					{Value: "starttls", Label: extend.T("STARTTLS (usually 587)")},
					{Value: "tls", Label: extend.T("Implicit TLS (usually 465)")},
				}},
				{Key: "username", Kind: extend.FieldText, Label: extend.T("Username"), Secret: true},
				{Key: "password", Kind: extend.FieldSecret, Label: extend.T("Password"), Secret: true},
				{Key: "from", Kind: extend.FieldText, Label: extend.T("From address"), Required: true},
				{Key: "to", Kind: extend.FieldText, Label: extend.T("To addresses"), Required: true},
			},
			SendTest:   true,
			Selectable: true,
			Formats:    []string{"text", "html"},
			Validate:   validateSMTP,
			Deliver: func(ctx context.Context, _ *Service, c Channel, e Event) error {
				return sendSMTP(ctx, c, e)
			},
		},
		{
			ID: "ntfy", Owner: extend.Builtin, Label: extend.T("ntfy"), Icon: "bell", Order: 60,
			Hint:       extend.T("Enter your complete topic URL. Subscribe to the same topic in the ntfy phone app. Add a bearer token for private topics."),
			Fields:     httpFields(endpoint, extend.Text{}),
			SendTest:   true,
			Selectable: true,
			Formats:    []string{"text"},
			Validate:   validateEndpoint,
			Deliver:    deliverNtfy,
		},
		{
			ID: "gotify", Owner: extend.Builtin, Label: extend.T("Gotify"), Icon: "bell-plus", Order: 70,
			Hint:       extend.T("Enter your server’s /message URL and an application token. Connect the Gotify app on your phone to receive alerts."),
			Fields:     httpFields(endpoint, extend.Text{}),
			SendTest:   true,
			Selectable: true,
			Formats:    []string{"text"},
			Validate:   validateEndpoint,
			Deliver:    deliverGotify,
		},
	}
}

func builtinRegistry() *KindRegistry { return NewKindRegistry(BuiltinChannelKinds()...) }

// ChannelKindDescriptors returns the built-in channelKind descriptors for
// the extend registry.
func ChannelKindDescriptors() []extend.Descriptor {
	kinds := BuiltinChannelKinds()
	out := make([]extend.Descriptor, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k.Descriptor())
	}
	return out
}

func validateEndpoint(c Channel) error {
	raw := c.Secret("endpoint")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || strings.ContainsAny(raw, "\r\n") {
		return errors.New("HTTP(S) endpoint without embedded username required")
	}
	return nil
}

func validateSMTP(c Channel) error {
	host, port, err := net.SplitHostPort(c.Secret("endpoint"))
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || portErr != nil || n < 1 || n > 65535 {
		return errors.New("SMTP host and port (1–65535) required")
	}
	if mode := c.Setting("smtpMode"); mode != "" && mode != "starttls" && mode != "tls" {
		return errors.New("SMTP security must be STARTTLS or implicit TLS")
	}
	for _, v := range []string{c.Setting("from"), c.Setting("to"), c.Secret("username")} {
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("SMTP fields cannot contain newlines")
		}
	}
	if _, err := mail.ParseAddress(c.Setting("from")); err != nil {
		return errors.New("valid sender email address required")
	}
	recipients, err := mail.ParseAddressList(c.Setting("to"))
	if err != nil || len(recipients) == 0 || len(recipients) > 50 {
		return errors.New("provide 1–50 recipient email addresses, separated by commas")
	}
	if c.Secret("password") != "" && c.Secret("username") == "" {
		return errors.New("SMTP username required when a password is set")
	}
	return nil
}
