package main

// Scoped credentials. tokenScopes are the read-only and the full scopes of
// a service by its preset, when its OAuth server advertises such scopes
// (checked at each one's /.well-known/oauth-protected-resource, October
// 2026). byName: the scopes are the ones the server advertises, split by
// their names (splitScopes), which keeps up with a vendor adding some.
// Another server is split the same way; one whose scopes do not read as
// reads and writes (Notion's "default", Stripe's "mcp") is not scoped:
// Eriphe holds to the level itself.
type scopeSet struct {
	read, write []string
	byName      bool
}

var tokenScopes = map[string]scopeSet{
	"linear": {read: []string{"read"}, write: []string{"read", "write"}},
	// Its MCP server advertises data:read_write only; data:read is the API's.
	"todoist": {read: []string{"data:read"}, write: []string{"data:read_write"}},
	// jobs and inference-api spend the account's credits: not a read.
	"huggingface": {read: []string{"openid", "profile", "read-mcp", "read-repos"},
		write: []string{"openid", "profile", "read-mcp", "read-repos", "contribute-repos", "jobs", "inference-api"}},
	// Its read set has account:read: the channels of a post are the account's.
	"buffer": {read: []string{"account:read", "posts:read", "ideas:read", "insights:read", "engagements:read"},
		write: []string{"account:read", "posts:read", "posts:write", "ideas:read", "ideas:write", "insights:read",
			"engagements:read", "engagements:write"}},
	"metricool": {read: []string{"mcp:read"}, write: []string{"mcp:read", "mcp:write"}},
	// Generation spends the account's credits: not a read.
	"elevenlabs": {read: []string{"convai_read", "speech_history_read", "ads_read"},
		write: []string{"convai_read", "convai_write", "text_to_speech", "speech_history_read", "flows",
			"image_video_generation", "voice_generation", "ads_read", "ads_write"}},
	"airtable": {byName: true}, "beehiiv": {byName: true}, "calendly": {byName: true}, "canva": {byName: true}, "dropbox": {byName: true},
	"evernote": {byName: true}, "gamma": {byName: true}, "jam": {byName: true}, "mem": {byName: true},
	"mercury": {byName: true}, "miro": {byName: true}, "netlify": {byName: true}, "raindrop": {byName: true},
	"ramp": {byName: true}, "readwise": {byName: true}, "sentry": {byName: true}, "supabase": {byName: true},
	"ticktick": {byName: true},
	// Its scopes name no verb: readOnly or full.
	"jotform": {read: []string{"readOnly"}, write: []string{"full"}},
	"akiflow": {byName: true}, "capacities": {byName: true}, "close": {byName: true}, "mural": {byName: true},
	"neon": {byName: true}, "pandadoc": {byName: true}, "posthog": {byName: true}, "qonto": {byName: true},
	"slite": {byName: true}, "sunsama": {byName: true}, "typeform": {byName: true}, "whimsical": {byName: true},
	"amplitude": {byName: true}, "circle": {byName: true}, "grafana": {byName: true}, "honeycomb": {byName: true},
	"leadpages": {byName: true}, "lovable": {byName: true}, "mintlify": {byName: true}, "wave": {byName: true},
	// publish names no verb; the theme and the account's users, which Kajabi
	// keeps off by default, are left out of the full set.
	"kajabi": {read: []string{"read"}, write: []string{"read", "write:content", "write:commerce", "write:contacts",
		"write:community", "write:emails", "write:automations", "publish"}},
	// Its restart, configure and full_access scopes name no verb. The full set
	// is the one its server asks for, without cluster deletion or billing.
	"typesense": {read: []string{"clusters:read", "metrics:read", "backups:read", "search_insights:read", "data:view", "data:search"},
		write: []string{"clusters:read", "metrics:read", "backups:read", "clusters:create", "clusters:configure", "clusters:restart",
			"cluster_parameters:manage", "search_insights:read", "data:view", "data:search", "data:full_access", "data:manage_synonyms",
			"data:manage_overrides", "data:manage_presets", "data:manage_stopwords", "data:manage_stemming", "data:manage_analytics_rules",
			"data:manage_conversation_models", "data:manage_natural_language_models"}},
}
