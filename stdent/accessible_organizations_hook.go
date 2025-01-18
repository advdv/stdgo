package stdent

import (
	"context"
	"encoding/json"
	"fmt"

	"entgo.io/ent/dialect"
)

// OrganizationRole describes a role in an organization. Both fields are encoded as strings
// such that RLS policies can easily cast it to a type that is relevant in the schema
// at hand, such as UUID or an enum.
type OrganizationRole struct {
	OrganizationID string `json:"organization_id"`
	Role           string `json:"role"`
}

type ctxKey string

// WithAccessibleOrganizations declares on the context that any calls carrying the context
// has access to these organizations with the provided role.
func WithAccessibleOrganizations(ctx context.Context, ors ...OrganizationRole) context.Context {
	ctx = context.WithValue(ctx, ctxKey("accessible_organizations"), ors)
	return ctx
}

// AccessibleOrganizations returns the organization and roles from the context or panic.
func AccessibleOrganizations(ctx context.Context) (ors []OrganizationRole) {
	ors, ok := ctx.Value(ctxKey("accessible_organizations")).([]OrganizationRole)
	if !ok {
		panic("stdent: context doesn't contain accessible organizations")
	}

	return
}

// NewAccessibleOrganizationsTxHook is transaction hook that sets a setting on the transaction
// for supporting RLS policies that follow the design outlined over here:
// https://www.flightcontrol.dev/blog/ultimate-guide-to-multi-tenant-saas-data-modeling
func NewAccessibleOrganizationsTxHook(setting string) TxHookFunc {
	return func(ctx context.Context, tx dialect.Tx) error {
		accsOrgs := AccessibleOrganizations(ctx)

		jsond, err := json.Marshal(accsOrgs)
		if err != nil {
			return fmt.Errorf("failed to marshal accessible_organizations json: %w", err)
		}

		if err := tx.Exec(ctx, fmt.Sprintf(`
			SET LOCAL %s = '%s';
		`, setting, string(jsond)), []any{}, nil); err != nil {
			return fmt.Errorf("failed to set accessible organizations setting: %w", err)
		}

		return nil
	}
}
