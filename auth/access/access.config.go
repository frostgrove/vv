package access

import "time"

// A Config is what this context needs an operator to decide: how long a
// sign-in lasts, and how short a password may be.
//
// The tags are cleanenv's, so utils/vvcfg loads it with no glue and an
// application keeps one field for the whole context:
//
//	type Config struct {
//	    Addr   string        `yaml:"addr" env:"ADDR"`
//	    Access access.Config `yaml:"access"`
//	}
//
// Nothing here names a subject type, a role or a route. Those are the
// consumer's, and a setting for one would be this module deciding something it
// cannot see: which identities exist is a fact about the application, and it
// arrives as an argument to a use case rather than as a string in a file.
type Config struct {
	Session  SessionConfig  `yaml:"session"`
	Password PasswordConfig `yaml:"password"`

	// Clock is not loaded from anywhere — it is the test seam, set in code.
	Clock Clock `yaml:"-"`
}

// SessionConfig bounds how long one sign-in can be used for.
type SessionConfig struct {
	// TTL is absolute. Nothing moves it, which is what bounds a session's total
	// life however active the caller is.
	TTL time.Duration `yaml:"ttl" env:"ACCESS_SESSION_TTL" env-default:"720h"`
	// IdleTTL closes a session that has not been used for this long. Zero
	// disables the idle rule and leaves TTL as the only limit.
	IdleTTL time.Duration `yaml:"idle_ttl" env:"ACCESS_SESSION_IDLE_TTL" env-default:"168h"`
	// TouchInterval is how stale last_used_at may get before a request pays for
	// an UPDATE. Without it every authenticated read is also a write.
	TouchInterval time.Duration `yaml:"touch_interval" env:"ACCESS_SESSION_TOUCH_INTERVAL" env-default:"5m"`
}

// PasswordConfig is the one password rule this context enforces.
//
// Length and nothing else. A composition rule ("one digit, one symbol")
// shortens the search space it claims to widen: it makes the password people
// choose more predictable, not less. An application that wants more can check
// what it likes before it calls in — this is the floor, not the policy.
type PasswordConfig struct {
	MinLength int `yaml:"min_length" env:"ACCESS_PASSWORD_MIN_LENGTH" env-default:"10"`
}

// DefaultSessionTTL and the rest apply when a zero value arrives — a Config
// built in code rather than loaded through the tags above.
const (
	DefaultSessionTTL        = 30 * 24 * time.Hour
	DefaultIdleTTL           = 7 * 24 * time.Hour
	DefaultTouchInterval     = 5 * time.Minute
	DefaultMinPasswordLength = 10
)

// Clock is what everything in this module reads instead of time.Now.
//
// A seam and not a call, because every expiry rule here is otherwise untestable
// without sleeping. nil means the wall clock.
type Clock func() time.Time

// Now answers the configured clock's reading.
func (this Config) Now() time.Time {
	if this.Clock == nil {
		return time.Now()
	}
	return this.Clock()
}

// Sessions answers the session settings with the defaults filled in.
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

// MinPasswordLength answers the floor, with its default filled in.
func (this Config) MinPasswordLength() int {
	if this.Password.MinLength > 0 {
		return this.Password.MinLength
	}
	return DefaultMinPasswordLength
}
