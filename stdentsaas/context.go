package stdentsaas

import (
	"context"
)

// OrganizationRole describes a role in an organization. Both fields are encoded as strings
// such that RLS policies can easily cast it to a type that is relevant in the schema
// at hand, such as UUID or an enum.
type OrganizationRole struct {
	OrganizationID string `json:"organization_id"`
	Role           string `json:"role"`
}

type ctxKey string

// WithAuthenticatedUser declares on the context that it is has access to a user with
// the provided id.
func WithAuthenticatedUser(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKey("authenticated_user"), userID)
}

// WithAuthenticatedOrganizations declares on the context that any calls carrying the context
// has access to these organizations with the provided role.
func WithAuthenticatedOrganizations(
	ctx context.Context, first OrganizationRole, more ...OrganizationRole,
) context.Context {
	ctx = context.WithValue(ctx, ctxKey("authenticated_organizations"), append([]OrganizationRole{first}, more...))
	return ctx
}

// AuthenticatedOrganizations returns the organization and roles from the context or panic.
func AuthenticatedOrganizations(ctx context.Context) (ors []OrganizationRole, ok bool) {
	ors, ok = ctx.Value(ctxKey("authenticated_organizations")).([]OrganizationRole)
	return
}

// AuthenticatedUser returns the user that the context is authenticated for.
func AuthenticatedUser(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(ctxKey("authenticated_user")).(string)
	return userID, ok
}

// WithNoTestForMaxQueryPlanCosts allow disabling the plan cost check.
func WithNoTestForMaxQueryPlanCosts(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey("no_test_for_max_query_plan_costs"), true)
}

// NoTestForMaxQueryPlanCosts returns whether the cost check is disabled.
func NoTestForMaxQueryPlanCosts(ctx context.Context) bool {
	v, ok := ctx.Value(ctxKey("no_test_for_max_query_plan_costs")).(bool)
	if !ok {
		return false
	}

	return v
}
