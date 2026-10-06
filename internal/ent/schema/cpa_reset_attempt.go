package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/scopes"
)

// CPAResetAttempt persists a provider reset claim even if the remote outcome is
// unknown. The unique credit_key is the concurrency gate: Codex keys one card,
// Claude keys one grant use (organization, grant, resets left before the use).
type CPAResetAttempt struct{ ent.Schema }

func (CPAResetAttempt) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (CPAResetAttempt) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("provider").Values("codex", "claude").Default("codex").Immutable(),
		field.String("credit_key").MaxLen(64).NotEmpty().Immutable().Sensitive(),
		field.Int("credential_id").Immutable(),
		field.Enum("state").Values("pending", "redeemed", "uncertain").Default("pending"),
		// request_id is reused verbatim when an uncertain Claude claim is retried,
		// so the provider can deduplicate it.
		field.String("request_id").MaxLen(64).Default("").Immutable(),
		field.String("grant_id").MaxLen(64).Default("").Immutable(),
		field.Int("resets_left").Optional().Nillable().Immutable(),
	}
}

func (CPAResetAttempt) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("credit_key").Unique().StorageKey("cpa_reset_attempts_by_credit_key"),
		index.Fields("credential_id").StorageKey("cpa_reset_attempts_by_credential_id"),
	}
}

func (CPAResetAttempt) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}

func (CPAResetAttempt) Policy() ent.Policy {
	return scopes.Policy{
		Query:    scopes.QueryPolicy{scopes.OwnerRule(), scopes.UserReadScopeRule(scopes.ScopeReadSettings)},
		Mutation: scopes.MutationPolicy{scopes.OwnerRule(), scopes.UserWriteScopeRule(scopes.ScopeWriteSettings)},
	}
}
