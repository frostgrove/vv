// The fx wiring for the MinIO backend is its own module so that a consumer who
// constructs the backend by hand — or uses a different container — never takes
// uber/fx as a dependency. See [[D-033]] and [[D-074]].
//
// It requires the MinIO backend and fx, and nothing else.
module github.com/frostgrove/vv/storage/storageminio/storageminiofx

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-20260829132449-bc1e4c0b1038
	github.com/frostgrove/vv/storage/storageminio v0.0.0-20260829110223-5aeeda71815f
	github.com/minio/minio-go/v7 v7.3.0
	go.uber.org/fx v1.24.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	gopkg.in/ini.v1 v1.67.3 // indirect
)

// storageminio has no tag carrying EnsureBucket yet; the workspace and this
// replace are how a satellite resolves it until the next release.
replace github.com/frostgrove/vv/storage/storageminio => ..
