package authorization

import (
	"reflect"
	"testing"
)

func TestEvaluatorCopiesPolicyAndReturnsDeterministicPermissions(t *testing.T) {
	adminGrants := []Permission{"receipts.write", "inventory.read"}
	policy, err := NewEvaluator(Policy{
		"admin":   adminGrants,
		"manager": {"receipts.write", "receipts.read"},
	})
	if err != nil {
		t.Fatal(err)
	}

	adminGrants[0] = "receipts.read"
	want := []Permission{"inventory.read", "receipts.read", "receipts.write"}
	if got := policy.Permissions([]Role{"manager", "admin", "unknown"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Permissions() = %v, want %v", got, want)
	}
	if !policy.Allows([]Role{"admin"}, "receipts.write") {
		t.Fatal("Allows() denied a permission granted when the evaluator was created")
	}
	if policy.Allows([]Role{"unknown"}, "receipts.write") {
		t.Fatal("Allows() granted a permission to an unknown role")
	}
}

func TestNewEvaluatorRejectsInvalidPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
	}{
		{name: "empty policy", policy: Policy{}},
		{name: "invalid role", policy: Policy{"Admin": {"receipts.read"}}},
		{name: "empty role grants", policy: Policy{"admin": {}}},
		{name: "invalid permission", policy: Policy{"admin": {"Receipts.Read"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewEvaluator(test.policy); err == nil {
				t.Fatal("NewEvaluator() succeeded for an invalid policy")
			}
		})
	}
}

func TestNilEvaluatorFailsClosed(t *testing.T) {
	var evaluator *Evaluator
	if evaluator.Allows([]Role{"admin"}, "receipts.read") {
		t.Fatal("nil evaluator allowed a permission")
	}
	if got := evaluator.Permissions([]Role{"admin"}); len(got) != 0 {
		t.Fatalf("nil evaluator permissions = %v, want empty", got)
	}
}
