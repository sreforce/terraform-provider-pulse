package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const automationAPIBasePath = "api/automation/v1"

// CurrentOrganization returns the organization derived from the automation key.
func (c *Client) CurrentOrganization(ctx context.Context) (Organization, error) {
	var result Organization
	err := c.get(ctx, automationAPIBasePath+"/organization", &result)
	if err == nil {
		err = validateOrganization(result)
	}
	if err != nil {
		return Organization{}, err
	}
	return result, err
}

// ListComponentTypes returns one stable cursor page of component types.
func (c *Client) ListComponentTypes(ctx context.Context, options ListOptions) (Page[ComponentType], error) {
	var result Page[ComponentType]
	err := c.get(ctx, collectionPath("component-types", options), &result)
	if err == nil {
		err = validateComponentTypes(result)
	}
	if err != nil {
		return Page[ComponentType]{}, err
	}
	return result, err
}

func (c *Client) CreateComponentType(ctx context.Context, payload ComponentTypeWriteRequest, options MutationOptions) (ComponentType, error) {
	return createCatalogItem(c, ctx, "component-types", payload, options, validateComponentType)
}

func (c *Client) GetComponentType(ctx context.Context, id string) (ComponentType, error) {
	return getCatalogItem(c, ctx, "component-types", id, validateComponentType, func(item ComponentType) string { return item.ID })
}

func (c *Client) UpdateComponentType(ctx context.Context, id string, payload ComponentTypeWriteRequest, options MutationOptions) (ComponentType, error) {
	return updateCatalogItem(c, ctx, "component-types", id, payload, options, validateComponentType, func(item ComponentType) string { return item.ID })
}

func (c *Client) DeleteComponentType(ctx context.Context, id string, options MutationOptions) error {
	return deleteCatalogItem(c, ctx, "component-types", id, options)
}

// ListTeams returns one stable cursor page of owner teams.
func (c *Client) ListTeams(ctx context.Context, options ListOptions) (Page[Team], error) {
	var result Page[Team]
	err := c.get(ctx, collectionPath("teams", options), &result)
	if err == nil {
		err = validateTeams(result)
	}
	if err != nil {
		return Page[Team]{}, err
	}
	return result, err
}

func (c *Client) CreateTeam(ctx context.Context, payload TeamWriteRequest, options MutationOptions) (Team, error) {
	return createCatalogItem(c, ctx, "teams", payload, options, validateTeam)
}

func (c *Client) GetTeam(ctx context.Context, id string) (Team, error) {
	return getCatalogItem(c, ctx, "teams", id, validateTeam, func(item Team) string { return item.ID })
}

func (c *Client) UpdateTeam(ctx context.Context, id string, payload TeamWriteRequest, options MutationOptions) (Team, error) {
	return updateCatalogItem(c, ctx, "teams", id, payload, options, validateTeam, func(item Team) string { return item.ID })
}

func (c *Client) DeleteTeam(ctx context.Context, id string, options MutationOptions) error {
	return deleteCatalogItem(c, ctx, "teams", id, options)
}

// ListTags returns one stable cursor page of tags.
func (c *Client) ListTags(ctx context.Context, options ListOptions) (Page[Tag], error) {
	var result Page[Tag]
	err := c.get(ctx, collectionPath("tags", options), &result)
	if err == nil {
		err = validateTags(result)
	}
	if err != nil {
		return Page[Tag]{}, err
	}
	return result, err
}

func (c *Client) CreateTag(ctx context.Context, payload TagWriteRequest, options MutationOptions) (Tag, error) {
	return createCatalogItem(c, ctx, "tags", payload, options, validateTag)
}

func (c *Client) GetTag(ctx context.Context, id string) (Tag, error) {
	return getCatalogItem(c, ctx, "tags", id, validateTag, func(item Tag) string { return item.ID })
}

func (c *Client) UpdateTag(ctx context.Context, id string, payload TagWriteRequest, options MutationOptions) (Tag, error) {
	return updateCatalogItem(c, ctx, "tags", id, payload, options, validateTag, func(item Tag) string { return item.ID })
}

func (c *Client) DeleteTag(ctx context.Context, id string, options MutationOptions) error {
	return deleteCatalogItem(c, ctx, "tags", id, options)
}

func createCatalogItem[T any](c *Client, ctx context.Context, collection string, payload any, options MutationOptions, validate func(T) error) (T, error) {
	var result T
	if err := requireCreateOptions(options); err != nil {
		return result, err
	}
	err := c.mutate(ctx, http.MethodPost, automationAPIBasePath+"/"+collection, payload, noPrecondition, &result)
	if err == nil {
		err = validate(result)
	}
	return result, err
}

func getCatalogItem[T any](c *Client, ctx context.Context, collection string, id string, validate func(T) error, identity func(T) string) (T, error) {
	var result T
	path, err := catalogItemPath(collection, id)
	if err != nil {
		return result, err
	}
	err = c.get(ctx, path, &result)
	if err == nil {
		err = validate(result)
	}
	if err == nil && identity(result) != id {
		err = contractError(collection + " read")
	}
	return result, err
}

func updateCatalogItem[T any](c *Client, ctx context.Context, collection string, id string, payload any, options MutationOptions, validate func(T) error, identity func(T) string) (T, error) {
	var result T
	path, err := catalogItemPath(collection, id)
	if err != nil {
		return result, err
	}
	precondition, err := standardRevisionPrecondition(options)
	if err != nil {
		return result, err
	}
	err = c.mutate(ctx, http.MethodPatch, path, payload, precondition, &result)
	if err == nil {
		err = validate(result)
	}
	if err == nil && identity(result) != id {
		err = contractError(collection + " update")
	}
	return result, err
}

func deleteCatalogItem(c *Client, ctx context.Context, collection string, id string, options MutationOptions) error {
	path, err := catalogItemPath(collection, id)
	if err != nil {
		return err
	}
	precondition, err := standardRevisionPrecondition(options)
	if err != nil {
		return err
	}
	return c.mutate(ctx, http.MethodDelete, path, nil, precondition, nil)
}

func catalogItemPath(collection string, id string) (string, error) {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, "/?#") {
		return "", errors.New("pulse catalog resource id must be a non-empty UUID")
	}
	return automationAPIBasePath + "/" + collection + "/" + url.PathEscape(id), nil
}

// CreateComponent creates or explicitly restores a component by immutable
// external_key. The client generates one fresh idempotency key for this call.
func (c *Client) CreateComponent(ctx context.Context, payload ComponentCreateRequest, options MutationOptions) (Component, error) {
	var result Component
	if err := requireCreateOptions(options); err != nil {
		return result, err
	}
	err := c.mutate(ctx, http.MethodPost, automationAPIBasePath+"/components", payload, noPrecondition, &result)
	if err == nil {
		err = validateComponent(result)
	}
	if err == nil && result.ExternalKey != payload.ExternalKey {
		err = contractError("component create")
	}
	if err != nil {
		return Component{}, err
	}
	return result, err
}

// GetComponent reads one active component by UUID.
func (c *Client) GetComponent(ctx context.Context, componentID string) (Component, error) {
	var result Component
	path, err := componentPath(componentID)
	if err != nil {
		return result, err
	}
	err = c.get(ctx, path, &result)
	if err == nil {
		err = validateComponent(result)
	}
	if err == nil && result.ID != componentID {
		err = contractError("component read")
	}
	if err != nil {
		return Component{}, err
	}
	return result, err
}

// UpdateComponent replaces the Terraform-managed mutable configuration.
func (c *Client) UpdateComponent(ctx context.Context, componentID string, payload ComponentUpdateRequest, options MutationOptions) (Component, error) {
	var result Component
	path, err := componentPath(componentID)
	if err != nil {
		return result, err
	}
	precondition, err := standardRevisionPrecondition(options)
	if err != nil {
		return result, err
	}
	err = c.mutate(ctx, http.MethodPatch, path, payload, precondition, &result)
	if err == nil {
		err = validateComponent(result)
	}
	if err == nil && result.ID != componentID {
		err = contractError("component update")
	}
	if err != nil {
		return Component{}, err
	}
	return result, err
}

// ArchiveComponent archives a component while retaining operational history.
func (c *Client) ArchiveComponent(ctx context.Context, componentID string, options MutationOptions) error {
	path, err := componentPath(componentID)
	if err != nil {
		return err
	}
	precondition, err := standardRevisionPrecondition(options)
	if err != nil {
		return err
	}
	return c.mutate(ctx, http.MethodDelete, path, nil, precondition, nil)
}

// GetComponentRollup reads the complete ordered ruleset for a parent component.
func (c *Client) GetComponentRollup(ctx context.Context, parentComponentID string) (ComponentRollup, error) {
	var result ComponentRollup
	path, err := componentSubresourcePath(parentComponentID, "rollup")
	if err != nil {
		return result, err
	}
	err = c.get(ctx, path, &result)
	if err == nil {
		err = validateRollup(result, parentComponentID)
	}
	if err != nil {
		return ComponentRollup{}, err
	}
	return result, err
}

// ReplaceComponentRollup creates or atomically replaces a complete ruleset.
// Revision zero means create-only and emits If-None-Match: *.
func (c *Client) ReplaceComponentRollup(ctx context.Context, parentComponentID string, payload ComponentRollupReplaceRequest, options MutationOptions) (ComponentRollup, error) {
	var result ComponentRollup
	path, err := componentSubresourcePath(parentComponentID, "rollup")
	if err != nil {
		return result, err
	}
	precondition, err := createOrRevisionPrecondition(options)
	if err != nil {
		return result, err
	}
	err = c.mutate(ctx, http.MethodPut, path, payload, precondition, &result)
	if err == nil {
		err = validateRollup(result, parentComponentID)
	}
	if err != nil {
		return ComponentRollup{}, err
	}
	return result, err
}

// DeleteComponentRollup removes the complete ruleset from a parent component.
func (c *Client) DeleteComponentRollup(ctx context.Context, parentComponentID string, options MutationOptions) error {
	path, err := componentSubresourcePath(parentComponentID, "rollup")
	if err != nil {
		return err
	}
	precondition, err := standardRevisionPrecondition(options)
	if err != nil {
		return err
	}
	return c.mutate(ctx, http.MethodDelete, path, nil, precondition, nil)
}

// GetComponentIntegration reads non-secret integration metadata.
func (c *Client) GetComponentIntegration(ctx context.Context, componentID string, provider IntegrationProvider) (ComponentIntegration, error) {
	var result ComponentIntegration
	path, err := componentIntegrationPath(componentID, provider)
	if err != nil {
		return result, err
	}
	err = c.get(ctx, path, &result)
	if err == nil {
		err = validateIntegration(result, componentID, provider, c.allowInsecureHTTP)
	}
	if err != nil {
		return ComponentIntegration{}, err
	}
	return result, err
}

// UpsertComponentIntegration creates or restores one provider binding. Setting
// adopt explicitly transfers a human-owned binding to automation atomically.
func (c *Client) UpsertComponentIntegration(ctx context.Context, componentID string, provider IntegrationProvider, payload ComponentIntegrationUpsertRequest, options MutationOptions) (ComponentIntegrationMutation, error) {
	var result ComponentIntegrationMutation
	path, err := componentIntegrationPath(componentID, provider)
	if err != nil {
		return result, err
	}
	precondition, err := upsertRevisionPrecondition(options)
	if err != nil {
		return result, err
	}
	err = c.mutate(ctx, http.MethodPut, path, payload, precondition, &result)
	if err == nil {
		err = validateIntegrationMutation(result, componentID, provider, c.allowInsecureHTTP)
	}
	if err != nil {
		result = ComponentIntegrationMutation{}
	}
	return result, err
}

// RotateComponentIntegration issues a successor credential and revokes the
// predecessor according to Pulse's bounded overlap policy.
func (c *Client) RotateComponentIntegration(ctx context.Context, componentID string, provider IntegrationProvider, options MutationOptions) (ComponentIntegrationMutation, error) {
	return c.mutateIntegrationAction(ctx, componentID, provider, "rotate", options, struct {
		RevokePredecessorImmediately bool `json:"revoke_predecessor_immediately,omitempty"`
	}{RevokePredecessorImmediately: options.RevokePredecessorImmediately})
}

// DeleteComponentIntegration archives the binding and disables ingress.
func (c *Client) DeleteComponentIntegration(ctx context.Context, componentID string, provider IntegrationProvider, options MutationOptions) error {
	path, err := componentIntegrationPath(componentID, provider)
	if err != nil {
		return err
	}
	precondition, err := standardRevisionPrecondition(options)
	if err != nil {
		return err
	}
	return c.mutate(ctx, http.MethodDelete, path, nil, precondition, nil)
}

func (c *Client) mutateIntegrationAction(ctx context.Context, componentID string, provider IntegrationProvider, action string, options MutationOptions, payload any) (ComponentIntegrationMutation, error) {
	var result ComponentIntegrationMutation
	path, err := componentIntegrationPath(componentID, provider)
	if err != nil {
		return result, err
	}
	path += "/" + action
	precondition, err := revisionPrecondition(options)
	if err != nil {
		return result, err
	}
	err = c.mutate(ctx, http.MethodPost, path, payload, precondition, &result)
	if err == nil {
		err = validateIntegrationMutation(result, componentID, provider, c.allowInsecureHTTP)
	}
	if err != nil {
		result = ComponentIntegrationMutation{}
	}
	return result, err
}

func componentIntegrationPath(componentID string, provider IntegrationProvider) (string, error) {
	if !validIntegrationProvider(provider) {
		return "", errors.New("pulse integration provider must be grafana, pagerduty, or pulse")
	}
	return componentSubresourcePath(componentID, "integrations/"+string(provider))
}

func (c *Client) get(ctx context.Context, path string, result any) error {
	request, err := c.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return c.Do(request, result)
}

type mutationPrecondition struct {
	revision   int64
	createOnly bool
}

var noPrecondition = mutationPrecondition{}

func (c *Client) mutate(ctx context.Context, method string, path string, payload any, precondition mutationPrecondition, result any) error {
	request, err := c.NewRequest(ctx, method, path, payload)
	if err != nil {
		return err
	}
	idempotencyKey, err := newIdempotencyKey()
	if err != nil {
		return err
	}
	request.Header.Set("Idempotency-Key", idempotencyKey)
	if precondition.createOnly {
		request.Header.Set("If-None-Match", "*")
	} else if precondition.revision > 0 {
		request.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(precondition.revision, 10)))
	}
	return c.Do(request, result)
}

func newIdempotencyKey() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("generate Pulse API idempotency key")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func requireCreateOptions(options MutationOptions) error {
	if options.Revision != 0 || options.RevokePredecessorImmediately {
		return errors.New("pulse create mutation options are invalid")
	}
	return nil
}

func revisionPrecondition(options MutationOptions) (mutationPrecondition, error) {
	if options.Revision <= 0 {
		return mutationPrecondition{}, errors.New("pulse mutation revision must be greater than zero")
	}
	return mutationPrecondition{revision: options.Revision}, nil
}

func standardRevisionPrecondition(options MutationOptions) (mutationPrecondition, error) {
	if options.RevokePredecessorImmediately {
		return mutationPrecondition{}, errors.New("pulse mutation options are invalid")
	}
	return revisionPrecondition(options)
}

func createOrRevisionPrecondition(options MutationOptions) (mutationPrecondition, error) {
	if options.RevokePredecessorImmediately {
		return mutationPrecondition{}, errors.New("pulse rollup mutation options are invalid")
	}
	if options.Revision < 0 {
		return mutationPrecondition{}, errors.New("pulse mutation revision must not be negative")
	}
	if options.Revision == 0 {
		return mutationPrecondition{createOnly: true}, nil
	}
	return mutationPrecondition{revision: options.Revision}, nil
}

func upsertRevisionPrecondition(options MutationOptions) (mutationPrecondition, error) {
	if options.RevokePredecessorImmediately || options.Revision < 0 {
		return mutationPrecondition{}, errors.New("pulse integration upsert mutation options are invalid")
	}
	if options.Revision == 0 {
		return noPrecondition, nil
	}
	return mutationPrecondition{revision: options.Revision}, nil
}

func componentPath(componentID string) (string, error) {
	segment, err := safePathSegment(componentID)
	if err != nil {
		return "", err
	}
	return automationAPIBasePath + "/components/" + segment, nil
}

func componentSubresourcePath(componentID string, suffix string) (string, error) {
	path, err := componentPath(componentID)
	if err != nil {
		return "", err
	}
	return path + "/" + suffix, nil
}

func safePathSegment(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || len(raw) > 256 || strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("pulse resource identifier is invalid")
	}
	return url.PathEscape(raw), nil
}

func collectionPath(collection string, options ListOptions) string {
	values := make(url.Values)
	if options.Cursor != "" {
		values.Set("cursor", options.Cursor)
	}
	if options.Limit > 0 {
		values.Set("limit", strconv.Itoa(options.Limit))
	}
	path := automationAPIBasePath + "/" + collection
	if query := values.Encode(); query != "" {
		path += "?" + query
	}
	return path
}

var _ AutomationAPI = (*Client)(nil)
