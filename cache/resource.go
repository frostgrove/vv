package cache

import (
	"fmt"
	"sort"
	"strings"
)

type ResourceTenant string

const (
	CacheTenant           ResourceTenant = "cache"
	DurableWorkTenant     ResourceTenant = "durable-work"
	DurableSecurityTenant ResourceTenant = "durable-security"
)

const MaxWaiverReasonBytes = 256

type SharedResourceWaiver struct {
	granted bool
	reason  string
}

func SharedDurableSecurity(reason string) SharedResourceWaiver {
	return SharedResourceWaiver{granted: true, reason: reason}
}

func (this SharedResourceWaiver) Granted() bool { return this.granted }

func (this SharedResourceWaiver) Reason() string { return this.reason }

type ResourceDeclaration struct {
	Resource ResourceID
	Tenants  []ResourceTenant
	Waiver   SharedResourceWaiver
}

type resourceDomain struct {
	tenants map[ResourceTenant]struct{}
	waiver  SharedResourceWaiver
}

func validResourceTenant(tenant ResourceTenant) bool {
	return tenant == CacheTenant || tenant == DurableWorkTenant || tenant == DurableSecurityTenant
}

func indexResources(values []ResourceDeclaration) (map[ResourceID]*resourceDomain, []string) {
	domains := make(map[ResourceID]*resourceDomain, len(values))
	problems := make([]string, 0)
	for index, declaration := range values {
		if validNamespacePart(string(declaration.Resource)) != nil {
			problems = append(problems, fmt.Sprintf("resource declaration %d has an invalid identity", index))
			continue
		}
		if _, exists := domains[declaration.Resource]; exists {
			problems = append(problems, fmt.Sprintf("duplicate resource declaration %q", declaration.Resource))
			continue
		}
		if len(declaration.Tenants) == 0 {
			problems = append(problems, fmt.Sprintf("resource %q declares no tenant", declaration.Resource))
			continue
		}
		domain := &resourceDomain{tenants: make(map[ResourceTenant]struct{}, len(declaration.Tenants)), waiver: declaration.Waiver}
		for _, tenant := range declaration.Tenants {
			if !validResourceTenant(tenant) {
				problems = append(problems, fmt.Sprintf("resource %q declares unknown tenant %q", declaration.Resource, tenant))
				continue
			}
			domain.tenants[tenant] = struct{}{}
		}
		reason := strings.TrimSpace(declaration.Waiver.reason)
		if declaration.Waiver.granted && (reason == "" || len(reason) > MaxWaiverReasonBytes) {
			problems = append(problems, fmt.Sprintf("resource %q waives sharing without a usable reason", declaration.Resource))
		}
		domains[declaration.Resource] = domain
	}
	return domains, problems
}

func undeclaredResourceProblems(domains map[ResourceID]*resourceDomain, cacheOwners map[ResourceID]string) []string {
	problems := make([]string, 0)
	for resource, owner := range cacheOwners {
		if _, declared := domains[resource]; !declared {
			problems = append(problems, fmt.Sprintf("%s: resource %q declares no tenant, so nothing proves it separate from durable state", owner, resource))
		}
	}
	sort.Strings(problems)
	return problems
}

func evictionDomainProblems(domains map[ResourceID]*resourceDomain, cacheOwners map[ResourceID]string) []string {
	problems := make([]string, 0)
	for resource, domain := range domains {
		owner, activated := cacheOwners[resource]
		_, declaredCache := domain.tenants[CacheTenant]
		_, holdsWork := domain.tenants[DurableWorkTenant]
		_, holdsSecurity := domain.tenants[DurableSecurityTenant]
		switch {
		case activated && (holdsWork || holdsSecurity):
			problems = append(problems, fmt.Sprintf("%s: shares the eviction domain of resource %q with durable state", owner, resource))
		case declaredCache && (holdsWork || holdsSecurity):
			problems = append(problems, fmt.Sprintf("resource %q shares one eviction domain between a cache and durable state", resource))
		case holdsWork && holdsSecurity && !domain.waiver.granted:
			problems = append(problems, fmt.Sprintf("resource %q holds durable work and durable security without a SharedDurableSecurity waiver", resource))
		case domain.waiver.granted && !(holdsWork && holdsSecurity):
			problems = append(problems, fmt.Sprintf("resource %q waives a sharing it does not declare", resource))
		}
	}
	return problems
}
