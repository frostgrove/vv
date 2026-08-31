package query

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/crud"
)

type Term struct {
	Path   string  `json:"path"`
	Op     string  `json:"op,omitempty"`
	Values Strings `json:"values,omitempty"`

	flat    bool
	flatRaw string

	jsonValues []termValue
}

func (this *Term) UnmarshalJSON(b []byte) error {
	type wire struct {
		Path   string          `json:"path"`
		Op     string          `json:"op"`
		Values json.RawMessage `json:"values"`
	}
	var in wire
	if err := decodeObject(b, &in, "terms", termKeys); err != nil {
		return err
	}
	*this = Term{Path: in.Path, Op: in.Op}
	if len(in.Values) == 0 || isNull(trim(in.Values)) {
		return nil
	}
	var rawValues []json.RawMessage
	if err := json.Unmarshal(in.Values, &rawValues); err != nil {
		return fmt.Errorf("query: term values must be an array of strings: %w", err)
	}
	this.Values = make(Strings, len(rawValues))
	this.jsonValues = make([]termValue, len(rawValues))
	for i, raw := range rawValues {
		raw = trim(raw)
		if isNull(raw) {
			this.jsonValues[i] = termValue{null: true}
			continue
		}
		if err := json.Unmarshal(raw, &this.Values[i]); err != nil {
			return fmt.Errorf("query: terms.values[%d] must be a string or null: %w", i, err)
		}
		this.jsonValues[i] = termValue{text: this.Values[i]}
	}
	return nil
}

func (this Term) MarshalJSON() ([]byte, error) {
	type wire struct {
		Path   string `json:"path"`
		Op     string `json:"op,omitempty"`
		Values []any  `json:"values,omitempty"`
	}
	w := wire{Path: this.Path, Op: this.Op}
	if this.jsonValues != nil {
		w.Values = make([]any, len(this.jsonValues))
		for i, value := range this.jsonValues {
			if !value.null {
				w.Values[i] = value.text
			}
		}
	} else if len(this.Values) != 0 {
		w.Values = make([]any, len(this.Values))
		for i, value := range this.Values {
			w.Values[i] = value
		}
	}
	return json.Marshal(w)
}

var termKeys = []string{"path", "op", "values"}

func ParseTerm(s string) (Term, error) {
	parts := strings.SplitN(s, ":", 3)
	switch len(parts) {
	case 2:
		raw := strings.TrimSpace(parts[1])
		return Term{Path: strings.TrimSpace(parts[0]), Op: "eq", Values: splitList(raw), flat: true, flatRaw: raw}, nil
	case 3:
		raw := strings.TrimSpace(parts[2])
		return Term{
			Path:    strings.TrimSpace(parts[0]),
			Op:      strings.TrimSpace(parts[1]),
			Values:  splitList(raw),
			flat:    true,
			flatRaw: raw,
		}, nil
	default:
		return Term{}, errf("filter", "%q is not field:op:value", s)
	}
}

func (this *compiler) terms(terms []Term) (crud.Predicate, error) {
	var preds []crud.Predicate
	for _, t := range terms {
		f, canonical, err := this.path(t.Path, "filter")
		if err != nil {
			return nil, err
		}
		if !allowed(this.config.filterable(), canonical) {
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
		if err := this.count("filter"); err != nil {
			return nil, err
		}
		values := t.values(kind.multi())

		switch {
		case kind.unary():
			want := true
			if len(values) > 1 {
				return nil, errf("filter."+canonical, "%s accepts at most one boolean value", op)
			}
			if len(values) == 1 {
				var err error
				want, err = strconv.ParseBool(values[0].text)
				if err != nil {
					return nil, errf("filter."+canonical, "%s expects true or false", op)
				}
			}
			if (kind == opIsNull) == want {
				preds = append(preds, crud.IsNull(canonical))
			} else {
				preds = append(preds, crud.IsNotNull(canonical))
			}

		case kind.textual():
			if len(values) == 0 {
				return nil, errf("filter."+canonical, "%s needs a value", op)
			}
			if len(values) > 1 {
				return nil, errf("filter."+canonical, "%s accepts exactly one value", op)
			}
			if crud.ElemType(f.Type).Kind() != reflect.String {
				return nil, errf("filter."+canonical, "%s requires a text field", op)
			}
			if err := this.countBinds(1, "filter."+canonical); err != nil {
				return nil, err
			}
			preds = append(preds, buildText(canonical, kind, values[0].text))

		case kind.multi():
			if err := this.countValues(len(values), "filter."+canonical); err != nil {
				return nil, err
			}
			vals, err := this.coerceTerms(values, f, canonical)
			if err != nil {
				return nil, err
			}
			p, err := buildMulti(canonical, kind, vals, "filter."+canonical)
			if err != nil {
				return nil, err
			}
			preds = append(preds, p)

		default:
			if len(values) == 0 {
				return nil, errf("filter."+canonical, "%s needs a value", op)
			}
			if len(values) > 1 {
				return nil, errf("filter."+canonical, "%s accepts exactly one value", op)
			}
			if values[0].null && kind != opEq && kind != opNe {
				return nil, errf("filter."+canonical, "%s has no meaning with null", op)
			}
			vals, err := this.coerceTerms(values[:1], f, canonical)
			if err != nil {
				return nil, err
			}
			if vals[0] != nil {
				if err := this.countBinds(1, "filter."+canonical); err != nil {
					return nil, err
				}
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

func (this *compiler) coerceTerms(raw []termValue, f *crud.Field, canonical string) ([]any, error) {
	t := crud.ElemType(f.Type)
	out := make([]any, 0, len(raw))
	for _, value := range raw {
		if value.null {
			out = append(out, nil)
			continue
		}
		v, err := coerceString(value.text, t)
		if err != nil {
			return nil, errf("filter."+canonical, "%q is not %s", value.text, wanted(t))
		}
		out = append(out, v)
	}
	return out, nil
}

type termValue struct {
	text string
	null bool
}

func (this Term) values(split bool) []termValue {
	if !this.flat {
		if this.jsonValues != nil {
			return append([]termValue(nil), this.jsonValues...)
		}
		out := make([]termValue, len(this.Values))
		for i, value := range this.Values {
			out[i] = termValue{text: value}
		}
		return out
	}
	if this.flatRaw == "" {
		return nil
	}
	var out []termValue
	out = append(out, parseTermValues(this.flatRaw, split)...)
	return out
}

func parseTermValues(s string, split bool) []termValue {
	var out []termValue
	var b strings.Builder
	escapedValue := false
	flush := func() {
		text := b.String()
		out = append(out, termValue{text: text, null: text == "null" && !escapedValue})
		b.Reset()
		escapedValue = false
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' {
			if i+1 < len(runes) {
				next := runes[i+1]
				switch next {
				case ',', '|', '\\':
					b.WriteRune(next)
					escapedValue = true
					i++
					continue
				case 'n':
					if i+4 < len(runes) && string(runes[i+1:i+5]) == "null" {
						b.WriteString("null")
						escapedValue = true
						i += 4
						continue
					}
				}
			}

			b.WriteRune(r)
			continue
		}
		if split && r == ',' {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return out
}

func ParseQuery(v url.Values) (*Request, error) {
	if err := checkParams(v); err != nil {
		return nil, err
	}
	r := &Request{}

	num := func(keys ...string) (int, error) {
		s, key, present, err := scalar(v, keys...)
		if err != nil || !present {
			return 0, err
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, errf(key, "%q is not a number", s)
		}
		return n, nil
	}

	flag := func(keys ...string) (bool, string, error) {
		s, key, present, err := scalar(v, keys...)
		if err != nil || !present {
			return false, key, err
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			return false, key, errf(key, "%q is not a boolean", s)
		}
		return b, key, nil
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
	if r.Unpaged, r.unpagedParam, err = flag("unpaged", "all"); err != nil {
		return nil, err
	}
	if r.SkipTotal, _, err = flag("skipTotal", "skip_total", "noTotal"); err != nil {
		return nil, err
	}
	if r.Distinct, _, err = flag("distinct"); err != nil {
		return nil, err
	}

	r.Sort = parseSortList(multi(v, "sort", "sorts", "orderBy", "order_by"))
	r.Select = Strings(multi(v, "select", "fields"))
	r.Preload = pathsToPreloads(multi(v, "preload", "preloads", "with", "include"))
	if after, key, present, err := scalar(v, "after", "afterCursor"); err != nil {
		return nil, err
	} else if present {
		r.After = strings.TrimSpace(after)
		if r.After == "" {
			return nil, errf(key, "must not be empty")
		}
	}
	if before, key, present, err := scalar(v, "before", "beforeCursor"); err != nil {
		return nil, err
	} else if present {
		r.Before = strings.TrimSpace(before)
		if r.Before == "" {
			return nil, errf(key, "must not be empty")
		}
	}
	if r.After != "" && r.Before != "" {
		return nil, errf("after", "cannot be combined with before")
	}
	if search, _, present, err := scalar(v, "search", "q"); err != nil {
		return nil, err
	} else if present {
		r.Search = strings.TrimSpace(search)
	}
	r.SearchFields = Strings(multi(v, "searchFields", "search_fields", "search-fields"))

	for _, raw := range append(v["f"], v["filters"]...) {
		for _, one := range splitFlatTerms(raw) {
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

	if doc, key, present, err := scalar(v, "filter"); err != nil {
		return nil, err
	} else if present {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			return nil, errf(key, "must not be empty")
		}
		r.Filter = RawFilter(doc)
	}
	return r, nil
}

func splitFlatTerms(raw string) []string {
	var out []string
	start := 0
	escaped := false
	for i, r := range raw {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|' && looksLikeFlatTerm(raw[i+1:]):
			out = append(out, raw[start:i])
			start = i + 1
		}
	}
	out = append(out, raw[start:])
	return out
}

func looksLikeFlatTerm(s string) bool {
	if s == "" {
		return false
	}
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			return false
		case r == ':':
			return strings.TrimSpace(s[:strings.IndexRune(s, ':')]) != ""
		}
	}
	return false
}

func scalar(v url.Values, keys ...string) (value, key string, present bool, err error) {
	for _, k := range keys {
		values, ok := v[k]
		if !ok {
			continue
		}
		if len(values) != 1 {
			return "", k, true, errf(k, "must occur exactly once")
		}
		if present {
			return "", k, true, errf(k, "conflicts with %s", key)
		}
		value, key, present = values[0], k, true
	}
	return value, key, present, nil
}

var queryParams = []string{
	"page", "limit", "perPage", "per_page", "per-page", "pageSize", "offset",
	"unpaged", "all", "skipTotal", "skip_total", "noTotal", "distinct",
	"sort", "sorts", "orderBy", "order_by", "select", "fields",
	"preload", "preloads", "with", "include",
	"search", "q", "searchFields", "search_fields", "search-fields",
	"after", "afterCursor", "before", "beforeCursor",
	"f", "filters", "filter",
}

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

func isOneShorter(short, long string) bool {
	for i := 0; i < len(short); i++ {
		if short[i] != long[i] {
			return short[i:] == long[i+1:]
		}
	}
	return true
}

func multi(v url.Values, keys ...string) []string {
	var out []string
	for _, k := range keys {
		for _, raw := range v[k] {
			out = append(out, splitList(raw)...)
		}
	}
	return out
}
