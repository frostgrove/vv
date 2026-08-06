// Package schema is the ent declaration of the same users table.
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// User mirrors the table the rest of the integration suite uses.
type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		// The table uses BIGSERIAL / BIGINT, so widen ent's default int id.
		field.Int64("id"),
		field.Int64("tenant_id"),
		field.String("email"),
		field.String("name"),
		field.Int("age").Optional().Nillable(),
		field.Bool("active").Default(true),
		field.Time("created_at").Default(time.Now),
	}
}
