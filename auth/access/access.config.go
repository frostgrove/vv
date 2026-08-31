package access

import "time"

type Config struct {
	Session  SessionConfig  `yaml:"session"`
	Password PasswordConfig `yaml:"password"`

	Clock Clock `yaml:"-"`
}

type SessionConfig struct {
	TTL time.Duration `yaml:"ttl" env:"ACCESS_SESSION_TTL" env-default:"720h"`

	IdleTTL time.Duration `yaml:"idle_ttl" env:"ACCESS_SESSION_IDLE_TTL" env-default:"168h"`

	TouchInterval time.Duration `yaml:"touch_interval" env:"ACCESS_SESSION_TOUCH_INTERVAL" env-default:"5m"`
}

type PasswordConfig struct {
	MinLength int `yaml:"min_length" env:"ACCESS_PASSWORD_MIN_LENGTH" env-default:"10"`
}

const (
	DefaultSessionTTL        = 30 * 24 * time.Hour
	DefaultIdleTTL           = 7 * 24 * time.Hour
	DefaultTouchInterval     = 5 * time.Minute
	DefaultMinPasswordLength = 10
)

type Clock func() time.Time

func (this Config) Now() time.Time {
	if this.Clock == nil {
		return time.Now()
	}
	return this.Clock()
}

func (this Config) Sessions() SessionConfig {
	settings := this.Session
	if settings.TTL <= 0 {
		settings.TTL = DefaultSessionTTL
	}
	if settings.IdleTTL < 0 {
		settings.IdleTTL = DefaultIdleTTL
	}
	if settings.TouchInterval <= 0 {
		settings.TouchInterval = DefaultTouchInterval
	}
	return settings
}

func (this Config) MinPasswordLength() int {
	if this.Password.MinLength > 0 {
		return this.Password.MinLength
	}
	return DefaultMinPasswordLength
}
