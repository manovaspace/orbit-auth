// Package authorization provides framework-neutral role and permission policy
// evaluation for services that use Orbit Auth identities.
//
// The package evaluates roles already assigned by a product. It does not own
// users, role assignments, persistence, transport, or authentication. Product
// services define and store their own policies, then use Evaluator at the
// authorization boundary.
package authorization
