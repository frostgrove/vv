package errs_test

import (
	"reflect"
	"testing"

	"github.com/frostgrove/vv/errs"
)

type rename struct{ from, to string }

func (this rename) Resolve(p errs.Path) (errs.Path, bool) {
	out := make(errs.Path, len(p))
	copy(out, p)
	for i, s := range out {
		if !s.IsIndex && s.Name == this.from {
			out[i] = errs.Named(this.to)
			return out, true
		}
	}
	return p, false
}

func TestAChainReportsWhenAHopDeclined(t *testing.T) {
	in := errs.Path{errs.Named("email_address"), errs.Named("local")}

	t.Run("a declined hop keeps what the earlier ones did", func(t *testing.T) {
		got, ok := errs.Chain(rename{"email_address", "email"}, rename{"nothing", "here"}).Resolve(in)
		if ok {
			t.Fatalf("the chain reported success though a hop declined — nothing would ever be marked approximate")
		}
		want := errs.Path{errs.Named("email"), errs.Named("local")}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the chain returned %v, want the first hop's work %v", got, want)
		}
	})

	t.Run("every hop accepting reports success", func(t *testing.T) {
		got, ok := errs.Chain(rename{"email_address", "email"}, rename{"local", "user"}).Resolve(in)
		if !ok {
			t.Fatalf("the chain reported a decline though every hop accepted")
		}
		want := errs.Path{errs.Named("email"), errs.Named("user")}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the chain returned %v, want %v", got, want)
		}
	})

	t.Run("a nil hop is skipped", func(t *testing.T) {
		got, ok := errs.Chain(nil, rename{"email_address", "email"}, nil).Resolve(in)
		if !ok {
			t.Fatalf("a nil hop was treated as a decline")
		}
		want := errs.Path{errs.Named("email"), errs.Named("local")}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the chain returned %v, want %v", got, want)
		}
	})

	t.Run("a chain of nothing is the identity", func(t *testing.T) {
		got, ok := errs.Chain().Resolve(in)
		if !ok || !reflect.DeepEqual(got, in) {
			t.Fatalf("an empty chain returned (%v, %v)", got, ok)
		}
	})
}
