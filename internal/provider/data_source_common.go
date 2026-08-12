package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

const catalogLookupPageSize = 200

var (
	errCatalogNotFound  = errors.New("catalog item not found")
	errCatalogAmbiguous = errors.New("catalog selector is ambiguous")
)

type catalogLookupError struct {
	Kind       error
	Resource   string
	Selector   string
	MatchCount int
}

type catalogReader interface {
	CurrentOrganization(context.Context) (client.Organization, error)
	ListComponentTypes(context.Context, client.ListOptions) (client.Page[client.ComponentType], error)
	ListTeams(context.Context, client.ListOptions) (client.Page[client.Team], error)
	ListTags(context.Context, client.ListOptions) (client.Page[client.Tag], error)
}

type componentReader interface {
	GetComponent(context.Context, string) (client.Component, error)
}

func (e *catalogLookupError) Error() string {
	switch {
	case errors.Is(e.Kind, errCatalogNotFound):
		return fmt.Sprintf("no %s matched %s", e.Resource, e.Selector)
	case errors.Is(e.Kind, errCatalogAmbiguous):
		return fmt.Sprintf("%d %s records matched %s", e.MatchCount, e.Resource, e.Selector)
	default:
		return fmt.Sprintf("unable to resolve %s from %s", e.Resource, e.Selector)
	}
}

func (e *catalogLookupError) Unwrap() error {
	return e.Kind
}

func configureCatalogDataSource(
	providerData any,
	response *datasource.ConfigureResponse,
) catalogReader {
	if providerData == nil {
		return nil
	}

	configuredClient, ok := providerData.(catalogReader)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected Pulse client type",
			fmt.Sprintf("Expected a Pulse catalog client, got %T. Please report this issue to the provider developers.", providerData),
		)
		return nil
	}

	return configuredClient
}

func configureComponentDataSource(
	providerData any,
	response *datasource.ConfigureResponse,
) componentReader {
	if providerData == nil {
		return nil
	}

	configuredClient, ok := providerData.(componentReader)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected Pulse client type",
			fmt.Sprintf("Expected a Pulse component client, got %T. Please report this issue to the provider developers.", providerData),
		)
		return nil
	}

	return configuredClient
}

type catalogSelector struct {
	ID      string
	Name    string
	Purpose string
}

func validateIDOrNameSelectorConfig(
	id types.String,
	name types.String,
	diagnostics *diag.Diagnostics,
) {
	// Terraform validates data-source configuration before all referenced
	// variables are necessarily known. Defer selector validation until Read in
	// that case; Read still requires one complete selector.
	if id.IsUnknown() || name.IsUnknown() {
		return
	}
	idOrNameSelector(id, name, diagnostics)
}

func validateTagSelectorConfig(
	id types.String,
	name types.String,
	purpose types.String,
	diagnostics *diag.Diagnostics,
) {
	if id.IsUnknown() || name.IsUnknown() || purpose.IsUnknown() {
		return
	}
	tagSelector(id, name, purpose, diagnostics)
}

func (s catalogSelector) description() string {
	if s.ID != "" {
		return fmt.Sprintf("id %q", s.ID)
	}
	if s.Purpose != "" {
		return fmt.Sprintf("purpose %q and name %q", s.Purpose, s.Name)
	}
	return fmt.Sprintf("name %q", s.Name)
}

func idOrNameSelector(
	id types.String,
	name types.String,
	diagnostics *diag.Diagnostics,
) (catalogSelector, bool) {
	idValue := ""
	if !id.IsNull() && !id.IsUnknown() {
		idValue = id.ValueString()
	}
	nameValue := ""
	if !name.IsNull() && !name.IsUnknown() {
		nameValue = name.ValueString()
	}
	if idValue != strings.TrimSpace(idValue) || nameValue != strings.TrimSpace(nameValue) {
		diagnostics.AddError(
			"Invalid catalog selector",
			"Catalog ids and names must not contain leading or trailing whitespace.",
		)
		return catalogSelector{}, false
	}

	if strings.TrimSpace(idValue) == "" {
		idValue = ""
	}
	if strings.TrimSpace(nameValue) == "" {
		nameValue = ""
	}

	// Selector attributes are Optional+Computed so Terraform represents the
	// unconfigured, computed counterpart as unknown. A known id or name is
	// sufficient; only defer when no complete selector is known yet.
	if idValue != "" && nameValue == "" {
		return catalogSelector{ID: idValue}, true
	}
	if nameValue != "" && idValue == "" {
		return catalogSelector{Name: nameValue}, true
	}
	if id.IsUnknown() || name.IsUnknown() {
		diagnostics.AddError(
			"Unknown catalog selector",
			"The catalog id or name must be known before Pulse can resolve the data source.",
		)
		return catalogSelector{}, false
	}

	if (idValue == "") == (nameValue == "") {
		diagnostics.AddError(
			"Invalid catalog selector",
			"Configure exactly one of id or name. Display names are resolved only by an exact match within the organization.",
		)
		return catalogSelector{}, false
	}

	return catalogSelector{ID: idValue, Name: nameValue}, true
}

func tagSelector(
	id types.String,
	name types.String,
	purpose types.String,
	diagnostics *diag.Diagnostics,
) (catalogSelector, bool) {
	idValue := ""
	if !id.IsNull() && !id.IsUnknown() {
		idValue = id.ValueString()
	}
	nameValue := ""
	if !name.IsNull() && !name.IsUnknown() {
		nameValue = name.ValueString()
	}
	purposeValue := ""
	if !purpose.IsNull() && !purpose.IsUnknown() {
		purposeValue = purpose.ValueString()
	}
	if idValue != strings.TrimSpace(idValue) ||
		nameValue != strings.TrimSpace(nameValue) ||
		purposeValue != strings.TrimSpace(purposeValue) {
		diagnostics.AddError(
			"Invalid tag selector",
			"Tag ids, names, and purposes must not contain leading or trailing whitespace.",
		)
		return catalogSelector{}, false
	}

	if strings.TrimSpace(idValue) == "" {
		idValue = ""
	}
	if strings.TrimSpace(nameValue) == "" {
		nameValue = ""
	}
	if strings.TrimSpace(purposeValue) == "" {
		purposeValue = ""
	}

	if idValue != "" {
		if nameValue != "" || purposeValue != "" {
			diagnostics.AddError(
				"Invalid tag selector",
				"Configure either id, or both purpose and name. Tag names alone are not stable identity.",
			)
			return catalogSelector{}, false
		}
		return catalogSelector{ID: idValue}, true
	}

	if nameValue != "" && purposeValue != "" {
		if purposeValue != "filter" && purposeValue != "relevance" {
			diagnostics.AddError(
				"Invalid tag purpose",
				"Tag purpose must be exactly \"filter\" or \"relevance\".",
			)
			return catalogSelector{}, false
		}
		return catalogSelector{Name: nameValue, Purpose: purposeValue}, true
	}

	if id.IsUnknown() || name.IsUnknown() || purpose.IsUnknown() {
		diagnostics.AddError(
			"Unknown tag selector",
			"The tag id or exact purpose and name must be known before Pulse can resolve the data source.",
		)
		return catalogSelector{}, false
	}

	if nameValue == "" || purposeValue == "" {
		diagnostics.AddError(
			"Invalid tag selector",
			"Configure either id, or both purpose and name. A tag name can be reused by different purposes.",
		)
		return catalogSelector{}, false
	}
	return catalogSelector{}, false
}

func lookupCatalogItem[T any](
	ctx context.Context,
	resource string,
	selector catalogSelector,
	list func(context.Context, client.ListOptions) (client.Page[T], error),
	id func(T) string,
	name func(T) string,
	purpose func(T) string,
) (T, error) {
	var zero T
	var matches []T
	cursor := ""
	seenCursors := map[string]struct{}{}

	for {
		page, err := list(ctx, client.ListOptions{Cursor: cursor, Limit: catalogLookupPageSize})
		if err != nil {
			return zero, err
		}

		for _, item := range page.Items {
			matched := false
			if selector.ID != "" {
				matched = id(item) == selector.ID
			} else {
				matched = name(item) == selector.Name
				if selector.Purpose != "" {
					matched = matched && purpose != nil && purpose(item) == selector.Purpose
				}
			}
			if matched {
				matches = append(matches, item)
			}
		}

		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			return zero, fmt.Errorf("pulse %s catalog returned the same pagination cursor twice", resource)
		}
		if _, exists := seenCursors[page.NextCursor]; exists {
			return zero, fmt.Errorf("pulse %s catalog returned a pagination cursor cycle", resource)
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}

	switch len(matches) {
	case 0:
		return zero, &catalogLookupError{
			Kind:     errCatalogNotFound,
			Resource: resource,
			Selector: selector.description(),
		}
	case 1:
		return matches[0], nil
	default:
		return zero, &catalogLookupError{
			Kind:       errCatalogAmbiguous,
			Resource:   resource,
			Selector:   selector.description(),
			MatchCount: len(matches),
		}
	}
}

func addCatalogLookupDiagnostic(
	resource string,
	err error,
	diagnostics *diag.Diagnostics,
) {
	var lookupError *catalogLookupError
	if errors.As(err, &lookupError) {
		if errors.Is(err, errCatalogAmbiguous) {
			diagnostics.AddError(
				"Ambiguous Pulse "+resource,
				lookupError.Error()+". Select the record by UUID instead of relying on a display name.",
			)
			return
		}
		if errors.Is(err, errCatalogNotFound) {
			diagnostics.AddError(
				"Pulse "+resource+" not found",
				lookupError.Error()+" in the organization derived from the configured automation credential.",
			)
			return
		}
	}

	diagnostics.AddError(
		"Unable to read Pulse "+resource,
		"The Pulse automation API could not resolve the catalog record: "+err.Error(),
	)
}

func stringValueOrNull(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func stringSetValue(
	ctx context.Context,
	values []string,
	diagnostics *diag.Diagnostics,
) basetypes.SetValue {
	if values == nil {
		values = []string{}
	}
	value, valueDiagnostics := types.SetValueFrom(ctx, types.StringType, values)
	diagnostics.Append(valueDiagnostics...)
	return value
}
