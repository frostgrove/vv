package crudhttp

import "github.com/shardit-io/vv/port"

// Repository is everything a transport binding needs. crud.Repo[M, ID, U]
// satisfies it, and so does specs.Repo and any struct that embeds either —
// which is how a service layer with extra checks takes the repository's place.
//
// A generic alias is the same type, so a repository written against this one
// and one written against port.Repository are interchangeable ([[D-022]]).
type Repository[M any, ID comparable, U any] = port.Repository[M, ID, U]

// Rules are the parts of a handler's configuration that say nothing about a
// transport: what a client may ask for, what it may not choose, and how much may
// arrive at once.
//
// It is an alias and not a hop over something that moved. port.Rules never lived
// here — it is a generic alias so the two names are the same type, and this one
// exists because an HTTP binding already imports crudhttp and would otherwise
// import port for one embedded field. crudgrpc, which does not import crudhttp,
// embeds port.Rules directly; both spell the same struct.
type Rules = port.Rules
