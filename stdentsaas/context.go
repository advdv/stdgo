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

// // NewAuthenticatedTxHook is transaction hook that sets a setting on the transaction
// // for supporting RLS policies that follow a standard multi-tenancy design outlined here:
// // https://www.flightcontrol.dev/blog/ultimate-guide-to-multi-tenant-saas-data-modeling
// func NewAuthenticatedTxHook(
// 	authenticatedUserSetting string,
// 	authenticatedOrganizationsSetting string,
// 	anonymousUserID string,
// ) TxHookFunc {
// 	return func(ctx context.Context, tx dialect.Tx) error {
// 		accsOrgs, ok := AuthenticatedOrganizations(ctx)
// 		if !ok {
// 			accsOrgs = []OrganizationRole{} // so the setting is never 'null'
// 		}

// 		jsond, err := json.Marshal(accsOrgs)
// 		if err != nil {
// 			return fmt.Errorf("failed to marshal authenticated_organizations json: %w", err)
// 		}

// 		accsUserID, ok := AuthenticatedUser(ctx)
// 		if !ok {
// 			accsUserID = anonymousUserID
// 		}

// 		if err := tx.Exec(ctx, fmt.Sprintf(`
// 			SET LOCAL %s = '%s';
// 			SET LOCAL %s = '%s';
// 		`,
// 			authenticatedUserSetting, accsUserID,
// 			authenticatedOrganizationsSetting, string(jsond),
// 		), []any{}, nil); err != nil {
// 			return fmt.Errorf("failed to set authenticated organizations setting: %w", err)
// 		}

// 		return nil
// 	}
// }
