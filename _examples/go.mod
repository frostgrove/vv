// The examples live in their own module so ent, gorm, sqlx and both HTTP
// bindings never become dependencies of the library. It is not published, and
// like test/ it reaches the library from disk.
//
// The leading underscore keeps the whole tree out of `go build ./...` at the
// repository root; `make examples` builds it.
module github.com/shardit-io/qq/_examples

go 1.26

replace github.com/shardit-io/qq => ../

replace github.com/shardit-io/qq/http/crudfiber => ../http/crudfiber

replace github.com/shardit-io/qq/http/crudgin => ../http/crudgin

replace github.com/shardit-io/qq/adapter/crudpgx => ../adapter/crudpgx

require github.com/shardit-io/qq v0.0.0

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/andybalholm/brotli v1.2.2 // indirect
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/gabriel-vasile/mimetype v1.4.12 // indirect
	github.com/gin-contrib/sse v1.1.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.30.1 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/gofiber/schema v1.8.0 // indirect
	github.com/gofiber/utils/v2 v2.1.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.19.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.72.0 // indirect
	go.mongodb.org/mongo-driver/v2 v2.5.0 // indirect
	golang.org/x/arch v0.22.0 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)

require (
	ariga.io/atlas v0.36.2-0.20250730182955-2c6300d0a3e1 // indirect
	entgo.io/ent v0.14.6
	github.com/agext/levenshtein v1.2.3 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/bmatcuk/doublestar v1.3.4 // indirect
	github.com/gin-gonic/gin v1.12.0
	github.com/go-openapi/inflect v0.19.0 // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/gofiber/fiber/v3 v3.4.0
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/hcl/v2 v2.18.1 // indirect
	github.com/jackc/pgx/v5 v5.10.0
	github.com/jmoiron/sqlx v1.4.0
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/shardit-io/qq/adapter/crudpgx v0.0.0
	github.com/shardit-io/qq/http/crudfiber v0.0.0
	github.com/shardit-io/qq/http/crudgin v0.0.0
	github.com/zclconf/go-cty v1.14.4 // indirect
	github.com/zclconf/go-cty-yaml v1.1.0 // indirect
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gorm.io/driver/mysql v1.6.0
	gorm.io/driver/postgres v1.6.2
	gorm.io/gorm v1.31.2
)
