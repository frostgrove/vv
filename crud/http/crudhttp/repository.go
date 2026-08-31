package crudhttp

import "github.com/frostgrove/vv/port"

type Repository[M any, ID comparable, U any] = port.Repository[M, ID, U]

type Rules = port.Rules
