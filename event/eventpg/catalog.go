package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Level 3: what pg_catalog says the schema is, against what this build expects
// it to be. Six reads plus the identity sequence, once, at Prepare — never on a
// probe path.
//
// The classes below are compared as sets rather than by presence, and that is
// the whole reason this file is longer than a fingerprint comparison: a hand
// edit is at least as likely to have added an object as to have dropped one, and
// the added ones are the silent ones. A policy on events makes every read a
// filtered subset and a short page is the end of a stream; a third BEFORE INSERT
// trigger records a fact nobody decided; a rule redirects the insert; a child
// table joins every read with none of the parent's constraints. An extra index
// or an extra constraint is exempt, because each of those fails loudly at the
// first write instead.
//
// A trigger is compared by what it does and not by what it is. The body of the
// function it calls, whether it carries a WHEN condition and whether it watches
// a column list are read too: a trigger with this build's name, timing and
// function that returns instead of raising, or that fires only on an UPDATE of
// one column, leaves the history mutable while every property that identifies
// it still matches.
const (
	relationsQuery = `
		SELECT c.relname, c.relkind::text, c.relpersistence::text, c.relrowsecurity, c.relforcerowsecurity, c.relhassubclass
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname IN `

	columnsQuery = `
		SELECT c.relname, a.attname, a.attnum::integer, a.atttypid::regtype::text, a.attnotnull,
		       a.attidentity::text, COALESCE(pg_get_expr(d.adbin, d.adrelid), '')
		  FROM pg_attribute a
		  JOIN pg_class c ON c.oid = a.attrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		 WHERE n.nspname = $1 AND a.attnum > 0 AND NOT a.attisdropped AND c.relname IN `

	columnsOrder = ` ORDER BY c.relname, a.attnum`

	constraintsQuery = `
		SELECT c.relname, con.conname, con.contype::text, con.convalidated, con.connoinherit,
		       COALESCE((SELECT string_agg(held.attname, ',' ORDER BY key.ord)
		                   FROM unnest(con.conkey) WITH ORDINALITY AS key(number, ord)
		                   JOIN pg_attribute held ON held.attrelid = con.conrelid AND held.attnum = key.number), ''),
		       COALESCE((SELECT other.relname FROM pg_class other WHERE other.oid = con.confrelid), ''),
		       COALESCE((SELECT string_agg(held.attname, ',' ORDER BY key.ord)
		                   FROM unnest(con.confkey) WITH ORDINALITY AS key(number, ord)
		                   JOIN pg_attribute held ON held.attrelid = con.confrelid AND held.attnum = key.number), ''),
		       pg_get_constraintdef(con.oid)
		  FROM pg_constraint con
		  JOIN pg_class c ON c.oid = con.conrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname IN `

	triggersQuery = `
		SELECT c.relname, t.tgname, t.tgtype::integer, t.tgenabled::text, p.proname, called.nspname, p.prosrc,
		       t.tgqual IS NOT NULL,
		       COALESCE((SELECT string_agg(watched.attname, ',' ORDER BY key.ord)
		                   FROM unnest(t.tgattr::int2[]) WITH ORDINALITY AS key(number, ord)
		                   JOIN pg_attribute watched ON watched.attrelid = t.tgrelid AND watched.attnum = key.number), '')
		  FROM pg_trigger t
		  JOIN pg_class c ON c.oid = t.tgrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  JOIN pg_proc p ON p.oid = t.tgfoid
		  JOIN pg_namespace called ON called.oid = p.pronamespace
		 WHERE n.nspname = $1 AND NOT t.tgisinternal AND c.relname IN `

	rulesQuery = `
		SELECT c.relname, r.rulename
		  FROM pg_rewrite r
		  JOIN pg_class c ON c.oid = r.ev_class
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND r.rulename <> '_RETURN' AND c.relname IN `

	policiesQuery = `
		SELECT c.relname, policy.polname
		  FROM pg_policy policy
		  JOIN pg_class c ON c.oid = policy.polrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname IN `

	sequenceNameQuery = `SELECT COALESCE(pg_get_serial_sequence($1, $2), '')`

	sequenceQuery = `SELECT s.seqincrement, s.seqcache, s.seqcycle FROM pg_sequence s WHERE s.seqrelid = $1::regclass`
)

// PostgreSQL's own trigger bitmask, which is what pg_trigger.tgtype holds. It is
// compared as a number rather than by parsing pg_get_triggerdef, because the
// number is the fact and the text is a rendering of it.
const (
	triggerRow      = 1 << 0
	triggerBefore   = 1 << 1
	triggerInsert   = 1 << 2
	triggerDelete   = 1 << 3
	triggerUpdate   = 1 << 4
	triggerTruncate = 1 << 5

	triggerEnabled = "O"

	ordinaryTable    = "r"
	permanentStorage = "p"
	unloggedStorage  = "u"
	temporaryStorage = "t"
)

type deployedRelation struct {
	kind        string
	persistence string
	security    bool
	forced      bool
	subclass    bool
}

type deployedColumn struct {
	name      string
	ordinal   int32
	dataType  string
	notNull   bool
	identity  string
	byDefault string
}

type deployedConstraint struct {
	kind       string
	columns    string
	table      string
	references string
	validated  bool
	noInherit  bool
	definition string
}

type deployedTrigger struct {
	kind        int32
	enabled     string
	function    string
	schema      string
	body        string
	conditional bool
	watched     string
}

type deployedSchema struct {
	relations   map[string]deployedRelation
	columns     map[string][]deployedColumn
	constraints map[string]map[string]deployedConstraint
	triggers    map[string]map[string]deployedTrigger
	unexpected  []string
}

func inspect(ctx context.Context, on *sql.Conn, schema Schema) error {
	model := expected(schema, SchemaVersion)
	found, err := readCatalog(ctx, on, model)
	if err != nil {
		return err
	}
	if err := model.compare(found); err != nil {
		return err
	}
	return model.compareIdentity(ctx, on)
}

func readCatalog(ctx context.Context, on *sql.Conn, model expectation) (deployedSchema, error) {
	found := deployedSchema{
		relations:   map[string]deployedRelation{},
		columns:     map[string][]deployedColumn{},
		constraints: map[string]map[string]deployedConstraint{},
		triggers:    map[string]map[string]deployedTrigger{},
	}
	for _, table := range model.tables {
		found.constraints[table.name] = map[string]deployedConstraint{}
		found.triggers[table.name] = map[string]deployedTrigger{}
	}
	if err := readRows(ctx, on, model, relationsQuery+tableList(model), func(rows *sql.Rows) error {
		var table string
		var held deployedRelation
		if err := rows.Scan(&table, &held.kind, &held.persistence, &held.security, &held.forced, &held.subclass); err != nil {
			return err
		}
		found.relations[table] = held
		return nil
	}); err != nil {
		return found, err
	}
	if err := readRows(ctx, on, model, columnsQuery+tableList(model)+columnsOrder, func(rows *sql.Rows) error {
		var table string
		var held deployedColumn
		if err := rows.Scan(&table, &held.name, &held.ordinal, &held.dataType, &held.notNull, &held.identity, &held.byDefault); err != nil {
			return err
		}
		found.columns[table] = append(found.columns[table], held)
		return nil
	}); err != nil {
		return found, err
	}
	if err := readRows(ctx, on, model, constraintsQuery+tableList(model), func(rows *sql.Rows) error {
		var table, name string
		var held deployedConstraint
		if err := rows.Scan(&table, &name, &held.kind, &held.validated, &held.noInherit,
			&held.columns, &held.table, &held.references, &held.definition); err != nil {
			return err
		}
		found.constraints[table][name] = held
		return nil
	}); err != nil {
		return found, err
	}
	if err := readRows(ctx, on, model, triggersQuery+tableList(model), func(rows *sql.Rows) error {
		var table, name string
		var held deployedTrigger
		if err := rows.Scan(&table, &name, &held.kind, &held.enabled, &held.function, &held.schema,
			&held.body, &held.conditional, &held.watched); err != nil {
			return err
		}
		found.triggers[table][name] = held
		return nil
	}); err != nil {
		return found, err
	}
	for _, class := range []struct {
		query string
		what  string
	}{
		{rulesQuery, "a rewrite rule"},
		{policiesQuery, "a row-level-security policy"},
	} {
		if err := readRows(ctx, on, model, class.query+tableList(model), func(rows *sql.Rows) error {
			var table, name string
			if err := rows.Scan(&table, &name); err != nil {
				return err
			}
			found.unexpected = append(found.unexpected, class.what+" named "+name+" on "+table)
			return nil
		}); err != nil {
			return found, err
		}
	}
	return found, nil
}

func readRows(ctx context.Context, on *sql.Conn, model expectation, statement string, read func(*sql.Rows) error) error {
	parameters := make([]any, 0, len(model.tables)+1)
	parameters = append(parameters, model.schema.Name)
	for _, table := range model.tables {
		parameters = append(parameters, table.name)
	}
	rows, err := on.QueryContext(ctx, statement, parameters...)
	if err != nil {
		return fmt.Errorf("eventpg: reading the catalog of %q: %w", model.schema.Name, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := read(rows); err != nil {
			return fmt.Errorf("eventpg: reading the catalog of %q: %w", model.schema.Name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("eventpg: reading the catalog of %q: %w", model.schema.Name, err)
	}
	return nil
}

func tableList(model expectation) string {
	placeholders := make([]string, len(model.tables))
	for index := range model.tables {
		placeholders[index] = "$" + strconv.Itoa(index+2)
	}
	return "(" + strings.Join(placeholders, ", ") + ")"
}

func (this expectation) compare(found deployedSchema) error {
	for _, table := range this.tables {
		if err := this.compareRelation(table, found.relations); err != nil {
			return err
		}
		if err := this.compareColumns(table, found.columns[table.name]); err != nil {
			return err
		}
		if err := this.compareConstraints(table, found.constraints[table.name]); err != nil {
			return err
		}
		if err := this.compareTriggers(table, found.triggers[table.name]); err != nil {
			return err
		}
	}
	if len(found.unexpected) > 0 {
		return fmt.Errorf("%w: %q carries %s, and this build expects none: an object that rewrites, redirects or filters a row changes what a read returns without raising anything",
			ErrSchemaMismatch, this.schema.Name, found.unexpected[0])
	}
	return nil
}

func (this expectation) compareRelation(table expectedTable, found map[string]deployedRelation) error {
	held, deployed := found[table.name]
	switch {
	case !deployed:
		return this.wrong(table.name, "is missing")
	case held.kind != ordinaryTable:
		return this.wrong(table.name, "is a relation of kind "+held.kind+" and this build expects an ordinary table")
	case held.persistence != permanentStorage:
		return this.wrong(table.name, "is "+storageName(held.persistence)+
			" and this store publishes a durable history, which a table a crash empties is not")
	case held.security || held.forced:
		return this.wrong(table.name, "has row-level security enabled, and a policy makes every read of a history a filtered subset of it")
	case held.subclass:
		return this.wrong(table.name, "is inherited from, and a child table joins every read of it carrying none of its constraints")
	}
	return nil
}

func (this expectation) compareColumns(table expectedTable, found []deployedColumn) error {
	if len(found) != len(table.columns) {
		return this.wrong(table.name, "has "+strconv.Itoa(len(found))+" columns and this build expects "+strconv.Itoa(len(table.columns)))
	}
	for index, column := range table.columns {
		held := found[index]
		where := table.name + "." + column.name
		switch {
		case held.name != column.name:
			return this.wrong(where, "is column "+strconv.Itoa(index+1)+" and the deployed schema holds "+held.name+" there")
		case held.ordinal != int32(index+1):
			return this.wrong(where, "is at ordinal "+strconv.Itoa(int(held.ordinal))+" and this build expects "+strconv.Itoa(index+1))
		case held.dataType != column.dataType:
			return this.wrong(where, "is "+held.dataType+" and this build expects "+column.dataType)
		case held.notNull != column.notNull:
			return this.wrong(where, "is nullable in the deployed schema and this build expects it not to be")
		case held.identity != identityLetter(column.identity):
			return this.wrong(where, "is generated as "+orDash(held.identity)+" and this build expects "+orDash(identityLetter(column.identity)))
		case held.byDefault != column.byDefault:
			return this.wrong(where, "defaults to "+orDash(held.byDefault)+" and this build expects "+orDash(column.byDefault)+
				": a default writes a value nobody recorded")
		}
	}
	return nil
}

func (this expectation) compareConstraints(table expectedTable, found map[string]deployedConstraint) error {
	if err := this.compareConstraint(table, found, table.primaryKeyName(), deployedConstraint{
		kind: "p", columns: strings.Join(table.pk, ","),
	}); err != nil {
		return err
	}
	for _, unique := range table.uniques {
		if err := this.compareConstraint(table, found, unique.name, deployedConstraint{
			kind: "u", columns: strings.Join(unique.columns, ","),
		}); err != nil {
			return err
		}
	}
	for _, foreign := range table.foreign {
		if err := this.compareConstraint(table, found, foreign.name, deployedConstraint{
			kind: "f", columns: strings.Join(foreign.columns, ","),
			table: foreign.table, references: strings.Join(foreign.references, ","),
		}); err != nil {
			return err
		}
	}
	for _, check := range table.checks {
		if err := this.compareConstraint(table, found, check.name, deployedConstraint{
			kind: "c", definition: check.definition(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (this expectation) compareConstraint(table expectedTable, found map[string]deployedConstraint, name string, want deployedConstraint) error {
	held, deployed := found[name]
	where := table.name + "." + name
	switch {
	case !deployed:
		return this.wrong(where, "is missing")
	case held.kind != want.kind:
		return this.wrong(where, "is a constraint of kind "+held.kind+" and this build expects "+want.kind)
	case !held.validated:
		return this.wrong(where, "was never validated, so the rows already in the table were never held to it")
	case want.kind == "c" && held.noInherit:
		return this.wrong(where, "is NO INHERIT and this build expects it to reach every row")
	case want.kind != "c" && held.columns != want.columns:
		return this.wrong(where, "covers ("+held.columns+") and this build expects ("+want.columns+")")
	case want.kind == "f" && held.table != want.table:
		return this.wrong(where, "references "+held.table+" and this build expects "+want.table)
	case want.kind == "f" && held.references != want.references:
		return this.wrong(where, "references ("+held.references+") and this build expects ("+want.references+")")
	case want.kind == "c" && !sameDefinition(held.definition, want.definition):
		return this.wrong(where, "is "+collapsed(held.definition)+" and this build expects "+collapsed(want.definition))
	}
	return nil
}

func (this expectation) compareTriggers(table expectedTable, found map[string]deployedTrigger) error {
	for name := range found {
		if !table.declaresTrigger(name) {
			return this.wrong(table.name+"."+name, "is a trigger this build does not expect, and one that fires on an insert records a fact nobody decided")
		}
	}
	for _, trigger := range table.triggers {
		held, deployed := found[trigger.name]
		where := table.name + "." + trigger.name
		body := collapsed(this.function(trigger.function).source())
		switch {
		case !deployed:
			return this.wrong(where, "is missing")
		case held.kind != triggerMask(trigger):
			return this.wrong(where, "fires on "+strconv.Itoa(int(held.kind))+" and this build expects "+strconv.Itoa(int(triggerMask(trigger)))+
				" ("+trigger.timing+" "+trigger.events+" FOR EACH "+trigger.level+")")
		case held.enabled != triggerEnabled:
			return this.wrong(where, "is disabled, so what it enforces is not enforced")
		case held.function != trigger.function || held.schema != this.schema.Name:
			return this.wrong(where, "runs "+held.schema+"."+held.function+" and this build expects "+this.schema.Name+"."+trigger.function)
		case held.watched != "":
			return this.wrong(where, "fires only on an UPDATE that names ("+held.watched+
				"), so an UPDATE of any other column never reaches it")
		case held.conditional:
			return this.wrong(where, "carries a WHEN condition this build did not author, so it decides per row whether to act at all")
		case collapsed(held.body) != body:
			return this.wrong(where, "calls "+held.function+", whose body is "+collapsed(held.body)+
				" and this build expects "+body+
				": a trigger enforces the body of the function it calls and not its name")
		}
	}
	return nil
}

func (this expectation) function(name string) expectedFunction {
	for _, function := range this.functions {
		if function.name == name {
			return function
		}
	}
	return expectedFunction{}
}

func storageName(persistence string) string {
	switch persistence {
	case unloggedStorage:
		return "an unlogged table"
	case temporaryStorage:
		return "a temporary table"
	}
	return "a table of persistence " + persistence
}

// The identity sequence, and it is not decoration: INCREMENT 1, CACHE 1 and no
// CYCLE are what make the log's positions an order over time, which is the first
// premise the read watermark rests on.
func (this expectation) compareIdentity(ctx context.Context, on *sql.Conn) error {
	table, column, identity := this.identityColumn()
	var sequence string
	qualified := quoteIdentifier(this.schema.Name) + "." + quoteIdentifier(table)
	if err := on.QueryRowContext(ctx, sequenceNameQuery, qualified, column).Scan(&sequence); err != nil {
		return fmt.Errorf("eventpg: reading the identity sequence of %s.%s.%s: %w", this.schema.Name, table, column, err)
	}
	if sequence == "" {
		return this.wrong(table+"."+column, "draws from no sequence, so it is not an identity column at all")
	}
	var increment, cache int64
	var cycle bool
	if err := on.QueryRowContext(ctx, sequenceQuery, sequence).Scan(&increment, &cache, &cycle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return this.wrong(table+"."+column, "names the sequence "+sequence+", which does not exist")
		}
		return fmt.Errorf("eventpg: reading the sequence %s: %w", sequence, err)
	}
	if increment != int64(identity.increment) || cache != int64(identity.cache) || cycle != identity.cycle {
		return this.wrong(table+"."+column, "draws from a sequence at increment="+strconv.FormatInt(increment, 10)+
			" cache="+strconv.FormatInt(cache, 10)+" cycle="+strconv.FormatBool(cycle)+
			" and this build expects "+identity.rendering()+
			": a cached or cycling sequence hands positions out in an order that is not the order they were drawn in")
	}
	return nil
}

func (this expectation) identityColumn() (string, string, expectedIdentity) {
	for _, table := range this.tables {
		for _, column := range table.columns {
			if column.identity.present() {
				return table.name, column.name, column.identity
			}
		}
	}
	return "", "", expectedIdentity{}
}

func (this expectation) wrong(object, what string) error {
	return fmt.Errorf("%w: %s.%s %s", ErrSchemaMismatch, this.schema.Name, object, what)
}

func (this expectedTable) declaresTrigger(name string) bool {
	for _, trigger := range this.triggers {
		if trigger.name == name {
			return true
		}
	}
	return false
}

func triggerMask(trigger expectedTrigger) int32 {
	var mask int32
	if strings.EqualFold(trigger.timing, "BEFORE") {
		mask |= triggerBefore
	}
	if strings.EqualFold(trigger.level, "ROW") {
		mask |= triggerRow
	}
	for _, event := range strings.Split(trigger.events, " OR ") {
		switch strings.ToUpper(strings.TrimSpace(event)) {
		case "INSERT":
			mask |= triggerInsert
		case "DELETE":
			mask |= triggerDelete
		case "UPDATE":
			mask |= triggerUpdate
		case "TRUNCATE":
			mask |= triggerTruncate
		}
	}
	return mask
}

func identityLetter(identity expectedIdentity) string {
	switch identity.generation {
	case "always":
		return "a"
	case "by default":
		return "d"
	}
	return ""
}

// PostgreSQL renders a constraint from the tree it parsed, so it parenthesises
// where this build does not and expands what it folded. Comparing the tokens
// rather than the text is what keeps one authored expression sufficient for both
// the DDL and this comparison.
func sameDefinition(deployed, described string) bool {
	return tokens(deployed) == tokens(described)
}

func tokens(definition string) string {
	var out strings.Builder
	for _, held := range definition {
		switch held {
		case ' ', '\t', '\n', '\r', '(', ')':
		default:
			out.WriteRune(held)
		}
	}
	return out.String()
}
