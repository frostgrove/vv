package access

import "github.com/frostgrove/vv/port"

// The last hop of an error path for this context's two CRUD resources: the
// model field a violation happened at becomes the key the client sent.
//
// MustPathMap refuses at start-up unless the map is exact *and* total, which is
// the whole reason to write one against a model rather than a port.Fields: a
// missing entry is a wrong field in a production error body and nowhere else,
// and there is no test that would notice.
// Every column a request can carry has an entry, including the ones a request
// may not *change*: a create body may name them, so a violation may happen at
// them, and "the client could not have sent this" is not the same statement as
// "this column cannot be written".
var (
	RolePaths = port.MustPathMap[Role](port.PathMap{
		"ID":       port.At("id"),
		"Slug":     port.At("slug"),
		"Name":     port.At("name"),
		"IsSystem": port.At("isSystem"),
	})

	PermissionPaths = port.MustPathMap[Permission](port.PathMap{
		"ID":     port.At("id"),
		"Code":   port.At("code"),
		"Name":   port.At("name"),
		"Module": port.At("module"),
	})
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
