# Orbit authorization

`github.com/manovaspace/orbit-auth/authorization` answers one question without
a framework, database, or network call: may this principal do this permission
on this resource in this organization?

Orbit Auth authenticates users and issues stable subject IDs. A consuming
product owns its role definitions, assignments, persistence, and resource
boundaries. It implements the `Store` port over its own tenant-scoped tables
and passes that store to `NewRBAC`. This keeps permission rules reusable while
letting each product enforce its own availability and data-isolation needs.

```go
catalog, err := authorization.NewCatalog(
	authorization.PermissionSpec{Key: "inventory.stock.read", Description: "Read stock"},
	authorization.PermissionSpec{Key: "inventory.stock.move", Description: "Move stock"},
)
if err != nil {
	return err
}

store := memstore.New() // a product supplies its own authorization.Store
store.SetRoles(orgID, []authorization.RoleDef{
	{Key: "manager", Permissions: []authorization.Permission{"inventory.stock.read", "inventory.stock.move"}},
})
store.AddAssignment(authorization.Assignment{
	OrgID:   orgID,
	Subject: userID,
	RoleKey: "manager",
	Scope:   authorization.Scope{Type: "location", ID: locationID},
})

authz := authorization.NewRBAC(catalog, store)
principal := authorization.Principal{Subject: userID, OrgID: orgID}
resource := authorization.Resource{
	OrgID:  orgID,
	Scopes: []authorization.Scope{{Type: "location", ID: locationID}},
}

decision, err := authz.Check(ctx, principal, "inventory.stock.move", resource)
if err != nil || !decision.Allowed {
	return ErrForbidden // reason is "no_org", "org_mismatch", "unknown_permission" or "no_grant"
}

allLocations, locations, err := authz.AllowedScopes(ctx, principal, "inventory.stock.read", "location")
if err != nil {
	return err
}
_ = allLocations // true means the permission is held organization-wide
_ = locations    // otherwise the location IDs where it is held
```

Rules:

- Deny by default. A principal without an `OrgID`, or a resource owned by
  another organization, is denied. A permission outside the catalog is denied
  with reason `unknown_permission`.
- An assignment with an empty `Scope.Type` grants the role on every resource
  of that organization. A scoped assignment grants only when the resource
  lists the same scope, for example the same location.
- `ValidateRoles` checks submitted or seeded roles: identifiers use the
  existing lowercase pattern and every referenced permission must be in the
  catalog.
- `CanGrant` enforces ADR-045 D7. A principal may grant a role only when they
  hold every permission of that role on the target scope or organization-wide.
- `AllowedScopes` is the bulk form for list endpoints. It returns
  `all = true` for an organization-wide grant, or the sorted IDs of the
  requested scope type where the permission is held.
- `authorization/memstore` is an in-memory `Store` for tests and small
  products.

Identifiers are lowercase and use letters, numbers, dots, underscores, or
hyphens. The deprecated `Policy` and `Evaluator` types still evaluate a static,
organization-free role map for compatibility; new code should use
`Authorizer`.
