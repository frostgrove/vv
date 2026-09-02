package crud

import "reflect"

type OptionGroup struct {
	verb   string
	allow  map[string]bool
	reason func(field string) string
}

var (
	ReadOptions = OptionGroup{
		verb: "a read",
		allow: optionFields("Filter", "Sort", "Preloads", "Fields", "PreloadRows",
			"Page", "Limit", "Offset", "RelScopes", "After", "Before",
			"Primary", "Unpaged", "NoSort", "NoTotal", "ForUpdate", "Distinct"),
		reason: unsupportedReadOption,
	}

	MutationOptions = OptionGroup{
		verb:   "a write",
		allow:  optionFields("Filter", "RelScopes", "ForUpdate", "Primary"),
		reason: unsupportedMutationOption,
	}

	AggregateOptions = OptionGroup{
		verb: "an aggregate read",
		allow: optionFields("Filter", "Sort", "RelScopes", "Agg",
			"Page", "Limit", "Offset", "Unpaged", "NoSort", "Primary"),
		reason: unsupportedAggregateOption,
	}

	PreloadOptions = OptionGroup{
		verb:   "a preload",
		allow:  optionFields("Filter", "Sort", "PreloadRows"),
		reason: unsupportedPreloadOption,
	}
)

func optionFields(names ...string) map[string]bool {
	allow := make(map[string]bool, len(names))
	for _, name := range names {
		allow[name] = true
	}
	return allow
}

func (this OptionGroup) Build(model string, options ...Option) (*Options, error) {
	o := Build(options...)
	if err := this.Check(model, o); err != nil {
		return nil, err
	}
	return o, nil
}

func (this OptionGroup) Check(model string, o *Options) error {
	field, reason := this.refused(o)
	if field == "" {
		return nil
	}
	return &SchemaError{Model: model, Field: OptionSpelling(field), Reason: reason}
}

func (this OptionGroup) refused(o *Options) (field, reason string) {
	value := reflect.ValueOf(*o)
	typ := value.Type()
	for i := range value.NumField() {
		name := typ.Field(i).Name
		if this.allow[name] || value.Field(i).IsZero() {
			continue
		}
		return name, this.why(name)
	}
	return "", ""
}

func (this OptionGroup) why(field string) string {
	if reason := this.reason(field); reason != "" {
		return reason
	}
	return "query option " + OptionSpelling(field) + " is not supported by " + this.verb
}

func OptionSpelling(field string) string {
	if spelling, ok := optionSpellings[field]; ok {
		return spelling
	}
	return field
}

var optionSpellings = map[string]string{
	"Filter":      "Where",
	"Sort":        "OrderBy",
	"Preloads":    "Preload",
	"Fields":      "Select",
	"PreloadRows": "PreloadRows",
	"Page":        "Page",
	"Limit":       "Limit",
	"Offset":      "Offset",
	"RelScopes":   "NarrowRelations",
	"Agg":         "Aggregate",
	"After":       "After",
	"Before":      "Before",
	"Primary":     "PrimaryOnly",
	"Unpaged":     "Unpaged",
	"NoSort":      "Unsorted",
	"NoTotal":     "SkipTotal",
	"ForUpdate":   "ForUpdate",
	"Distinct":    "Distinct",
}

func unsupportedReadOption(field string) string {
	if field == "Agg" {
		return "a read answers with rows; ask for aggregations through Aggregate"
	}
	return ""
}

func unsupportedMutationOption(field string) string {
	switch field {
	case "Page", "Limit", "Offset", "Unpaged":
		return "a filtered write is not paginated; every row the filter matches is written"
	case "After", "Before":
		return "a cursor continues a page of a read; a write has no page to continue"
	case "Sort":
		return "a write touches a set, not a sequence; narrow it with a filter instead"
	case "Fields":
		return "a write projects nothing; the patch decides which columns change"
	case "Preloads":
		return "a write loads no relations"
	case "PreloadRows":
		return "a write loads no relations to cap"
	case "NoSort":
		return "a write has no ordering to disable"
	case "NoTotal":
		return "a write has no total query to skip"
	case "Distinct":
		return "a write cannot deduplicate the rows it touches"
	case "Agg":
		return "a write is not an aggregate read"
	}
	return ""
}

func unsupportedAggregateOption(field string) string {
	switch field {
	case "Preloads":
		return "an aggregate answers with groups, and there is no model to hang a relation on"
	case "PreloadRows":
		return "an aggregate loads no relations to cap"
	case "Fields":
		return "an aggregate projects its groups and its aggregations; a projection cannot change that"
	case "After", "Before":
		return "a cursor walks the sort tuple of entity rows, which an aggregate does not return"
	case "NoTotal":
		return "an aggregate has no separate total query to skip"
	case "ForUpdate":
		return "an aggregate reads groups and cannot lock the rows behind them"
	case "Distinct":
		return "DISTINCT on an aggregate belongs to the aggregation; use CountDistinct"
	}
	return ""
}
