// Package gormstore is the gorm half of the integration: ordinary gorm models,
// with `rel` tags alongside the `gorm` ones so rx-crud can navigate the same
// associations. It is what docs/gorm.md tells a gorm project to do, executed.
package gormstore

//go:generate go run rx-crud/cmd/rxcrud -readonly UpdatedAt,DeletedAt

import (
	"sync/atomic"

	"gorm.io/gorm"
)

// Team is an ordinary gorm model. The only additions are the `rel` tags, which
// gorm ignores and rx-crud reads — one struct, two libraries.
type Team struct {
	gorm.Model
	Name string `gorm:"size:120"`

	Members []Member `gorm:"foreignKey:TeamID" rel:"has_many"`
	Labels  []Label  `gorm:"many2many:team_labels;" rel:"many_to_many,join=team_labels"`
}

type Member struct {
	gorm.Model
	TeamID uint
	Name   string `gorm:"size:120"`
	Age    *int

	Team *Team `gorm:"foreignKey:TeamID" rel:"belongs_to"`
}

type Label struct {
	gorm.Model
	Slug string `gorm:"size:64"`
}

// LabelCreations counts the gorm callbacks that fired. docs/gorm.md §16 says
// gorm hooks do not run on rx-crud writes — rx-crud sends one statement to the
// driver and never goes through gorm's callback chain — and no model in the
// tree declared a hook, so the claim was pinned by nothing.
var LabelCreations atomic.Int64

// BeforeCreate is an ordinary gorm hook: it counts, and it defaults the slug, so
// a test can see whether it ran from the row itself and not only from a counter.
func (l *Label) BeforeCreate(*gorm.DB) error {
	LabelCreations.Add(1)
	if l.Slug == "" {
		l.Slug = "defaulted by the hook"
	}
	return nil
}
