// Package gormstore is the gorm half of the integration: ordinary gorm models,
// with `rel` tags alongside the `gorm` ones so rx-crud can navigate the same
// associations. It is what docs/gorm.md tells a gorm project to do, executed.
package gormstore

//go:generate go run rx-crud/cmd/rxcrud -readonly UpdatedAt,DeletedAt

import "gorm.io/gorm"

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
