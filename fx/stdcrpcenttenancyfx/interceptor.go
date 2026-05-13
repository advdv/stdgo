package stdcrpcenttenancyfx

// This file holds the wire-side half of stdcrpcenttenancyfx: the ctx
// stamping helpers ([WithDatabaseRole] / [DatabaseRoleFromContext]),
// the [DatabaseRoleResolver] interface and its proto-extension-backed
// implementation, the [ProtoExtensionDatabaseRole] fx wiring helper,
// and the Connect [Authorize.Interceptor] that turns a per-procedure
// `db_role` annotation into a ctx-stamped [DatabaseRole].
//
// Kept separate from [stdcrpcenttenancyfx.go] (which owns the BeginHook
// and fx graph) so the wire boundary is reviewable on its own — adding
// a new role-decision input (e.g. an HTTP header instead of the proto
// annotation) is a localized change to this file.

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"github.com/cockroachdb/errors"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// databaseRoleKey is the ctx key the database role value is stored
// under. Unexported; callers go through [WithDatabaseRole] /
// [DatabaseRoleFromContext].
type databaseRoleKey struct{}

// WithDatabaseRole returns a copy of ctx with the given database role
// stamped on it. The role is read by [Authorize.BeginHook] when a
// transaction is opened against an stdcrpcenttenancyfx-managed
// transactor.
//
// In production this function has exactly two legitimate callers:
//
//   - the stdcrpcenttenancyfx Connect interceptor, which stamps the
//     role declared by the procedure's `db_role` proto annotation;
//   - trusted internal code paths that have no inbound RPC (Temporal
//     activities, system bootstrap, test seed helpers), which stamp
//     [DatabaseRoleSysuser] explicitly.
//
// All other call sites are bypasses of the proto-driven role decision
// and are gated by a project-wide forbidigo lint rule that requires a
// `//nolint:forbidigo // <one-line justification>` comment at every
// call site. Code review should treat any new nolint as a privilege
// escalation that needs an explicit reason.
func WithDatabaseRole(ctx context.Context, role DatabaseRole) context.Context {
	return context.WithValue(ctx, databaseRoleKey{}, role)
}

// DatabaseRoleFromContext reads the database role previously stamped
// by [WithDatabaseRole]. Returns ([DatabaseRoleUnspecified], false)
// when no role is set.
func DatabaseRoleFromContext(ctx context.Context) (DatabaseRole, bool) {
	r, ok := ctx.Value(databaseRoleKey{}).(DatabaseRole)

	return r, ok
}

// DatabaseRoleResolver resolves the [DatabaseRole] required by a
// ConnectRPC procedure. Mirrors stdcrpcauthfx.ScopeResolver's shape
// so the stdcrpcenttenancyfx interceptor stays decoupled from the
// proto extension type.
type DatabaseRoleResolver interface {
	// RequiredDatabaseRole returns the role declared by the procedure's
	// proto annotation, or an error if the annotation is missing or
	// invalid.
	RequiredDatabaseRole(procedure string) (DatabaseRole, error)
	// AllProcedures returns every RPC procedure the resolver knows
	// about (e.g. by walking the proto registry). Used at boot to
	// validate the annotation is present on every method.
	AllProcedures() ([]string, error)
}

// protoExtensionDatabaseRoleResolver resolves database roles by reading
// an enum-typed protobuf method option extension on the procedure's
// MethodDescriptor. Mirrors stdcrpcauthfx.protoExtensionScopeResolver.
type protoExtensionDatabaseRoleResolver struct {
	ext protoreflect.ExtensionType
}

// RequiredDatabaseRole implements [DatabaseRoleResolver].
func (r *protoExtensionDatabaseRoleResolver) RequiredDatabaseRole(procedure string) (DatabaseRole, error) {
	desc, err := lookupMethodDescriptor(procedure)
	if err != nil {
		return DatabaseRoleUnspecified, err
	}

	return databaseRoleFromMethodDescriptor(desc, r.ext)
}

// AllProcedures implements [DatabaseRoleResolver]. It walks
// [protoregistry.GlobalFiles] and returns every RPC procedure declared
// in a file that lives in the same proto package as the extension —
// i.e. the proto package that owns the role annotation. Limiting the
// walk to that package avoids spuriously requiring the annotation on
// third-party services (buf.validate, descriptor, …) that happen to
// be linked into the binary.
func (r *protoExtensionDatabaseRoleResolver) AllProcedures() ([]string, error) {
	parentPkg := r.ext.TypeDescriptor().ParentFile().Package()

	var procs []string

	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if file.Package() != parentPkg {
			return true
		}

		services := file.Services()
		for i := range services.Len() {
			svc := services.Get(i)
			methods := svc.Methods()
			for j := range methods.Len() {
				m := methods.Get(j)
				procs = append(procs, "/"+string(svc.FullName())+"/"+string(m.Name()))
			}
		}

		return true
	})

	return procs, nil
}

// lookupMethodDescriptor maps a Connect procedure path
// ("/pkg.Service/Method") to its [protoreflect.MethodDescriptor].
// Mirrors stdcrpcauthfx's procedure → descriptor mapping verbatim so
// both packages agree on the lookup convention.
func lookupMethodDescriptor(procedure string) (protoreflect.MethodDescriptor, error) {
	fullName := strings.TrimPrefix(procedure, "/")
	fullName = strings.Replace(fullName, "/", ".", 1)

	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(fullName))
	if err != nil {
		return nil, errors.Wrapf(err, "stdcrpcenttenancyfx: find descriptor for %q", fullName)
	}

	method, ok := desc.(protoreflect.MethodDescriptor)
	if !ok {
		return nil, errors.Newf("stdcrpcenttenancyfx: %q is not a method descriptor", fullName)
	}

	return method, nil
}

// databaseRoleFromMethodDescriptor reads the [DatabaseRole] from the
// method's options. Returns an error when the annotation is missing or
// carries the proto-required zero value (DB_ROLE_UNSPECIFIED): both
// indicate the method author forgot to declare the posture, which is
// what the boot-time validation surfaces.
func databaseRoleFromMethodDescriptor(
	method protoreflect.MethodDescriptor, ext protoreflect.ExtensionType,
) (DatabaseRole, error) {
	opts, ok := method.Options().(*descriptorpb.MethodOptions)
	if !ok || opts == nil {
		return DatabaseRoleUnspecified,
			errors.Newf("stdcrpcenttenancyfx: method %q has no options", method.FullName())
	}

	if !proto.HasExtension(opts, ext) {
		return DatabaseRoleUnspecified, errors.Newf(
			"stdcrpcenttenancyfx: method %q is missing the db_role annotation "+
				"(see proto/rpc/v1/auth.proto)", method.FullName(),
		)
	}

	raw := proto.GetExtension(opts, ext)

	enumVal, ok := raw.(protoreflect.Enum)
	if !ok {
		return DatabaseRoleUnspecified, errors.Newf(
			"stdcrpcenttenancyfx: method %q: db_role extension is not an enum (got %T)", method.FullName(), raw,
		)
	}

	switch int32(enumVal.Number()) {
	case 0: // DB_ROLE_UNSPECIFIED
		return DatabaseRoleUnspecified, errors.Newf(
			"stdcrpcenttenancyfx: method %q: db_role is DB_ROLE_UNSPECIFIED — "+
				"every RPC must declare an explicit posture", method.FullName(),
		)
	case 1: // DB_ROLE_ANONYMOUS
		return DatabaseRoleAnonymous, nil
	case 2: // DB_ROLE_WEBUSER
		return DatabaseRoleWebuser, nil
	case 3: // DB_ROLE_SYSUSER
		return DatabaseRoleSysuser, nil
	default:
		return DatabaseRoleUnspecified, errors.Newf(
			"stdcrpcenttenancyfx: method %q: unknown db_role enum value %d", method.FullName(), enumVal.Number(),
		)
	}
}

// ProtoExtensionDatabaseRole returns an fx.Option that provides a
// [DatabaseRoleResolver] backed by the given protobuf method-option
// extension type. Mirrors stdcrpcauthfx.ProtoExtensionScope so wiring
// stays uniform: a composition root passes
// `stdcrpcenttenancyfx.ProtoExtensionDatabaseRole(rpcv1.E_DbRole)` next to
// `stdcrpcauthfx.ProtoExtensionScope(rpcv1.E_RequiredPermission)`.
func ProtoExtensionDatabaseRole(ext protoreflect.ExtensionType) fx.Option {
	return fx.Provide(func() DatabaseRoleResolver {
		return &protoExtensionDatabaseRoleResolver{ext: ext}
	})
}

// Interceptor returns the Connect unary interceptor that, for every
// inbound server-side call, reads the procedure's [DatabaseRole] from
// the [DatabaseRoleResolver] and stamps it on ctx via
// [WithDatabaseRole]. The interceptor is registered alongside the
// other rpc interceptors; its presence is what makes the BeginHook's
// ctx-driven role decision possible.
//
// On the client side it is a no-op — the role decision is server-side
// only, and a misconfigured client interceptor would silently corrupt
// the BeginHook's view of the ctx.
//
// On the server side, a missing or invalid annotation is surfaced as
// `connect.CodeInternal` rather than swallowed: the boot-time
// validation in [New] should already have failed for any such method,
// so reaching this branch indicates a registry / wiring inconsistency.
func (a *Authorize) Interceptor(
	resolver DatabaseRoleResolver, logs *zap.Logger,
) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if req.Spec().IsClient {
				return next(ctx, req)
			}

			role, err := resolver.RequiredDatabaseRole(req.Spec().Procedure)
			if err != nil {
				logs.Error("stdcrpcenttenancyfx: failed to resolve db_role for procedure",
					zap.String("procedure", req.Spec().Procedure), zap.Error(err))

				return nil, connect.NewError(connect.CodeInternal, err)
			}

			return next(WithDatabaseRole(ctx, role), req)
		}
	}
}
