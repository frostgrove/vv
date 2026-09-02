package crud

import "context"

type Creator[M any] interface {
	Create(ctx context.Context, m *M) (M, error)
}

type Replacer[M any] interface {
	Replace(ctx context.Context, m *M) (M, error)
}

func CreateOf[M any, ID comparable](core Core[M, ID], ctx context.Context, m *M) (M, error, bool) {
	if creator, ok := core.(Creator[M]); ok {
		created, err := creator.Create(ctx, m)
		return created, err, true
	}
	var zero M
	return zero, nil, false
}

func ReplaceOf[M any, ID comparable](core Core[M, ID], ctx context.Context, m *M) (M, error, bool) {
	if replacer, ok := core.(Replacer[M]); ok {
		replaced, err := replacer.Replace(ctx, m)
		return replaced, err, true
	}
	var zero M
	return zero, nil, false
}

func (this *Repo[M, ID, U]) Create(ctx context.Context, m *M) (M, error) {
	created, err, ok := CreateOf(this.Core, ctx, m)
	if !ok {
		var zero M
		return zero, ErrNoCreateSupport
	}
	return created, err
}

func (this *Repo[M, ID, U]) Replace(ctx context.Context, m *M) (M, error) {
	replaced, err, ok := ReplaceOf(this.Core, ctx, m)
	if !ok {
		var zero M
		return zero, ErrNoReplaceSupport
	}
	return replaced, err
}
