// Package schema is the ent declaration the two ent examples share.
//
// In your own project this is simply your ent schema; nothing here is
// vv-specific. The generated entity struct next door is what vv
// binds to, as-is.
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Product is an ordinary ent schema.
type Product struct {
	ent.Schema
}

func (Product) Fields() []ent.Field {
	return []ent.Field{
		// The table uses BIGSERIAL, so widen ent's default int id.
		field.Int64("id"),
		field.String("sku"),
		field.String("name"),
		field.Int("price"),
		field.Int("stock").Optional().Nillable(),
		field.Bool("active").Default(true),
		field.Time("created_at").Default(time.Now),
	}
}
