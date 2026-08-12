package provider

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

func TestLookupCatalogItemReadsEveryPageBeforeSelecting(t *testing.T) {
	t.Parallel()

	pages := map[string]client.Page[client.Team]{
		"": {
			Items:      []client.Team{{ID: "team-1", Name: "Other"}},
			NextCursor: "next-page",
		},
		"next-page": {
			Items: []client.Team{{ID: "team-2", Name: "Operations"}},
		},
	}
	var cursors []string

	item, err := lookupCatalogItem(
		context.Background(),
		"team",
		catalogSelector{Name: "Operations"},
		func(_ context.Context, options client.ListOptions) (client.Page[client.Team], error) {
			cursors = append(cursors, options.Cursor)
			if options.Limit != catalogLookupPageSize {
				t.Fatalf("limit = %d, want %d", options.Limit, catalogLookupPageSize)
			}
			return pages[options.Cursor], nil
		},
		func(item client.Team) string { return item.ID },
		func(item client.Team) string { return item.Name },
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := item.ID, "team-2"; got != want {
		t.Fatalf("id = %q, want %q", got, want)
	}
	if got, want := cursors, []string{"", "next-page"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cursors = %#v, want %#v", got, want)
	}
}

func TestLookupCatalogItemRejectsAmbiguousExactName(t *testing.T) {
	t.Parallel()

	_, err := lookupCatalogItem(
		context.Background(),
		"team",
		catalogSelector{Name: "Operations"},
		func(_ context.Context, _ client.ListOptions) (client.Page[client.Team], error) {
			return client.Page[client.Team]{Items: []client.Team{
				{ID: "team-1", Name: "Operations"},
				{ID: "team-2", Name: "Operations"},
			}}, nil
		},
		func(item client.Team) string { return item.ID },
		func(item client.Team) string { return item.Name },
		nil,
	)
	if !errors.Is(err, errCatalogAmbiguous) {
		t.Fatalf("error = %v, want ambiguous catalog error", err)
	}
	var lookupError *catalogLookupError
	if !errors.As(err, &lookupError) {
		t.Fatalf("error type = %T, want *catalogLookupError", err)
	}
	if got, want := lookupError.MatchCount, 2; got != want {
		t.Fatalf("match count = %d, want %d", got, want)
	}
}

func TestLookupCatalogItemRejectsMissingExactName(t *testing.T) {
	t.Parallel()

	_, err := lookupCatalogItem(
		context.Background(),
		"component type",
		catalogSelector{Name: "Service"},
		func(_ context.Context, _ client.ListOptions) (client.Page[client.ComponentType], error) {
			return client.Page[client.ComponentType]{Items: []client.ComponentType{{ID: "type-1", Name: "Database"}}}, nil
		},
		func(item client.ComponentType) string { return item.ID },
		func(item client.ComponentType) string { return item.Name },
		nil,
	)
	if !errors.Is(err, errCatalogNotFound) {
		t.Fatalf("error = %v, want catalog not found error", err)
	}
}

func TestLookupCatalogItemRejectsCursorCycle(t *testing.T) {
	t.Parallel()

	_, err := lookupCatalogItem(
		context.Background(),
		"team",
		catalogSelector{ID: "team-1"},
		func(_ context.Context, options client.ListOptions) (client.Page[client.Team], error) {
			if options.Cursor == "" {
				return client.Page[client.Team]{NextCursor: "cursor-a"}, nil
			}
			return client.Page[client.Team]{NextCursor: "cursor-a"}, nil
		},
		func(item client.Team) string { return item.ID },
		func(item client.Team) string { return item.Name },
		nil,
	)
	if err == nil {
		t.Fatal("expected cursor cycle error")
	}
}

func TestIDOrNameSelectorRequiresExactlyOneSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        types.String
		itemName  types.String
		wantValid bool
	}{
		{name: "id", id: types.StringValue("id-1"), itemName: types.StringNull(), wantValid: true},
		{name: "name", id: types.StringNull(), itemName: types.StringValue("Service"), wantValid: true},
		{name: "neither", id: types.StringNull(), itemName: types.StringNull()},
		{name: "both", id: types.StringValue("id-1"), itemName: types.StringValue("Service")},
		{name: "unknown", id: types.StringUnknown(), itemName: types.StringNull()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var diagnostics diag.Diagnostics
			_, valid := idOrNameSelector(test.id, test.itemName, &diagnostics)
			if valid != test.wantValid {
				t.Fatalf("valid = %t, want %t; diagnostics: %v", valid, test.wantValid, diagnostics)
			}
			if diagnostics.HasError() == test.wantValid {
				t.Fatalf("has error = %t, want %t; diagnostics: %v", diagnostics.HasError(), !test.wantValid, diagnostics)
			}
		})
	}
}

func TestTagSelectorRequiresUUIDOrPurposeNameTuple(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		id          types.String
		tagName     types.String
		purpose     types.String
		wantValid   bool
		wantPurpose string
	}{
		{name: "id", id: types.StringValue("tag-1"), tagName: types.StringNull(), purpose: types.StringNull(), wantValid: true},
		{name: "tuple", id: types.StringNull(), tagName: types.StringValue("network"), purpose: types.StringValue("filter"), wantValid: true, wantPurpose: "filter"},
		{name: "name only", id: types.StringNull(), tagName: types.StringValue("network"), purpose: types.StringNull()},
		{name: "purpose only", id: types.StringNull(), tagName: types.StringNull(), purpose: types.StringValue("filter")},
		{name: "id plus tuple", id: types.StringValue("tag-1"), tagName: types.StringValue("network"), purpose: types.StringValue("filter")},
		{name: "unsupported purpose", id: types.StringNull(), tagName: types.StringValue("network"), purpose: types.StringValue("routing")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var diagnostics diag.Diagnostics
			selector, valid := tagSelector(test.id, test.tagName, test.purpose, &diagnostics)
			if valid != test.wantValid {
				t.Fatalf("valid = %t, want %t; diagnostics: %v", valid, test.wantValid, diagnostics)
			}
			if valid && selector.Purpose != test.wantPurpose {
				t.Fatalf("purpose = %q, want %q", selector.Purpose, test.wantPurpose)
			}
		})
	}
}
