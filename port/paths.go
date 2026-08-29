package port

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/frostgrove/vv/crud"
)

// Paths derives a [PathMap] from the model's own wire tags instead of asking
// somebody to type one.
//
// The map a consumer writes by hand is nearly always the model's `json` tags
// transcribed, one line per column, and transcription is a job for the machine:
// every entry is a chance to type "sourceFileName" where the struct says
// "sourceFilename", and the symptom is a wrong `field` in a production error
// body that no test looks at. The tag is already the single source of truth
// about what the client sent — this reads it.
//
//	var ContractPaths = port.Paths[Contract]().
//		From("json", "form").
//		Override(port.PathMap{"SourceHash": port.At(FieldFile)}).
//		MustBuild()
//
// # What it does not do
//
// It does not weaken anything [MustPathMap] enforces. The result goes through
// [NewPathMap] before it is returned, so a [PathBuilder.Override] naming a
// column the model does not have, or one no request carries, still refuses at
// start-up ([[D-021]]).
//
// It does not guess. A column no named tag gives a key for is a refusal naming
// that column, never a key derived from the Go field name — that is
// [PathBuilder.OrFieldName], and it is a separate call because a silent
// fallback answers `SourceFilename` where the wire says `sourceFilename` and is
// indistinguishable from a correct map until the first violation. A column a
// tag took *off* the wire is a refusal that even that call does not cover
// ([[D-071]]).
//
// # When to keep writing one by hand
//
// A generated resource ([[D-050]]) has a wire shape of its own — `cmd/vv
// -adapter` names the input fields `lowerFirst(FieldName)` rather than by the
// model's tag — so its map is generated with it and must not be replaced by
// this. This is for the resource mounted straight onto the model, where the
// model *is* the wire shape.
func Paths[M any]() *PathBuilder[M] {
	return &PathBuilder[M]{tags: []string{"json"}}
}

// A PathBuilder collects the derivation rules and answers a validated
// [PathMap]. Build it with [Paths].
//
// Every method answers the receiver so the whole declaration is one expression,
// and a misuse is held until [PathBuilder.Build] rather than panicking in the
// middle of a chain: a package-level var that failed halfway through would name
// the builder in the stack trace and not the model.
type PathBuilder[M any] struct {
	tags      []string
	overrides PathMap
	except    []string
	fieldName bool
	err       error
}

// From names the struct tags to read, in order of preference: the first one
// that names this field wins.
//
// The default is `json` alone. An endpoint that also accepts a form body
// declares `From("json", "form")`, and any tag key at all is allowed — the
// argument is the tag name, not a fixed set, so a house tag (`wire`, `api`)
// works with no change here.
//
// Order matters where a field carries both. It is preference and not a merge:
// two tags naming two different keys is one column with two wire spellings, and
// a map can only hold the one the violation should be reported at.
func (this *PathBuilder[M]) From(tags ...string) *PathBuilder[M] {
	if len(tags) == 0 {
		return this.fail("From was given no tag to read")
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			return this.fail("From was given an empty tag name")
		}
	}
	this.tags = tags
	return this
}

// Override replaces what the tags say for the columns it names, and is the
// answer for the one field whose wire key is not the model's.
//
// The usual case is a column that arrives under a different name than it is
// stored under — a hash the client sends as a file — where the tag is right for
// the response body and wrong for the violation. Called twice, the later entry
// for a column wins.
//
// An override is validated like any other entry: one naming a column the model
// does not have, or a `generated` one no request carries, refuses at start-up.
// That is the whole point of routing it through here rather than editing the
// map afterwards.
func (this *PathBuilder[M]) Override(entries PathMap) *PathBuilder[M] {
	if len(entries) == 0 {
		return this
	}
	if this.overrides == nil {
		this.overrides = make(PathMap, len(entries))
	}
	for name, to := range entries {
		this.overrides[name] = to
	}
	return this
}

// Except drops columns from the map and from the totality check both, for a
// column deliberately outside the wire shape.
//
// It is the same list [NewPathMap] takes, and it is needed for the same reason:
// reflection sees an ordinary writable column where the author decided the
// client never sends one. Excepting a column and overriding it is a
// contradiction and is refused rather than resolved.
func (this *PathBuilder[M]) Except(names ...string) *PathBuilder[M] {
	this.except = append(this.except, names...)
	return this
}

// OrFieldName maps a column no tag names to its Go field name.
//
// Separate from [PathBuilder.From] and never a default, because it is the one
// rule here that can be confidently wrong: a model with no tags at all is
// decoded by the field name and this is exactly right, while a model whose tags
// somebody forgot on one field gets `SourceFilename` where the wire says
// `sourceFilename` — a map that looks complete, passes every check, and reports
// the wrong key. Asking for it is the consumer saying which of the two they
// have.
//
// The field name verbatim, not lowerFirst. A binding that mounts a model
// directly decodes into it by field name; `lowerFirst` is the *generated*
// adapter's convention ([[D-050]]) and belongs to the map generated with it.
//
// It answers for silence only. A column whose tag says `-` is one the author
// stated is not on the wire, and giving it a key anyway would point a violation
// at something the client cannot find in its own body — so that stays a
// refusal, curable with [PathBuilder.Except] or [PathBuilder.Override].
func (this *PathBuilder[M]) OrFieldName() *PathBuilder[M] {
	this.fieldName = true
	return this
}

// Build derives the map and validates it against the model.
//
// The domain is [NewPathMap]'s: every column an INSERT writes, less the
// optimistic lock and less whatever [PathBuilder.Except] named. Deriving over
// exactly that set is what makes the result total by construction — the failure
// this replaces is a hand-written map missing a column somebody added last
// Tuesday.
func (this *PathBuilder[M]) Build() (PathMap, error) {
	if this.err != nil {
		return nil, this.err
	}
	schema, err := crud.SchemaOf[M]()
	if err != nil {
		return nil, err
	}
	tags := wireTags(schema.Type)
	excepted := setOf(this.except)

	out := make(PathMap, len(schema.Insert))
	var underived, omitted []string
	for _, field := range schema.Insert {
		if field.Version || excepted[field.Name] {
			continue
		}
		if to, overridden := this.overrides[field.Name]; overridden {
			out[field.Name] = to
			continue
		}
		switch key, verdict := this.keyOf(field.Name, tags[field.Name]); {
		case verdict == tagNames:
			out[field.Name] = At(key)
		case verdict == tagOmits:
			omitted = append(omitted, field.Name)
		case this.fieldName:
			out[field.Name] = At(field.Name)
		default:
			underived = append(underived, field.Name)
		}
	}

	// An override for something outside the domain is carried through rather
	// than dropped, so NewPathMap reports it. Silently ignoring an entry
	// somebody wrote is how a typo in an override survives — the map would be
	// valid and the line would do nothing.
	var contradicted []string
	for name, to := range this.overrides {
		if excepted[name] {
			contradicted = append(contradicted, name)
			continue
		}
		if _, derived := out[name]; !derived {
			out[name] = to
		}
	}

	if err := this.refuse(schema.Name, underived, omitted, contradicted); err != nil {
		return nil, err
	}
	return NewPathMap[M](out, this.except...)
}

// MustBuild is [PathBuilder.Build] as a package-level declaration: a model this
// cannot derive a map for refuses to start rather than answering a wrong path
// on some later request ([[D-021]]).
func (this *PathBuilder[M]) MustBuild() PathMap {
	out, err := this.Build()
	if err != nil {
		panic(err)
	}
	return out
}

// What the named tags had to say about one field. Silence and refusal are two
// answers and not one, because only the first of them is something
// [PathBuilder.OrFieldName] may answer for: a field nobody tagged might well be
// decoded under its own name, and a field tagged `json:"-"` is one the author
// said is not on the wire at all.
type tagVerdict int

const (
	tagSilent tagVerdict = iota // no named tag is on the field
	tagOmits                    // a named tag says the field is not on the wire
	tagNames                    // a named tag gives the key
)

// keyOf answers the wire key the named tags give this field.
//
// The options are cut off, so `json:"email,omitempty"` is the key `email` — an
// entry carrying the option would translate a violation to a key no client ever
// sent.
//
// Two spellings of encoding/json's are honoured rather than reinvented, because
// a map that disagreed with the decoder would be wrong in the one direction
// nobody checks:
//
//   - `json:"-"` omits the field, so this tag names no key and the next one is
//     asked. `json:"-,"` is the field literally called "-", which cuts to "-".
//   - `json:",omitempty"` keeps the *field name* as the key. That is the
//     decoder's own rule rather than a guess about one, so it is not
//     [PathBuilder.OrFieldName] — a field with no tag at all is what that
//     answers for.
func (this *PathBuilder[M]) keyOf(field string, tag reflect.StructTag) (string, tagVerdict) {
	omitted := false
	for _, name := range this.tags {
		value, tagged := tag.Lookup(name)
		if !tagged {
			continue
		}
		if value == "-" {
			omitted = true
			continue
		}
		key, _, _ := strings.Cut(value, ",")
		if key == "" {
			return field, tagNames
		}
		return key, tagNames
	}
	if omitted {
		return "", tagOmits
	}
	return "", tagSilent
}

// refuse turns whatever could not be derived into one message naming every
// column and what to do about it.
//
// One message and not the first failure: somebody adding a model with four
// untagged columns should see four names once, rather than run the application
// four times.
func (this *PathBuilder[M]) refuse(model string, underived, omitted, contradicted []string) error {
	sort.Strings(underived)
	sort.Strings(omitted)
	sort.Strings(contradicted)

	sources := strings.Join(this.tags, " or ")
	var problems []string
	if len(underived) > 0 {
		problems = append(problems, fmt.Sprintf("no %s tag names %s — take the field name with OrFieldName, or name the key with Override",
			sources, strings.Join(underived, ", ")))
	}
	if len(omitted) > 0 {
		problems = append(problems, fmt.Sprintf("%s says %s is not on the wire, so no key can be right for it — drop it with Except, or name its key with Override",
			sources, strings.Join(omitted, ", ")))
	}
	if len(contradicted) > 0 {
		problems = append(problems, "an override and an exclusion for "+strings.Join(contradicted, ", ")+
			", which cannot both be what was meant")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the inverse path map for %s cannot be derived from the model: %s",
		model, strings.Join(problems, "; "))
}

// wireTags indexes a model's struct tags by Go field name, flattening embedded
// structs the way crud.Schema does — a promoted column is one entry in the
// schema, so its tag has to be reachable under the same name.
//
// Breadth-first, because Go's promotion rule is *shallowest wins*: a
// depth-first walk would let a field two embeddings down claim a name the outer
// struct declares later in its own body. Nothing in this repository can reach
// that today — crud refuses a model with a duplicate field name before any of
// this runs — so it is the cheap way round rather than a fix for a bug anybody
// has hit.
//
// What is skipped is what crud does not map: a `db:"-"` field, a relation, an
// unexported one. Those can share a name with a column without crud objecting,
// and indexing one would hand a column the tag of a field that is not it.
//
// An embedded field is indexed under its own name as well as walked into: a
// scalar-shaped one — sql.NullString, a wrapper over a string — is a column in
// its own right to crud, and its tag is the one that matters.
func wireTags(root reflect.Type) map[string]reflect.StructTag {
	tags := map[string]reflect.StructTag{}

	frontier := []tagFrame{{structure: structOf(root)}}
	for len(frontier) > 0 {
		var deeper []tagFrame
		for _, frame := range frontier {
			if frame.structure == nil {
				continue
			}
			for i := range frame.structure.NumField() {
				field := frame.structure.Field(i)
				// A `db:"-"` embedding is dropped whole by crud, so its fields
				// are not columns and walking into it would index tags for
				// names the schema does not have.
				if field.Tag.Get(crud.TagKey) == "-" {
					continue
				}
				if field.Anonymous {
					deeper = append(deeper, frame.into(field.Type))
				}
				if unmapped(field) {
					continue
				}
				if _, shallower := tags[field.Name]; !shallower {
					tags[field.Name] = field.Tag
				}
			}
		}
		frontier = deeper
	}
	return tags
}

// unmapped reports whether crud leaves this field out of the schema, and
// therefore whether its tag could only ever be attached to the wrong column.
func unmapped(field reflect.StructField) bool {
	if _, related := field.Tag.Lookup("rel"); related {
		return true
	}
	return !field.IsExported() && !field.Anonymous
}

// A tagFrame is one struct on the walk, with the embeddings it was reached
// through — which is what stops a type that embeds itself from looping forever.
type tagFrame struct {
	structure reflect.Type
	through   []reflect.Type
}

func (this tagFrame) into(embedded reflect.Type) tagFrame {
	next := structOf(embedded)
	for _, seen := range this.through {
		if seen == next {
			return tagFrame{}
		}
	}
	if next == this.structure {
		return tagFrame{}
	}
	return tagFrame{structure: next, through: append(this.through, this.structure)}
}

// structOf answers the struct behind a type, or nil for anything else.
func structOf(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	return t
}

// fail records a misuse for Build to answer with. A builder that panicked here
// would name the method and not the model, and the model is what has to change.
func (this *PathBuilder[M]) fail(problem string) *PathBuilder[M] {
	if this.err == nil {
		this.err = fmt.Errorf("port.Paths[%s]: %s", typeName[M](), problem)
	}
	return this
}
