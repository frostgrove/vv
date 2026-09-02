package module

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrDefinition = errors.New("module: the definition is not usable")
	ErrProfile    = errors.New("module: the profile is not usable")
	ErrCatalog    = errors.New("module: the catalog is not usable")
)

// Refusal carries every problem at once: one problem per run means one run per
// problem, and a composition root is assembled by someone reading what it
// printed.
type Refusal struct {
	kind     error
	subject  string
	problems []string
}

func refuse(kind error, subject string, problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return &Refusal{kind: kind, subject: subject, problems: slices.Clone(problems)}
}

func (this *Refusal) Problems() []string { return slices.Clone(this.problems) }

func (this *Refusal) Is(target error) bool { return target == this.kind }

func (this *Refusal) Error() string {
	head := this.kind.Error()
	if this.subject != "" {
		head = fmt.Sprintf("%s: %s", head, this.subject)
	}
	return fmt.Sprintf("%s:\n  - %s", head, strings.Join(this.problems, "\n  - "))
}
