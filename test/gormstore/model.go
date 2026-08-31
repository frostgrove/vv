package gormstore

//go:generate go run github.com/frostgrove/vv/cmd/vv -readonly UpdatedAt,DeletedAt

import (
	"sync/atomic"

	"gorm.io/gorm"
)

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

var LabelCreations atomic.Int64

func (this *Label) BeforeCreate(*gorm.DB) error {
	LabelCreations.Add(1)
	if this.Slug == "" {
		this.Slug = "defaulted by the hook"
	}
	return nil
}
