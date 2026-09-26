package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/scopes"
)

// CPACodexResetAttempt persists a claim even if the remote outcome is unknown.
type CPACodexResetAttempt struct{ ent.Schema }

func (CPACodexResetAttempt) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (CPACodexResetAttempt) Fields() []ent.Field {
	return []ent.Field{
		field.String("credit_key").MaxLen(64).NotEmpty().Immutable().Sensitive(),
		field.Int("credential_id").Immutable(),
		field.Enum("state").Values("pending", "redeemed", "uncertain").Default("pending"),
	}
}

func (CPACodexResetAttempt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("credit_key").Unique().StorageKey("cpa_codex_reset_attempts_by_credit_key")}
}

func (CPACodexResetAttempt) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}

func (CPACodexResetAttempt) Policy() ent.Policy {
	return scopes.Policy{
		Query:    scopes.QueryPolicy{scopes.OwnerRule(), scopes.UserReadScopeRule(scopes.ScopeReadSettings)},
		Mutation: scopes.MutationPolicy{scopes.OwnerRule(), scopes.UserWriteScopeRule(scopes.ScopeWriteSettings)},
	}
}
