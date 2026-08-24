package query

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/shardit-io/rx/crud"
)

// Term is the flat filter form: one field, one operator, one or more values as
// text. It is what a query string carries, and it is also a perfectly good JSON
// shape when a client would rather not nest:
//
//	?f=title:contains:go&f=views:gte:100&f=status:in:draft,live
//	{"terms": [{"path": "views", "op": "gte", "values": ["100"]}]}
//
// Terms are ANDed with each other and with the structured filter.
type Term struct {
	Path   string  `json:"path"`
	Op     string  `json:"op,omitempty"`
	Values Strings `json:"values,omitempty"`
}

// ParseTerm reads the `field:op:value` triple. The value keeps every colon
// after the second one, so timestamps survive. Two segments mean equality —
// implicit "contains" would be convenient and occasionally very wrong.
func ParseTerm(s string) (Term, error) {
	parts := strings.SplitN(s, ":", 3)
	switch len(parts) {
	case 2:
		return Term{Path: strings.TrimSpace(parts[0]), Op: "eq", Values: splitList(parts[1])}, nil
	case 3:
		return Term{
			Path:   strings.TrimSpace(parts[0]),
			Op:     strings.TrimSpace(parts[1]),
			Values: splitList(parts[2]),
		}, nil
	default:
		return Term{}, errf("filter", "%q is not field:op:value", s)
	}
}

// compileTerms turns the flat form into predicates, coercing each text value to
// the column's Go type.
func (c *compiler) terms(terms []Term) (crud.Predicate, error) {
	var preds []crud.Predicate
	for _, t := range terms {
		f, canonical, err := c.path(t.Path, "filter")
		if err != nil {
			return nil, err
		}
		if !allowed(c.cfg.filterable(), canonical) {
			return nil, errf("filter", "%s is not filterable", canonical)
		}
		op := t.Op
		if op == "" {
			op = "eq"
		}
		kind, ok := normalizeOp(op)
		if !ok {
			return nil, errf("filter."+canonical, "unknown operator %q", t.Op)
		}
		if err := c.count("filter"); err != nil {
			return nil, err
		}

		switch {
		case kind.unary():
			want := true
			if len(t.Values) > 0 {
				want, _ = strconv.ParseBool(t.Values[0])
			}
			if (kind == opIsNull) == want {
				preds = append(preds, crud.IsNull(canonical))
			} else {
				preds = append(preds, crud.IsNotNull(canonical))
			}

		case kind.textual():
			if len(t.Values) == 0 {
				return nil, errf("filter."+canonical, "%s needs a value", op)
			}
			preds = append(preds, buildText(canonical, kind, t.Values[0]))

		case kind.multi():
			vals, err := c.coerceAll(t.Values, f, canonical)
			if err != nil {
				return nil, err
			}
			p, err := buildMulti(canonical, kind, vals, "filter."+canonical)
			if err != nil {
				return nil, err
			}
			preds = append(preds, p)

		default:
			if len(t.Values) == 0 {
				return nil, errf("filter."+canonical, "%s needs a value", op)
			}
			vals, err := c.coerceAll(t.Values[:1], f, canonical)
			if err != nil {
				return nil, err
			}
			preds = append(preds, buildScalar(canonical, kind, vals[0]))
		}
	}
	switch len(preds) {
	case 0:
		return nil, nil
	case 1:
		return preds[0], nil
	default:
		return crud.And(preds...), nil
	}
}

func (c *compiler) coerceAll(raw []string, f *crud.Field, canonical string) ([]any, error) {
	t := crud.ElemType(f.Type)
	out := make([]any, 0, len(raw))
	for _, s := range raw {
		if s == "null" {
			out = append(out, nil)
			continue
		}
		v, err := coerceString(s, t)
		if err != nil {
			return nil, errf("filter."+canonical, "%q is not a valid %s", s, t)
		}
		out = append(out, v)
	}
	return out, nil
}

// ParseQuery reads a request out of a URL query string:
//
//	?page=2&limit=20&sort=-createdAt,author.name&preload=author,comments.author
//	&select=id,title&search=go&searchFields=title,body
//	&f=views:gte:100&f=tags.slug:in:go,rust
//	&filter={"or":[{"status":"draft"},{"publishedAt":{"isNull":true}}]}
//
// `filter` takes the full JSON document for anything the flat form cannot say.
func ParseQuery(v url.Values) (*Request, error) {
	if err := checkParams(v); err != nil {
		return nil, err
	}
	r := &Request{}

	num := func(keys ...string) (int, error) {
		for _, k := range keys {
			if s := v.Get(k); s != "" {
				n, err := strconv.Atoi(s)
				if err != nil {
					return 0, errf(k, "%q is not a number", s)
				}
				return n, nil
			}
		}
		return 0, nil
	}
	flag := func(keys ...string) bool {
		for _, k := range keys {
			if s := v.Get(k); s != "" {
				b, err := strconv.ParseBool(s)
				return err == nil && b
			}
		}
		return false
	}

	var err error
	if r.Page, err = num("page"); err != nil {
		return nil, err
	}
	if r.Limit, err = num("limit", "perPage", "per_page", "per-page", "pageSize"); err != nil {
		return nil, err
	}
	if r.Offset, err = num("offset"); err != nil {
		return nil, err
	}
	r.Unpaged = flag("unpaged", "all")
	r.SkipTotal = flag("skipTotal", "skip_total", "noTotal")
	r.Distinct = flag("distinct")

	r.Sort = parseSortList(multi(v, "sort", "sorts", "orderBy", "order_by"))
	r.Select = Strings(multi(v, "select", "fields"))
	r.Preload = pathsToPreloads(multi(v, "preload", "preloads", "with", "include"))
	r.After = strings.TrimSpace(firstOf(v, "after", "afterCursor"))
	r.Before = strings.TrimSpace(firstOf(v, "before", "beforeCursor"))
	r.Search = strings.TrimSpace(firstOf(v, "search", "q"))
	r.SearchFields = Strings(multi(v, "searchFields", "search_fields", "search-fields"))

	for _, raw := range append(v["f"], v["filters"]...) {
		for _, one := range strings.Split(raw, "|") {
			if one = strings.TrimSpace(one); one == "" {
				continue
			}
			t, err := ParseTerm(one)
			if err != nil {
				return nil, err
			}
			r.Terms = append(r.Terms, t)
		}
	}
	// Whatever the parameter holds goes to the compiler, which knows how to say
	// no. Keeping only the documents that look like objects would turn a
	// malformed filter into an unfiltered answer — the one failure a client
	// cannot see.
	if doc := strings.TrimSpace(v.Get("filter")); doc != "" {
		r.Filter = RawFilter(doc)
	}
	return r, nil
}

// queryParams is every spelling ParseQuery answers to. Used only to recognise a
// typo of one; a name that is nothing like any of these belongs to the
// application and is none of this package's business.
var queryParams = []string{
	"page", "limit", "perPage", "per_page", "per-page", "pageSize", "offset",
	"unpaged", "all", "skipTotal", "skip_total", "noTotal", "distinct",
	"sort", "sorts", "orderBy", "order_by", "select", "fields",
	"preload", "preloads", "with", "include",
	"search", "q", "searchFields", "search_fields", "search-fields",
	"after", "afterCursor", "before", "beforeCursor",
	"f", "filters", "filter",
}

// checkParams refuses a parameter that is one edit away from one of ours.
//
// The whole set cannot be closed the way the JSON document's can: a handler is
// free to read its own parameters off the same URL — `?includeArchived=1`
// driving a WithScope option is the documented pattern — so an unknown name has
// to pass. But `?filtr=…` is not somebody's parameter, it is ours misspelled,
// and left alone it answers 200 with the whole table.
//
// Short names are skipped. `q` and `f` are one edit from most single letters,
// and an application's `?a=1` is not a typo of anything.
func checkParams(v url.Values) error {
	for name := range v {
		if len(name) < 4 || knownParam(name) {
			continue
		}
		for _, known := range queryParams {
			if len(known) >= 4 && isOneTypoAway(name, known) {
				return errf(name, "no such parameter; did you mean %q", known)
			}
		}
	}
	return nil
}

func knownParam(name string) bool {
	for _, k := range queryParams {
		if k == name {
			return true
		}
	}
	return false
}

// isOneTypoAway reports whether one insertion, deletion, substitution or
// transposition of adjacent characters turns a into b.
//
// Transposition is in there because it is the typo people actually make —
// "prelaod" for "preload" — and without it that one is two substitutions away
// and sails through.
func isOneTypoAway(a, b string) bool {
	switch d := len(a) - len(b); {
	case d == 0:
		first := -1
		diff := 0
		for i := range a {
			if a[i] != b[i] {
				if diff == 0 {
					first = i
				}
				diff++
				if diff > 2 {
					return false
				}
			}
		}
		if diff == 1 {
			return true
		}
		// Two differences are a typo only when they are adjacent and swapped.
		return diff == 2 && first+1 < len(a) &&
			a[first] == b[first+1] && a[first+1] == b[first] &&
			a[first+2:] == b[first+2:]
	case d == 1:
		return isOneShorter(b, a)
	case d == -1:
		return isOneShorter(a, b)
	}
	return false
}

// isOneShorter reports whether short becomes long by inserting one byte.
func isOneShorter(short, long string) bool {
	for i := 0; i < len(short); i++ {
		if short[i] != long[i] {
			return short[i:] == long[i+1:]
		}
	}
	return true
}

// multi collects a repeated or comma-separated parameter under any of its
// accepted spellings.
func multi(v url.Values, keys ...string) []string {
	var out []string
	for _, k := range keys {
		for _, raw := range v[k] {
			out = append(out, splitList(raw)...)
		}
	}
	return out
}

func firstOf(v url.Values, keys ...string) string {
	for _, k := range keys {
		if s := v.Get(k); s != "" {
			return s
		}
	}
	return ""
}
