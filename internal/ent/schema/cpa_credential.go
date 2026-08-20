package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
)

// CPACredential stores the current credential and quota snapshot returned by CPA.
type CPACredential struct {
	ent.Schema
}

func (CPACredential) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (CPACredential) Fields() []ent.Field {
	return []ent.Field{
		field.Int("cpa_instance_id").Immutable(),
		field.String("external_key").NotEmpty(),
		field.String("auth_index").Default(""),
		field.String("remote_name").NotEmpty(),
		field.String("label").Default(""),
		field.String("display_name").NotEmpty(),
		field.String("provider").Default("unknown"),
		field.String("email").Default(""),
		field.String("status").Default("unknown"),
		field.String("status_message").Default(""),
		field.Bool("disabled").Default(false),
		field.Bool("unavailable").Default(false),
		field.Bool("runtime_only").Default(false),
		field.Int("priority").Default(0),
		field.String("plan_type").Default(""),
		field.JSON("quota_context", objects.CPAQuotaContext{}).
			Default(objects.CPAQuotaContext{}).
			Sensitive().
			Annotations(entgql.Skip(entgql.SkipAll)),
		field.String("quota_state").Default(string(objects.CPAQuotaStatePending)),
		field.JSON("quota_data", objects.CPAQuotaSnapshot{}).
			Default(objects.CPAQuotaSnapshot{}).
			Annotations(entgql.Skip(entgql.SkipAll)),
		field.Time("quota_last_attempt_at").Optional().Nillable(),
		field.Time("quota_last_success_at").Optional().Nillable(),
		field.Time("quota_last_failure_at").Optional().Nillable(),
		field.String("quota_last_error").Default(""),
	}
}

func (CPACredential) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("cpa_instance", CPAInstance.Type).
			Ref("credentials").
			Field("cpa_instance_id").
			Required().
			Immutable().
			Unique().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (CPACredential) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("cpa_instance_id", "external_key").Unique(),
		index.Fields("cpa_instance_id", "provider"),
		index.Fields("cpa_instance_id", "disabled"),
		index.Fields("cpa_instance_id", "unavailable"),
		index.Fields("cpa_instance_id", "plan_type"),
		index.Fields("cpa_instance_id", "priority", "display_name"),
	}
}

func (CPACredential) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}

func (CPACredential) Policy() ent.Policy {
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
