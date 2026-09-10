package auditdeployment

import "github.com/frostgrove/vv/audit"

type Inputs struct {
	Admin audit.CatalogAdmin
}

func NewCatalogAdmin(inputs Inputs) audit.CatalogAdmin {
	return inputs.Admin
}
