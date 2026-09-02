package module

type Builder struct {
	spec Spec
}

func New(name string) *Builder {
	return &Builder{spec: Spec{Name: name}}
}

func (this *Builder) Order(order int) *Builder {
	this.spec.Order = order
	return this
}

func (this *Builder) Provide(constructors ...any) *Builder {
	this.spec.Provide = append(this.spec.Provide, constructors...)
	return this
}

func (this *Builder) Routes(constructors ...any) *Builder {
	this.spec.Routes = append(this.spec.Routes, constructors...)
	return this
}

func (this *Builder) Workers(constructors ...any) *Builder {
	this.spec.Workers = append(this.spec.Workers, constructors...)
	return this
}

func (this *Builder) Seeders(constructors ...any) *Builder {
	this.spec.Seeders = append(this.spec.Seeders, constructors...)
	return this
}

func (this *Builder) Checks(constructors ...any) *Builder {
	this.spec.Checks = append(this.spec.Checks, constructors...)
	return this
}

func (this *Builder) Build() (Definition, error) { return Define(this.spec) }

func (this *Builder) MustBuild() Definition { return MustDefine(this.spec) }
