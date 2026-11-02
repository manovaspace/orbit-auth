// Package authorization provides framework-neutral, organization-scoped Role
// Based Access Control for services that use Orbit Auth identities.
//
// Products own their role definitions, assignments, persistence and resource
// boundaries. A product implements Store over its own tables and calls
// NewRBAC to obtain an Authorizer. Check answers one permission question for
// one principal and resource and denies by default; AllowedScopes lists the
// scopes where a permission is held for list endpoints. The package does not
// own users, storage, transport, authentication or entitlements.
//
// The older Policy and Evaluator types evaluate a static, organization-free
// role map. They remain available for compatibility but are deprecated in
// favour of Authorizer.
package authorization
