package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/scopes"
)

// CpaUsageEvent stores one proxied request observed from a CPA instance's
// usage stream. Events are accumulated locally so per-credential usage within
// a quota cycle can be aggregated for the quota value estimation.
type CpaUsageEvent struct {
	ent.Schema
}

func (CpaUsageEvent) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (CpaUsageEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int("cpa_instance_id"),
		field.String("auth_index").Default(""),
		field.String("provider").Default(""),
		field.String("model").Default(""),
		field.String("source").Default(""),
		field.Int64("input_tokens").Default(0),
		field.Int64("output_tokens").Default(0),
		field.Int64("reasoning_tokens").Default(0),
		field.Int64("cached_tokens").Default(0),
		field.Int64("cache_read_tokens").Default(0),
		field.Int64("cache_creation_tokens").Default(0),
		field.Int64("total_tokens").Default(0),
		field.Bool("failed").Default(false),
		field.Int("status_code").Default(0),
		field.Time("requested_at"),
	}
}

func (CpaUsageEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("cpa_instance_id", "auth_index", "requested_at"),
		index.Fields("cpa_instance_id", "requested_at"),
	}
}

func (CpaUsageEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}

func (CpaUsageEvent) Policy() ent.Policy {
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
