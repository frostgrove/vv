module github.com/frostgrove/vv/jobs/jobsredis

go 1.26.6

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/frostgrove/vv v0.0.0-00010101000000-000000000000
	github.com/redis/go-redis/v9 v9.7.3
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
)

replace github.com/frostgrove/vv => ../..
