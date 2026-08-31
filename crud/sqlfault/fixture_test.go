package sqlfault

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

type sqliteish struct{ code int }

func (this *sqliteish) Error() string { return "constraint failed" }
func (this *sqliteish) Code() int     { return this.code }

type mattnish struct {
	Code         int
	ExtendedCode int
}

func (this *mattnish) Error() string { return "constraint failed" }
