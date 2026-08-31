package access

import "github.com/frostgrove/vv/port"

var (
	RolePaths = port.Paths[Role]().MustBuild()

	PermissionPaths = port.Paths[Permission]().MustBuild()
)

var AuthBodyPaths = port.Fields{
	"Identifier": port.At("email"),
	"Email":      port.At("email"),
	"Password":   port.At("password"),
	"SecretHash": port.At("password"),
	"Name":       port.At("name"),
	"Role":       port.At("role"),
	"Permission": port.At("permission"),
}
