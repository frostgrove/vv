package wire

type PatchMapper[P, U any] interface {
	Update(patch P) U
}

type Presenter[M, R any] interface {
	Response(model M) R
}

func IdentityPatch[U any]() PatchMapper[U, U] { return identityPatch[U]{} }

type identityPatch[U any] struct{}

func (identityPatch[U]) Update(patch U) U { return patch }

func IdentityPresenter[M any]() Presenter[M, M] { return identityPresenter[M]{} }

type identityPresenter[M any] struct{}

func (identityPresenter[M]) Response(model M) M { return model }
