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
		if item.ID == "" || item.Name == "" || item.GreenLabel == "" || item.YellowLabel == "" || item.RedLabel == "" || item.UnknownLabel == "" {
			return contractError("component type collection")
		}
	}
	return nil
}

func validateTeams(page Page[Team]) error {
	for _, item := range page.Items {
		if item.ID == "" || item.Name == "" {
			return contractError("team collection")
		}
	}
	return nil
}

func validateTags(page Page[Tag]) error {
	for _, item := range page.Items {
		if item.ID == "" || item.Name == "" || (item.Purpose != "relevance" && item.Purpose != "filter") {
			return contractError("tag collection")
		}
	}
	return nil
}

func validateComponent(value Component) error {
	if value.ID == "" || value.ExternalKey == "" || value.Name == "" || value.ComponentTypeID == "" || value.RelevanceTagIDs == nil || value.FilterTagIDs == nil || value.Revision <= 0 || value.ArchivedAt != nil {
		return contractError("component")
	}
	if value.Kind != ComponentKindExternal && value.Kind != ComponentKindRollup {
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

func validateIntegration(value ComponentIntegration, expectedComponentID string, allowInsecureHTTP bool) error {
	if value.ID == "" || value.ComponentID != expectedComponentID || value.Source != IntegrationSourceGrafana || value.SourceKey == "" || value.CredentialVersionID == "" || value.Revision <= 0 || value.ArchivedAt != nil {
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
	expectedSuffix := "/webhooks/component-integrations/" + value.ID + "/grafana"
	if !strings.HasSuffix(endpoint.Path, expectedSuffix) {
		return contractError("component integration")
	}
	return nil
}

func validateIntegrationMutation(value ComponentIntegrationMutation, expectedComponentID string, allowInsecureHTTP bool) error {
	if err := validateIntegration(value.Integration, expectedComponentID, allowInsecureHTTP); err != nil {
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
