// Package query is the wire DSL: one JSON (or query-string) document that
// compiles into crud.Options. It is what turns a repository into an HTTP API
// without writing a line of per-model code.
//
//	POST /articles/query
//	{
//	  "page": 2, "limit": 20,
//	  "sort": ["-createdAt", "author.name"],
//	  "preload": ["author", "comments.author"],
//	  "search": "generics", "searchFields": ["title", "body"],
//	  "filter": {
//	    "views":       {"gte": 100},
//	    "author.name": {"contains": "an"},
//	    "tags.slug":   {"in": ["go", "rust"]},
//	    "or": [ {"status": "draft"}, {"publishedAt": {"isNull": true}} ]
//	  }
//	}
//
// Every field reference — in filters, sorts, projections and preloads — may walk
// relations, and every one of them is resolved against the model schema before
// any SQL is built. An unknown path is a 400, never a silently ignored clause.
package query

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Request is the parsed query document.
type Request struct {
	Page   int `json:"page,omitempty"`
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`

	Sort    Sorts    `json:"sort,omitempty"`
	Select  Strings  `json:"select,omitempty"`
	Preload Preloads `json:"preload,omitempty"`
	Filter  Filter   `json:"filter,omitzero"`
	// Terms is the flat filter form, ANDed with Filter.
	Terms []Term `json:"terms,omitempty"`

	Search       string  `json:"search,omitempty"`
	SearchFields Strings `json:"searchFields,omitempty"`

	Unpaged   bool `json:"unpaged,omitempty"`
	SkipTotal bool `json:"skipTotal,omitempty"`
	Distinct  bool `json:"distinct,omitempty"`
}

// Strings accepts either a JSON array or a single comma-separated string, so
// `"select": "id,title"` and `"select": ["id","title"]` both work.
type Strings []string

func (s *Strings) UnmarshalJSON(b []byte) error {
	b = trim(b)
	if isNull(b) {
		*s = nil
		return nil
	}
	if b[0] == '"' {
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*s = splitList(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	out := make(Strings, 0, len(many))
	for _, v := range many {
		out = append(out, splitList(v)...)
	}
	*s = out
	return nil
}

// Sort is one ordering term. `-field` means descending.
type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
	// Nulls is "first" or "last"; empty leaves it to the database.
	Nulls string `json:"nulls,omitempty"`
}

// Sorts accepts "-createdAt", ["-createdAt","title"] or the object form.
type Sorts []Sort

func (s *Sorts) UnmarshalJSON(b []byte) error {
	b = trim(b)
	if isNull(b) {
		*s = nil
		return nil
	}
	switch b[0] {
	case '"':
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*s = parseSortList(splitList(one))
		return nil
	case '{':
		var one Sort
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*s = Sorts{one}
		return nil
	case '[':
		var raw []json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		out := make(Sorts, 0, len(raw))
		for _, item := range raw {
			item = trim(item)
			if len(item) == 0 {
				continue
			}
			if item[0] == '{' {
				var one Sort
				if err := json.Unmarshal(item, &one); err != nil {
					return err
				}
				out = append(out, one)
				continue
			}
			var str string
			if err := json.Unmarshal(item, &str); err != nil {
				return err
			}
			out = append(out, parseSortList(splitList(str))...)
		}
		*s = out
		return nil
	}
	return fmt.Errorf("query: sort must be a string, an object or an array, got %s", b)
}

func parseSortList(items []string) Sorts {
	out := make(Sorts, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		switch item[0] {
		case '-':
			out = append(out, Sort{Field: item[1:], Desc: true})
		case '+':
			out = append(out, Sort{Field: item[1:]})
		default:
			out = append(out, Sort{Field: item})
		}
	}
	return out
}

// Preload is one relation to load, optionally narrowed.
type Preload struct {
	Path   string `json:"path"`
	Filter Filter `json:"filter,omitempty"`
	Sort   Sorts  `json:"sort,omitempty"`
}

// Preloads accepts "author", ["author","comments.author"] or the object form
// with a per-relation filter.
type Preloads []Preload

func (p *Preloads) UnmarshalJSON(b []byte) error {
	b = trim(b)
	if isNull(b) {
		*p = nil
		return nil
	}
	switch b[0] {
	case '"':
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*p = pathsToPreloads(splitList(one))
		return nil
	case '{':
		var one Preload
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*p = Preloads{one}
		return nil
	case '[':
		var raw []json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		var out Preloads
		for _, item := range raw {
			item = trim(item)
			if len(item) == 0 {
				continue
			}
			if item[0] == '{' {
				var one Preload
				if err := json.Unmarshal(item, &one); err != nil {
					return err
				}
				out = append(out, one)
				continue
			}
			var str string
			if err := json.Unmarshal(item, &str); err != nil {
				return err
			}
			out = append(out, pathsToPreloads(splitList(str))...)
		}
		*p = out
		return nil
	}
	return fmt.Errorf("query: preload must be a string, an object or an array, got %s", b)
}

func pathsToPreloads(paths []string) Preloads {
	out := make(Preloads, 0, len(paths))
	for _, p := range paths {
		if p != "" {
			out = append(out, Preload{Path: p})
		}
	}
	return out
}

// Filter holds the filter document untouched until it is compiled against a
// schema, so errors can point at the exact path that was wrong.
type Filter struct{ raw json.RawMessage }

func (f *Filter) UnmarshalJSON(b []byte) error {
	f.raw = append(f.raw[:0], b...)
	return nil
}

func (f Filter) MarshalJSON() ([]byte, error) {
	if len(f.raw) == 0 {
		return []byte("null"), nil
	}
	return f.raw, nil
}

// IsZero reports an absent filter, and makes `json:",omitzero"` work.
func (f Filter) IsZero() bool { return len(trim(f.raw)) == 0 || isNull(trim(f.raw)) }

// RawFilter builds a Filter from a JSON document, for tests and for callers
// that assemble the document themselves.
func RawFilter(doc string) Filter { return Filter{raw: json.RawMessage(doc)} }

func splitList(s string) []string {
	if !strings.ContainsAny(s, ",") {
		if s = strings.TrimSpace(s); s == "" {
			return nil
		}
		return []string{s}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func trim(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

func isNull(b []byte) bool { return len(b) == 4 && string(b) == "null" }
