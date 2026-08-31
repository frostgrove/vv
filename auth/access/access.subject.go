package access

import (
	"context"

	"github.com/google/uuid"
)

type Subject struct {
	Type SubjectType

	Directory Directory

	Normalize func(identifier string) string
}

func (this Subject) Identifier(raw string) string {
	if this.Normalize == nil {
		return raw
	}
	return this.Normalize(raw)
}

func (this Subject) Ref(id uuid.UUID) SubjectRef {
	return SubjectRef{Type: this.Type, ID: id}
}

type Registrar[P any] interface {
	Create(ctx context.Context, payload P) (uuid.UUID, string, error)

	Password(payload P) string
}
