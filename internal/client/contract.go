package client

import (
	"errors"
	"net/url"
	"strings"
)

// ContractError indicates that a nominally successful Pulse response did not
// satisfy the frozen automation API contract. It intentionally carries no
// response values.
type ContractError struct {
	operation string
}

func (e *ContractError) Error() string {
	return "Pulse API returned an invalid " + e.operation + " response"
}

func contractError(operation string) error {
	return &ContractError{operation: operation}
}

func validateOrganization(value Organization) error {
	if value.ID == "" || value.Name == "" || value.Slug == "" {
		return contractError("organization")
	}
	return nil
}

func validateComponentTypes(page Page[ComponentType]) error {
	for _, item := range page.Items {
		if err := validateComponentType(item); err != nil {
			return contractError("component type collection")
		}
	}
	return nil
}

func validateComponentType(item ComponentType) error {
	if item.ID == "" || item.Name == "" || item.GreenLabel == "" || item.YellowLabel == "" || item.RedLabel == "" || item.UnknownLabel == "" || item.Revision <= 0 {
		return contractError("component type")
	}
	return nil
}

func validateTeams(page Page[Team]) error {
	for _, item := range page.Items {
		if err := validateTeam(item); err != nil {
			return contractError("team collection")
		}
	}
	return nil
}

func validateTeam(item Team) error {
	if item.ID == "" || item.Name == "" || item.Revision <= 0 {
		return contractError("team")
	}
	return nil
}

func validateTags(page Page[Tag]) error {
	for _, item := range page.Items {
		if err := validateTag(item); err != nil {
			return contractError("tag collection")
		}
	}
	return nil
}

func validateTag(item Tag) error {
	if item.ID == "" || item.Name == "" || (item.Purpose != "relevance" && item.Purpose != "filter") || item.Revision <= 0 {
		return contractError("tag")
	}
	return nil
}

func validateComponent(value Component) error {
	if value.ID == "" || value.ExternalKey == "" || value.Name == "" || value.ComponentTypeID == "" || value.RelevanceTagIDs == nil || value.FilterTagIDs == nil || value.Revision <= 0 || value.ArchivedAt != nil {
		return contractError("component")
	}
	switch value.State {
	case ComponentStateUnknown, ComponentStateGreen, ComponentStateYellow, ComponentStateRed:
	default:
		return contractError("component")
	}
	if containsEmpty(value.RelevanceTagIDs) || containsEmpty(value.FilterTagIDs) {
		return contractError("component")
	}
	return nil
}

func validateRollup(value ComponentRollup, expectedParentID string) error {
	if value.ParentComponentID == "" || value.ParentComponentID != expectedParentID || value.Rules == nil || value.Revision <= 0 {
		return contractError("component rollup")
	}
	seenChildren := make(map[string]struct{})
	for _, rule := range value.Rules {
		if len(rule.ChildComponentIDs) == 0 || !validRollupEffect(rule.WhenChildYellow) || !validRollupEffect(rule.WhenChildRed) {
			return contractError("component rollup")
		}
		for _, childID := range rule.ChildComponentIDs {
			if childID == "" {
				return contractError("component rollup")
			}
			if _, duplicate := seenChildren[childID]; duplicate {
				return contractError("component rollup")
			}
			seenChildren[childID] = struct{}{}
		}
	}
	return nil
}

func validRollupEffect(value RollupEffect) bool {
	return value == RollupEffectNone || value == RollupEffectYellow || value == RollupEffectRed
}

func validateIntegration(value ComponentIntegration, expectedComponentID string, expectedProvider IntegrationProvider, allowInsecureHTTP bool) error {
	if value.ComponentID != expectedComponentID || value.Provider != expectedProvider || !validIntegrationProvider(value.Provider) || value.CredentialVersionID == "" || value.Revision <= 0 || value.ArchivedAt != nil {
		return contractError("component integration")
	}
	if value.LifecycleOwner != IntegrationLifecycleOwnerHuman && value.LifecycleOwner != IntegrationLifecycleOwnerAutomation {
		return contractError("component integration")
	}
	if value.Status != IntegrationStatusActive {
		return contractError("component integration")
	}
	endpoint, err := url.Parse(value.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return contractError("component integration")
	}
	if endpoint.Scheme == "http" && (!allowInsecureHTTP || !isLoopbackHost(endpoint.Hostname())) {
		return contractError("component integration")
	}
	expectedSuffix := "/webhooks/components/" + value.ComponentID + "/" + string(value.Provider)
	if !strings.HasSuffix(endpoint.Path, expectedSuffix) {
		return contractError("component integration")
	}
	return nil
}

func validateIntegrationMutation(value ComponentIntegrationMutation, expectedComponentID string, expectedProvider IntegrationProvider, allowInsecureHTTP bool) error {
	if err := validateIntegration(value.Integration, expectedComponentID, expectedProvider, allowInsecureHTTP); err != nil {
		return err
	}
	if value.Integration.LifecycleOwner != IntegrationLifecycleOwnerAutomation || value.Secret == nil {
		return contractError("component integration mutation")
	}
	if value.Secret.Value == "" || strings.TrimSpace(value.Secret.Value) != value.Secret.Value || strings.ContainsAny(value.Secret.Value, "\r\n") || value.Secret.VersionID == "" || value.Secret.VersionID != value.Integration.CredentialVersionID {
		return contractError("component integration mutation")
	}
	return nil
}

func validIntegrationProvider(value IntegrationProvider) bool {
	return value == IntegrationProviderGrafana || value == IntegrationProviderPagerDuty || value == IntegrationProviderPulse
}

func containsEmpty(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

// IsContractError reports whether err indicates a successful response that did
// not match the frozen automation API contract.
func IsContractError(err error) bool {
	var target *ContractError
	return errors.As(err, &target)
}
