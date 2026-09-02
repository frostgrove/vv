package vvcfg

import (
	"os"

	"github.com/frostgrove/vv/utils/vvflag"
)

type PathOrigin int

const (
	PathFromNothing PathOrigin = iota
	PathFromCaller
	PathFromFlag
	PathFromEnvironment
	PathFromDefault
)

func (this PathOrigin) String() string {
	switch this {
	case PathFromCaller:
		return "the caller"
	case PathFromFlag:
		return "--config-path"
	case PathFromEnvironment:
		return "CONFIG_PATH"
	case PathFromDefault:
		return "the default path"
	default:
		return "nothing"
	}
}

type Source struct {
	Path               string
	Arguments          []string
	DefaultPath        string
	AllowNoFile        bool
	Strict             bool
	RequireEnvironment []string
}

const DefaultPath = "./config/app.yml"

func DefaultSource() Source {
	return Source{Arguments: os.Args[1:], DefaultPath: DefaultPath}
}

func (this Source) Resolve() (string, PathOrigin, error) {
	if this.Path != "" {
		return this.Path, PathFromCaller, nil
	}
	fromFlag, err := vvflag.Or(this.Arguments, "config-path", "")
	if err != nil {
		return "", PathFromNothing, err
	}
	if fromFlag != "" {
		return fromFlag, PathFromFlag, nil
	}
	if fromEnvironment := os.Getenv("CONFIG_PATH"); fromEnvironment != "" {
		return fromEnvironment, PathFromEnvironment, nil
	}
	if this.DefaultPath != "" {
		return this.DefaultPath, PathFromDefault, nil
	}
	if this.AllowNoFile {
		return "", PathFromNothing, nil
	}
	return "", PathFromNothing, ErrNoPath
}
