package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("tenant_id"),
		field.String("email"),
		field.String("name"),
		field.Int("age").Optional().Nillable(),
		field.Bool("active").Default(true),
		field.Time("created_at").Default(time.Now),
	}
}
