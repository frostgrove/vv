package access

import "github.com/frostgrove/vv/port"

// The last hop of an error path for this context's two CRUD resources: the
// model field a violation happened at becomes the key the client sent.
//
// Derived and not transcribed. Nothing maps these two models into a body of
// their own, so the model *is* the wire shape and the json tag already says
// what the client sent; writing it out a second time only adds a place for the
// two to disagree, and the disagreement shows up as a wrong field in a
// production error body and nowhere else.
//
// It refuses at start-up on exactly the terms a written-out map does: every
// column a request can carry has an entry, including the ones a request may not
// *change*, because a create body may name them and a violation may happen at
// them. "The client could not have sent this" is not the same statement as
// "this column cannot be written".
var (
	RolePaths = port.Paths[Role]().MustBuild()

	PermissionPaths = port.Paths[Permission]().MustBuild()
)

// AuthBodyPaths is the same hop for the hand-written sign-in endpoints.
//
// port.Fields and not MustPathMap, and the difference is not a shortcut: there
// is no model behind /auth/login to be total against. The bodies are three
// small structs, several model fields map onto one key — a caller types a
// password, and both Credential.SecretHash and the register command's Password
// are violations about that key — and a head this map does not name passes
// through unchanged, which is the honest answer for a field nobody mapped.
var AuthBodyPaths = port.Fields{
	"Identifier": port.At("email"),
	"Email":      port.At("email"),
	"Password":   port.At("password"),
	"SecretHash": port.At("password"),
	"Name":       port.At("name"),
	"Role":       port.At("role"),
	"Permission": port.At("permission"),
}
