package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type Product struct {
	ent.Schema
}

func (Product) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.String("sku"),
		field.String("name"),
		field.Int("price"),
		field.Int("stock").Optional().Nillable(),
		field.Bool("active").Default(true),
		field.Time("created_at").Default(time.Now),
	}
}
