// The config loader is its own module because cleanenv is a dependency, and the
// root module takes none (D-033, as amended by D-036). A consumer that binds
// its own configuration pays nothing for this one existing.
module github.com/frostgrove/vv/utils/vvcfg

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-20260829132449-bc1e4c0b1038
	github.com/ilyakaznacheev/cleanenv v1.5.0
)

require (
	github.com/BurntSushi/toml v1.2.1 // indirect
	github.com/joho/godotenv v1.5.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	olympos.io/encoding/edn v0.0.0-20201019073823-d3554ca0b0a3 // indirect
)
