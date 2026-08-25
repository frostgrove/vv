package crudhttp

import "github.com/shardit-io/vv/port"

// Repository is everything a transport binding needs. crud.Repo[M, ID, U]
// satisfies it, and so does specs.Repo and any struct that embeds either —
// which is how a service layer with extra checks takes the repository's place.
//
// A generic alias is the same type, so a repository written against this one
// and one written against port.Repository are interchangeable ([[D-022]]).
type Repository[M any, ID comparable, U any] = port.Repository[M, ID, U]
