# Orbit authorization

`github.com/manovaspace/orbit-auth/authorization` evaluates a service's
role-to-permission policy without a framework, database, or network call.

Orbit Auth authenticates users and issues stable subject IDs. A consuming
product owns its role definitions, assignments, persistence, and resource
boundaries. It loads those assignments at its own boundary and passes the
resolved roles to this package. This keeps permission rules reusable while
letting each product enforce its own availability and data-isolation needs.

```go
policy, err := authorization.NewEvaluator(authorization.Policy{
	"admin":   {"catalog.manage", "receipts.read", "receipts.write"},
	"manager": {"receipts.read", "receipts.write"},
})
if err != nil {
	return err
}

if !policy.Allows(userRoles, "receipts.write") {
	return ErrForbidden
}
```

Identifiers are lowercase and use letters, numbers, dots, underscores, or
hyphens. The evaluator copies the policy at construction and returns
deterministic permission lists.
