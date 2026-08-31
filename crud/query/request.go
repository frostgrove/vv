package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Request struct {
	Page   int `json:"page,omitempty"`
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`

	Sort    Sorts    `json:"sort,omitempty"`
	Select  Strings  `json:"select,omitempty"`
	Preload Preloads `json:"preload,omitempty"`
	Filter  Filter   `json:"filter,omitzero"`

	Terms []Term `json:"terms,omitempty"`

	Search       string  `json:"search,omitempty"`
	SearchFields Strings `json:"searchFields,omitempty"`

	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`

	Unpaged   bool `json:"unpaged,omitempty"`
	SkipTotal bool `json:"skipTotal,omitempty"`
	Distinct  bool `json:"distinct,omitempty"`

	unpagedParam string

	afterSet  bool
	beforeSet bool

	omitPaging bool
}

func (this *Request) UnpagedParam() string {
	if this == nil || this.unpagedParam == "" {
		return "unpaged"
	}
	return this.unpagedParam
}

func (this *Request) OmitPaging() {
	if this != nil {
		this.omitPaging = true
	}
}

func (this *Request) ClearCursors() {
	if this != nil {
		this.After, this.Before = "", ""
		this.afterSet, this.beforeSet = false, false
	}
}

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
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := rejectDuplicateJSONValue(dec, where); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	default:
		return fmt.Errorf("query: malformed JSON delimiter %q", delim)
	}
}

func unknownFieldOf(err error) (string, bool) {
	const prefix = "json: unknown field "
	message := err.Error()
	i := strings.Index(message, prefix)
	if i < 0 {
		return "", false
	}
	return strings.Trim(message[i+len(prefix):], `"`), true
}

var requestKeys = []string{
	"page", "limit", "offset", "sort", "select", "preload", "filter", "terms",
	"search", "searchFields", "after", "before", "unpaged", "skipTotal", "distinct",
}

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

type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`

	Nulls string `json:"nulls,omitempty"`
}

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

type Preload struct {
	Path    string `json:"path"`
	Filter  Filter `json:"filter,omitzero"`
	Sort    Sorts  `json:"sort,omitempty"`
	MaxRows int    `json:"maxRows,omitzero"`
}

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

func (this Filter) IsZero() bool { return len(trim(this.raw)) == 0 }

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
