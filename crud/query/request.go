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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

	// After and Before page by cursor: an opaque string a previous page handed
	// back. They replace page/offset rather than adding to them.
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`

	Unpaged   bool `json:"unpaged,omitempty"`
	SkipTotal bool `json:"skipTotal,omitempty"`
	Distinct  bool `json:"distinct,omitempty"`

	// unpagedParam is the query-string spelling that set Unpaged — "unpaged" or
	// its alias "all" — so a refusal names the key the client sent rather than
	// the one this struct calls it.
	//
	// Unexported, so it is not part of the document: a JSON body has one
	// spelling and needs no record of it, and an exported field would put a
	// query-string detail on the wire shape.
	unpagedParam string

	// afterSet and beforeSet retain JSON object-key presence. An empty cursor is
	// invalid just as `?after=` is invalid, but a Go Request with Before set and
	// After left at its zero value is a perfectly ordinary backwards page. The
	// value alone cannot distinguish those two cases.
	afterSet  bool
	beforeSet bool

	// omitPaging is set by a transport that compiles this document for an
	// operation whose own contract supplies cardinality (COUNT and GetByID).
	// It is not JSON: a client may not turn the endpoint's hard page budget off.
	omitPaging bool
}

// UnpagedParam is the request parameter that asked for unpaged results, for a
// caller building its own refusal. It is "unpaged" for a JSON document and for
// a query string that spelled it that way, and "all" for the alias.
func (this *Request) UnpagedParam() string {
	if this == nil || this.unpagedParam == "" {
		return "unpaged"
	}
	return this.unpagedParam
}

// OmitPaging marks this request for an operation that does not use list
// pagination, such as a count or a lookup by primary key. It is for transport
// adapters; it never appears on the wire and cannot be selected by a client.
func (this *Request) OmitPaging() {
	if this != nil {
		this.omitPaging = true
	}
}

// ClearCursors removes cursor controls, including the JSON presence markers.
// Count and entity endpoints call it because a cursor has no meaning there;
// keeping only the private marker would turn an intentionally removed cursor
// into a misleading "must not be empty" refusal.
func (this *Request) ClearCursors() {
	if this != nil {
		this.After, this.Before = "", ""
		this.afterSet, this.beforeSet = false, false
	}
}

// UnmarshalJSON refuses a key this document does not define.
//
// Every field reference *inside* the document is resolved against the model and
// an unknown one is a 400 — but that check starts one level too deep. A client
// that writes "filtr" instead of "filter" produces a document with no filter at
// all, and the endpoint answers 200 with every row in the table. That is the one
// failure a client cannot see, and it is the failure the strictness inside the
// document exists to prevent, so the document's own keys are held to it too.
func (this *Request) UnmarshalJSON(b []byte) error {
	b = trim(b)
	if len(b) == 0 {
		return errf("", "document must be a JSON object")
	}
	if isNull(b) {
		return errf("", "document must be a JSON object, not null")
	}
	if b[0] != '{' {
		return errf("", "document must be a JSON object")
	}
	if err := rejectDuplicateJSONKeys(b); err != nil {
		return err
	}
	// A distinct type so the decoder does not call this method again. The field
	// types keep their own unmarshallers.
	type document Request
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()

	var doc document
	if err := dec.Decode(&doc); err != nil {
		if key, ok := unknownFieldOf(err); ok {
			return errf(key, "no such option; the document accepts %s", strings.Join(requestKeys, ", "))
		}
		return err
	}
	if err := requireJSONEOF(dec); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	for key, raw := range keys {
		if isNull(trim(raw)) {
			return errf(key, "must not be null")
		}
	}
	_, doc.afterSet = keys["after"]
	_, doc.beforeSet = keys["before"]
	*this = Request(doc)
	return nil
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("query: document must contain one JSON value")
		}
		return err
	}
	return nil
}

func decodeObject(b []byte, destination any, where string, keys []string) error {
	if err := rejectDuplicateJSONKeys(b); err != nil {
		return err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(b, &values); err != nil {
		return err
	}
	for key, raw := range values {
		if isNull(trim(raw)) {
			path := key
			if where != "" {
				path = where + "." + key
			}
			return errf(path, "must not be null")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(destination); err != nil {
		if key, ok := unknownFieldOf(err); ok {
			path := key
			if where != "" {
				path = where + "." + key
			}
			return errf(path, "no such option; this object accepts %s", strings.Join(keys, ", "))
		}
		return err
	}
	return requireJSONEOF(dec)
}

// rejectDuplicateJSONKeys closes the last-wins hole in encoding/json. Query
// documents are policy, not a convenient map: accepting both values of a key
// lets a proxy, logger or signature checker see a different filter than the
// decoder that executes it.
func rejectDuplicateJSONKeys(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := rejectDuplicateJSONValue(dec, ""); err != nil {
		return err
	}
	return requireJSONEOF(dec)
}

func rejectDuplicateJSONValue(dec *json.Decoder, where string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("query: JSON object key is not a string")
			}
			path := key
			if where != "" {
				path = where + "." + key
			}
			if _, duplicate := seen[key]; duplicate {
				return errf(path, "duplicate key")
			}
			seen[key] = struct{}{}
			if err := rejectDuplicateJSONValue(dec, path); err != nil {
				return err
			}
		}
		_, err := dec.Token() // closing '}'
		return err
	case '[':
		for dec.More() {
			if err := rejectDuplicateJSONValue(dec, where); err != nil {
				return err
			}
		}
		_, err := dec.Token() // closing ']'
		return err
	default:
		return fmt.Errorf("query: malformed JSON delimiter %q", delim)
	}
}

// unknownFieldOf digs the offending key out of encoding/json's message. The
// package gives no typed error for it, and the message is stable enough to be
// worth a better diagnostic than passing it through raw.
func unknownFieldOf(err error) (string, bool) {
	const prefix = "json: unknown field "
	message := err.Error()
	i := strings.Index(message, prefix)
	if i < 0 {
		return "", false
	}
	return strings.Trim(message[i+len(prefix):], `"`), true
}

// requestKeys is what the error message offers back. Kept next to the struct so
// the two cannot drift; the test walks the struct tags to prove they agree.
var requestKeys = []string{
	"page", "limit", "offset", "sort", "select", "preload", "filter", "terms",
	"search", "searchFields", "after", "before", "unpaged", "skipTotal", "distinct",
}

// Strings accepts either a JSON array or a single comma-separated string, so
// `"select": "id,title"` and `"select": ["id","title"]` both work.
type Strings []string

func (this *Strings) UnmarshalJSON(b []byte) error {
	b = trim(b)
	if isNull(b) {
		*this = nil
		return nil
	}
	if b[0] == '"' {
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*this = splitList(one)
		return nil
	}
	var many []json.RawMessage
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	out := make(Strings, 0, len(many))
	for _, raw := range many {
		raw = trim(raw)
		if isNull(raw) {
			return fmt.Errorf("query: string list must not contain null")
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		out = append(out, splitList(v)...)
	}
	*this = out
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

func (this *Sorts) UnmarshalJSON(b []byte) error {
	b = trim(b)
	if isNull(b) {
		*this = nil
		return nil
	}
	switch b[0] {
	case '"':
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*this = parseSortList(splitList(one))
		return nil
	case '{':
		var one Sort
		if err := decodeObject(b, &one, "sort", sortKeys); err != nil {
			return err
		}
		*this = Sorts{one}
		return nil
	case '[':
		var raw []json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		out := make(Sorts, 0, len(raw))
		for _, item := range raw {
			item = trim(item)
			if len(item) == 0 || isNull(item) {
				return fmt.Errorf("query: sort list must not contain null")
			}
			if item[0] == '{' {
				var one Sort
				if err := decodeObject(item, &one, "sort", sortKeys); err != nil {
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
		*this = out
		return nil
	}
	return fmt.Errorf("query: sort must be a string, an object or an array, got %s", b)
}

var sortKeys = []string{"field", "desc", "nulls"}

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
	Path    string `json:"path"`
	Filter  Filter `json:"filter,omitzero"`
	Sort    Sorts  `json:"sort,omitempty"`
	MaxRows int    `json:"maxRows,omitzero"`
}

// Preloads accepts "author", ["author","comments.author"] or the object form
// with a per-relation filter.
type Preloads []Preload

func (this *Preloads) UnmarshalJSON(b []byte) error {
	b = trim(b)
	if isNull(b) {
		*this = nil
		return nil
	}
	switch b[0] {
	case '"':
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*this = pathsToPreloads(splitList(one))
		return nil
	case '{':
		var one Preload
		if err := decodeObject(b, &one, "preload", preloadKeys); err != nil {
			return err
		}
		*this = Preloads{one}
		return nil
	case '[':
		var raw []json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		var out Preloads
		for _, item := range raw {
			item = trim(item)
			if len(item) == 0 || isNull(item) {
				return fmt.Errorf("query: preload list must not contain null")
			}
			if item[0] == '{' {
				var one Preload
				if err := decodeObject(item, &one, "preload", preloadKeys); err != nil {
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
		*this = out
		return nil
	}
	return fmt.Errorf("query: preload must be a string, an object or an array, got %s", b)
}

var preloadKeys = []string{"path", "filter", "sort", "maxRows"}

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

func (this *Filter) UnmarshalJSON(b []byte) error {
	this.raw = append(this.raw[:0], b...)
	return nil
}

func (this Filter) MarshalJSON() ([]byte, error) {
	if len(this.raw) == 0 {
		return []byte("null"), nil
	}
	return this.raw, nil
}

// IsZero reports an absent filter, and makes `json:",omitzero"` work. A JSON
// null is present input rather than absence: Compile rejects it instead of
// treating a malformed narrowing as no narrowing at all.
func (this Filter) IsZero() bool { return len(trim(this.raw)) == 0 }

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
