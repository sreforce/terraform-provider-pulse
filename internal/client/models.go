package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ComponentKind distinguishes signal-receiving leaves from aggregate rollups.
type ComponentKind string

const (
	ComponentKindExternal ComponentKind = "external"
	ComponentKindRollup   ComponentKind = "rollup"
)

// ComponentState is computed operational state and is never submitted by the
// Terraform provider.
type ComponentState string

const (
	ComponentStateUnknown ComponentState = "unknown"
	ComponentStateGreen   ComponentState = "green"
	ComponentStateYellow  ComponentState = "yellow"
	ComponentStateRed     ComponentState = "red"
)

// Organization is the organization derived from the automation credential.
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ComponentType is an organization-scoped component type catalog entry.
type ComponentType struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	GreenLabel   string `json:"green_label"`
	YellowLabel  string `json:"yellow_label"`
	RedLabel     string `json:"red_label"`
	UnknownLabel string `json:"unknown_label"`
}

// Team is an organization-scoped owner-team catalog entry.
type Team struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Tag is an organization-scoped tag catalog entry. Purpose and name together
// are the human lookup identity; UUID remains the resource identity.
type Tag struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Purpose      string  `json:"purpose"`
	DisplayLabel *string `json:"display_label"`
	DisplayOrder int64   `json:"display_order"`
	Icon         *string `json:"icon"`
}

// UnmarshalJSON distinguishes an omitted or null display_order from its valid
// zero value. The field is required by the canonical automation API contract.
func (t *Tag) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		Purpose      string  `json:"purpose"`
		DisplayLabel *string `json:"display_label"`
		DisplayOrder *int64  `json:"display_order"`
		Icon         *string `json:"icon"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.DisplayOrder == nil {
		return contractError("tag collection")
	}
	*t = Tag{
		ID:           wire.ID,
		Name:         wire.Name,
		Purpose:      wire.Purpose,
		DisplayLabel: wire.DisplayLabel,
		DisplayOrder: *wire.DisplayOrder,
		Icon:         wire.Icon,
	}
	return nil
}

// ListOptions controls stable cursor pagination.
type ListOptions struct {
	Cursor string
	Limit  int
}

// Page is the canonical collection envelope.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// UnmarshalJSON rejects missing or null collection fields instead of treating a
// malformed/legacy envelope as a valid terminal empty page.
func (p *Page[T]) UnmarshalJSON(data []byte) error {
	var wire struct {
		Items      *[]T    `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Items == nil || wire.NextCursor == nil {
		return errors.New("pulse collection response is missing required fields")
	}
	p.Items = *wire.Items
	p.NextCursor = *wire.NextCursor
	return nil
}

// Component contains desired configuration plus computed runtime state.
type Component struct {
	ID              string         `json:"id"`
	ExternalKey     string         `json:"external_key"`
	Kind            ComponentKind  `json:"kind"`
	Name            string         `json:"name"`
	ComponentTypeID string         `json:"component_type_id"`
	OwnerTeamID     *string        `json:"owner_team_id"`
	RelevanceTagIDs []string       `json:"relevance_tag_ids"`
	FilterTagIDs    []string       `json:"filter_tag_ids"`
	AlertEnabled    bool           `json:"alert_enabled"`
	State           ComponentState `json:"state"`
	StateReason     *string        `json:"state_reason"`
	Revision        int64          `json:"revision"`
	ArchivedAt      *time.Time     `json:"archived_at"`
}

// ComponentCreateRequest contains only desired configuration. Reissuing a
// create for an archived external_key restores the same Pulse UUID server-side.
type ComponentCreateRequest struct {
	ExternalKey     string        `json:"external_key"`
	Kind            ComponentKind `json:"kind"`
	Name            string        `json:"name"`
	ComponentTypeID string        `json:"component_type_id"`
	OwnerTeamID     *string       `json:"owner_team_id"`
	RelevanceTagIDs []string      `json:"relevance_tag_ids"`
	FilterTagIDs    []string      `json:"filter_tag_ids"`
	AlertEnabled    bool          `json:"alert_enabled"`
}

// ComponentUpdateRequest replaces the complete Terraform-managed mutable
// configuration. Runtime state and immutable identity are deliberately absent.
type ComponentUpdateRequest struct {
	Name            string   `json:"name"`
	ComponentTypeID string   `json:"component_type_id"`
	OwnerTeamID     *string  `json:"owner_team_id"`
	RelevanceTagIDs []string `json:"relevance_tag_ids"`
	FilterTagIDs    []string `json:"filter_tag_ids"`
	AlertEnabled    bool     `json:"alert_enabled"`
}

// RollupEffect maps a child state to a parent effect.
type RollupEffect string

const (
	RollupEffectNone   RollupEffect = "none"
	RollupEffectYellow RollupEffect = "yellow"
	RollupEffectRed    RollupEffect = "red"
)

// RollupRule is one ordered aggregate rule.
type RollupRule struct {
	ChildComponentIDs []string     `json:"child_component_ids"`
	WhenChildYellow   RollupEffect `json:"when_child_yellow"`
	WhenChildRed      RollupEffect `json:"when_child_red"`
}

// ComponentRollup is the complete ruleset owned by one parent component.
type ComponentRollup struct {
	ParentComponentID string       `json:"parent_component_id"`
	Rules             []RollupRule `json:"rules"`
	Revision          int64        `json:"revision"`
}

// ComponentRollupReplaceRequest atomically replaces the complete ordered ruleset.
type ComponentRollupReplaceRequest struct {
	Rules []RollupRule `json:"rules"`
}

// IntegrationSource identifies the accepted signal adapter.
type IntegrationSource string

const IntegrationSourceGrafana IntegrationSource = "grafana"

// IntegrationLifecycleOwner protects human-owned integrations from accidental
// automation takeover.
type IntegrationLifecycleOwner string

const (
	IntegrationLifecycleOwnerHuman      IntegrationLifecycleOwner = "human"
	IntegrationLifecycleOwnerAutomation IntegrationLifecycleOwner = "automation"
)

// IntegrationStatus is computed integration lifecycle state.
type IntegrationStatus string

const (
	IntegrationStatusActive   IntegrationStatus = "active"
	IntegrationStatusArchived IntegrationStatus = "archived"
)

// ComponentIntegration is the non-secret integration configuration.
type ComponentIntegration struct {
	ID                  string                    `json:"id"`
	ComponentID         string                    `json:"component_id"`
	Source              IntegrationSource         `json:"source"`
	SourceKey           string                    `json:"source_key"`
	Endpoint            string                    `json:"endpoint"`
	LifecycleOwner      IntegrationLifecycleOwner `json:"lifecycle_owner"`
	Status              IntegrationStatus         `json:"status"`
	CredentialVersionID string                    `json:"credential_version_id"`
	Revision            int64                     `json:"revision"`
	ArchivedAt          *time.Time                `json:"archived_at"`
}

// ComponentIntegrationSecret is returned once on issue, rotation, or adoption.
// Its formatter redacts both the plaintext and version in diagnostics.
type ComponentIntegrationSecret struct {
	Value     string `json:"value"`
	VersionID string `json:"version_id"`
}

// Format redacts the one-time plaintext for every fmt verb, including nested
// `%+v` and `%#v` formatting of ComponentIntegrationMutation.
func (ComponentIntegrationSecret) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<sensitive>")
}

// ComponentIntegrationMutation contains metadata and an optional one-time secret.
type ComponentIntegrationMutation struct {
	Integration ComponentIntegration        `json:"integration"`
	Secret      *ComponentIntegrationSecret `json:"secret"`
}

// ComponentIntegrationCreateRequest binds one stable Grafana mapping identity.
type ComponentIntegrationCreateRequest struct {
	Source    IntegrationSource `json:"source"`
	SourceKey string            `json:"source_key"`
}

// MutationOptions carries the current configuration revision. Idempotency keys
// are intentionally generated inside each client method and are never persisted
// or derived from Terraform configuration.
type MutationOptions struct {
	Revision                     int64
	RevokePredecessorImmediately bool
}

// CatalogAPI provides organization identity and read-only reference catalogs.
type CatalogAPI interface {
	CurrentOrganization(context.Context) (Organization, error)
	ListComponentTypes(context.Context, ListOptions) (Page[ComponentType], error)
	ListTeams(context.Context, ListOptions) (Page[Team], error)
	ListTags(context.Context, ListOptions) (Page[Tag], error)
}

// ComponentAPI manages desired component configuration.
type ComponentAPI interface {
	CreateComponent(context.Context, ComponentCreateRequest, MutationOptions) (Component, error)
	GetComponent(context.Context, string) (Component, error)
	UpdateComponent(context.Context, string, ComponentUpdateRequest, MutationOptions) (Component, error)
	ArchiveComponent(context.Context, string, MutationOptions) error
}

// RollupAPI manages one complete rollup ruleset per parent component.
type RollupAPI interface {
	GetComponentRollup(context.Context, string) (ComponentRollup, error)
	ReplaceComponentRollup(context.Context, string, ComponentRollupReplaceRequest, MutationOptions) (ComponentRollup, error)
	DeleteComponentRollup(context.Context, string, MutationOptions) error
}

// IntegrationAPI manages a component-bound ingestion integration.
type IntegrationAPI interface {
	GetComponentIntegration(context.Context, string) (ComponentIntegration, error)
	CreateComponentIntegration(context.Context, string, ComponentIntegrationCreateRequest, MutationOptions) (ComponentIntegrationMutation, error)
	RotateComponentIntegration(context.Context, string, MutationOptions) (ComponentIntegrationMutation, error)
	AdoptComponentIntegration(context.Context, string, MutationOptions) (ComponentIntegrationMutation, error)
	DeleteComponentIntegration(context.Context, string, MutationOptions) error
}

// AutomationAPI is the complete typed client contract. API remains separately
// embedded so provider configuration tests can mock transport without
// implementing every domain operation.
type AutomationAPI interface {
	API
	CatalogAPI
	ComponentAPI
	RollupAPI
	IntegrationAPI
}
