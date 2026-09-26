package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/scopes"
)

// CPAInstance stores one CLIProxyAPI management connection.
type CPAInstance struct {
	ent.Schema
}

func (CPAInstance) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (CPAInstance) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("base_url").NotEmpty(),
		field.String("encrypted_secret").Sensitive(),
		field.Bool("enabled").Default(true),
		field.Bool("insecure_skip_tls").Default(false),
		field.Bool("auto_refresh_enabled").Default(true),
		// usage_stream_enabled is retained as the compatibility name for the
		// HTTP usage-queue collector used by codex quota value estimation.
		field.Bool("usage_stream_enabled").Default(false),
		// usage_collector_id identifies the local collector ownership boundary and
		// remains stable across process restarts.
		field.String("usage_collector_id").Default(""),
		field.Int("refresh_interval_minutes").Default(5).Min(5).Max(1440),
		field.Time("next_refresh_at").Optional().Nillable(),
		field.Bool("auto_manage_enabled").Default(false),
		field.Bool("auto_reset_enabled").Default(false),
		field.Int("enabled_patrol_interval_minutes").Default(5).Min(1).Max(1440),
		field.Int("disabled_patrol_interval_minutes").Default(480).Min(60).Max(10080),
		field.Time("next_enabled_patrol_at").Optional().Nillable(),
		field.Time("next_disabled_patrol_at").Optional().Nillable(),
		field.String("server_version").Default(""),
		field.String("server_commit").Default(""),
		field.String("server_build_date").Default(""),
		field.Time("last_sync_attempt_at").Optional().Nillable(),
		field.Time("last_sync_success_at").Optional().Nillable(),
		field.Time("last_error_at").Optional().Nillable(),
		field.String("last_error").Optional().Nillable(),
	}
}

func (CPAInstance) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("credentials", CPACredential.Type),
	}
}

func (CPAInstance) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("name").Unique(),
		index.Fields("base_url").Unique(),
		index.Fields("enabled", "auto_refresh_enabled", "next_refresh_at"),
		index.Fields("enabled", "auto_manage_enabled", "next_enabled_patrol_at"),
		index.Fields("enabled", "auto_manage_enabled", "next_disabled_patrol_at"),
	}
}

func (CPAInstance) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}

func (CPAInstance) Policy() ent.Policy {
	return scopes.Policy{
		Query: scopes.QueryPolicy{
			scopes.OwnerRule(),
			scopes.UserReadScopeRule(scopes.ScopeReadSettings),
		},
		Mutation: scopes.MutationPolicy{
			scopes.OwnerRule(),
			scopes.UserWriteScopeRule(scopes.ScopeWriteSettings),
		},
	}
}
