package revokeredis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const (
	EvictionParameter = "maxmemory-policy"

	RetainingPolicy = "noeviction"
)

type Verdict string

const (
	Retaining Verdict = "retaining"
	Evicting  Verdict = "evicting"
	Unknown   Verdict = "unknown"
)

// EvictionPolicy is what one server answered about what it does when it reaches
// maxmemory. Unknown is a third answer and not a quiet Retaining: a managed
// Redis usually forbids CONFIG, and a list that could not ask has proved
// nothing either way.
type EvictionPolicy struct {
	Verdict Verdict
	Name    string
	Reason  error
}

func (this EvictionPolicy) String() string {
	if this.Verdict == Unknown {
		return string(Unknown)
	}
	return string(this.Verdict) + " (" + this.Name + ")"
}

var (
	ErrEvicting = errors.New("the revocation list is on a server that evicts keys")

	ErrUnknownPolicy = errors.New("the server would not say what it evicts")
)

type UnknownPolicy string

const (
	Reported UnknownPolicy = "reported"
	Refused  UnknownPolicy = "refused"
)

func (this UnknownPolicy) validate() error {
	switch this {
	case "", Reported, Refused:
		return nil
	}
	return fmt.Errorf("revokeredis: an unknown %s is %q, which is neither revokeredis.Reported nor revokeredis.Refused",
		EvictionParameter, string(this))
}

func OnUnknownPolicy(choice UnknownPolicy) Option {
	return func(list *List) { list.unknown = choice }
}

func Logger(logger *slog.Logger) Option {
	return func(list *List) { list.logger = logger }
}

func (this *List) EvictionPolicy(ctx context.Context) EvictionPolicy {
	answer, err := this.client.ConfigGet(ctx, EvictionParameter).Result()
	if err != nil {
		return EvictionPolicy{Verdict: Unknown, Reason: fmt.Errorf("asking for %s: %w", EvictionParameter, err)}
	}
	name := strings.TrimSpace(answer[EvictionParameter])
	if name == "" {
		return EvictionPolicy{Verdict: Unknown, Reason: fmt.Errorf("the server answered no %s", EvictionParameter)}
	}
	if strings.EqualFold(name, RetainingPolicy) {
		return EvictionPolicy{Verdict: Retaining, Name: name}
	}
	return EvictionPolicy{Verdict: Evicting, Name: name}
}

// VerifyEvictionPolicy is the start-up check, and it belongs to whoever owns the
// lifecycle rather than to New: a constructor that reached the network would do
// it where no start timeout applies and no stop hook exists yet.
//
// A question that came back unanswered is asked a second way before it is called
// unknown. CONFIG fails identically whether an ACL forbids it or there is no
// server at all, and the second of those is not a policy nobody would state — it
// is a list that will refuse every request it is later asked. So a ping decides
// which happened, and only a server that answered gets the benefit of Reported.
func (this *List) VerifyEvictionPolicy(ctx context.Context) (EvictionPolicy, error) {
	policy := this.EvictionPolicy(ctx)
	switch policy.Verdict {
	case Evicting:
		return policy, fmt.Errorf("revokeredis: %w: %s is %q, and an evicted key reads as a session nobody revoked; only %q keeps a revocation for as long as it was written for",
			ErrEvicting, EvictionParameter, policy.Name, RetainingPolicy)
	case Unknown:
		if err := this.Ping(ctx); err != nil {
			return policy, err
		}
		if this.unknown == Refused {
			return policy, fmt.Errorf("revokeredis: %w: %w", ErrUnknownPolicy, policy.Reason)
		}
		this.report(ctx, policy)
	}
	return policy, nil
}

func (this *List) report(ctx context.Context, policy EvictionPolicy) {
	logger := this.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, "revokeredis: the eviction policy of the revocation list's server is unknown",
		"parameter", EvictionParameter,
		"reason", policy.Reason.Error(),
		"consequence", "a server that evicts hands a signed-out session back under memory pressure",
		"remedy", "confirm "+RetainingPolicy+" out of band, or set revokeredis.OnUnknownPolicy(revokeredis.Refused)")
}
