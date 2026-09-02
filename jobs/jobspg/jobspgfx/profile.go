package jobspgfx

import (
	"fmt"
	"strings"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobspg"
)

type DeploymentProfile string

const (
	DevelopmentProfile DeploymentProfile = "development"
	TestProfile        DeploymentProfile = "test"
	ProductionProfile  DeploymentProfile = "production"
)

func ProfileOf(environment string) DeploymentProfile {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "dev", "devel", "development", "local":
		return DevelopmentProfile
	case "test", "tests", "testing", "ci":
		return TestProfile
	}
	return ProductionProfile
}

func (this DeploymentProfile) SchemaManagement() jobspg.SchemaManagement {
	if this == ProductionProfile {
		return jobspg.VerifySchema
	}
	return jobspg.ManageSchema
}

type SchemaManagementDecision struct {
	Profile    DeploymentProfile
	Management jobspg.SchemaManagement
	Overridden bool
}

func (this ApplicationSettings) SchemaManagementDecision() (SchemaManagementDecision, error) {
	if !this.SchemaManagement.Valid() {
		return SchemaManagementDecision{}, fmt.Errorf("jobspgfx: %w: schema management", jobs.ErrInvalid)
	}
	profile := ProfileOf(this.Environment)
	management := this.SchemaManagement
	if management == jobspg.UnsetSchemaManagement {
		management = profile.SchemaManagement()
	}
	decision := SchemaManagementDecision{Profile: profile, Management: management}
	if management != jobspg.ManageSchema || profile != ProductionProfile {
		return decision, nil
	}
	if !this.AllowManagedSchemaInProduction {
		return SchemaManagementDecision{}, fmt.Errorf("jobspgfx: %w: the %s profile does not migrate its own jobs schema; run jobspg.MigrationStatements from the deployment step, or set AllowManagedSchemaInProduction to take that risk on purpose", jobs.ErrInvalid, profile)
	}
	decision.Overridden = true
	return decision, nil
}
