package sqlfault

// The driver shapes this package meets, written out rather than imported. The
// root module has no third-party requirement and that includes a driver in a
// test file: `go mod tidy` counts test imports, so importing pgx here would put
// pgx in the root go.mod and check-tidy would say so.
//
// Each one is the shape a real driver presents, and the shape is the contract —
// pgconn's SQLState method and named string fields, mysql.MySQLError's Number
// with a [5]byte state, modernc.org/sqlite's Code method with nothing exported
// at all.

// pgconnish is *pgconn.PgError's shape. Code holds the SQLSTATE as a *string*,
// which is why a Code field is only read as a number when it holds one.
type pgconnish struct {
	Code           string
	Message        string
	Detail         string
	Hint           string
	ConstraintName string
	TableName      string
	SchemaName     string
	ColumnName     string
	DataTypeName   string
	// File, Line and Routine name PostgreSQL's own C source and change when the
	// server is rebuilt. Position is the offset into the statement. None of the
	// four is carried.
	File     string
	Line     int32
	Routine  string
	Position int32
}

func (this *pgconnish) Error() string    { return this.Message }
func (this *pgconnish) SQLState() string { return this.Code }

func pgish(state string) *pgconnish {
	return &pgconnish{Code: state, Message: "ERROR: something the server said"}
}

// mysqlish is *mysql.MySQLError's shape: a number, a [5]byte state padded with
// NULs, and a message. Nothing structured, ever.
type mysqlish struct {
	Number   uint16
	SQLState [5]byte
	Message  string
}

func (this *mysqlish) Error() string { return this.Message }

func myish(number uint16, state, message string) *mysqlish {
	e := &mysqlish{Number: number, Message: message}
	copy(e.SQLState[:], state)
	return e
}

// sqliteish is modernc.org/sqlite's *Error: a Code method, no SQLSTATE, and no
// exported field for anything.
type sqliteish struct{ code int }

func (this *sqliteish) Error() string { return "constraint failed" }
func (this *sqliteish) Code() int     { return this.code }

// mattnish is mattn/go-sqlite3's: two integer fields, the extended one being the
// one that names the constraint.
type mattnish struct {
	Code         int
	ExtendedCode int
}

func (this *mattnish) Error() string { return "constraint failed" }
