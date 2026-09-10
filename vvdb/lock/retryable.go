package lock

import (
	"sync"

	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

var standardCodes = sync.OnceValue(errs.StandardCodes)

func (this *Locks) Retryable(err error) bool { return this.RetryCode(err) != "" }

// RetryCode names the concurrency failure a second attempt fixes on its own, and answers empty for
// everything else. It is what Retry re-runs on, so a caller that reports rather than retries can
// say which of them it was.
//
// Which failures those are is not decided here. The SQLSTATE is read out of whatever the error
// carries and handed to the framework's per-dialect table, which is versioned, taken off live
// servers and covered by its own corpus. Extract walks both single and multi-error unwrapping, so
// this reads the driver's answer through the plain wrapping Take does and through the *errs.Fault a
// repository hands back — the same call, either way.
//
// Deliberately not the same question as errs.KindRetryable at the boundary: an application fault
// that asks the client to try again is not a reason to run somebody's transaction a second time.
func (this *Locks) RetryCode(err error) errs.Code {
	if err == nil {
		return ""
	}
	extracted := sqlfault.Extract(err)
	if extracted == nil || extracted.SQLState == "" {
		return ""
	}
	code, _, known := sqlerr.Classify(this.backend.dialect, extracted)
	if !known {
		return ""
	}
	if kind, defined := standardCodes().KindOf(code); !defined || kind != errs.KindRetryable {
		return ""
	}
	return code
}
