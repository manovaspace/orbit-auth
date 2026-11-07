package domain

import (
	"errors"
	"testing"
)

func TestParseMembershipRole(t *testing.T) {
	for _, value := range []string{"owner", "admin", "member"} {
		if role, err := ParseMembershipRole(value); err != nil || string(role) != value {
			t.Fatalf("ParseMembershipRole(%q) = %q, %v", value, role, err)
		}
	}
	for _, value := range []string{"", "root", "Owner", " admin"} {
		if _, err := ParseMembershipRole(value); !errors.Is(err, ErrInvalidRole) {
			t.Fatalf("ParseMembershipRole(%q) err = %v, want ErrInvalidRole", value, err)
		}
	}
}

func TestAuthorizeMembershipAction(t *testing.T) {
	mem := func(user string, role MembershipRole, status MembershipStatus) Membership {
		return Membership{OrgID: "o", UserID: user, Role: role, Status: status}
	}
	owner := mem("caller", RoleOwner, MembershipActive)
	admin := mem("caller", RoleAdmin, MembershipActive)
	member := mem("caller", RoleMember, MembershipActive)
	suspendedOwner := mem("caller", RoleOwner, MembershipSuspended)
	targetMember := mem("target", RoleMember, MembershipActive)
	self := Membership{OrgID: "o", UserID: "me", Role: RoleMember, Status: MembershipActive}
	selfAdmin := Membership{OrgID: "o", UserID: "me", Role: RoleAdmin, Status: MembershipActive}
	targetAdmin := mem("target", RoleAdmin, MembershipActive)
	targetOwner := mem("target", RoleOwner, MembershipActive)

	tests := []struct {
		name    string
		caller  Membership
		action  MembershipAction
		target  *Membership
		newRole MembershipRole
		want    error
	}{
		{"member cannot add", member, ActionAddMember, nil, "", ErrForbidden},
		{"member cannot invite", member, ActionInvite, nil, "", ErrForbidden},
		{"member cannot list invites", member, ActionListInvites, nil, "", ErrForbidden},
		{"member cannot revoke invite", member, ActionRevokeInvite, nil, "", ErrForbidden},
		{"member cannot remove member", member, ActionRemoveMember, &targetMember, "", ErrForbidden},
		{"owner changes the slug", owner, ActionSetSlug, nil, "", nil},
		{"admin cannot change the slug", admin, ActionSetSlug, nil, "", ErrForbidden},
		{"member cannot change the slug", member, ActionSetSlug, nil, "", ErrForbidden},
		{"suspended owner cannot change the slug", suspendedOwner, ActionSetSlug, nil, "", ErrForbidden},
		{"member leaves the organization", self, ActionRemoveMember, &self, "", nil},
		{"admin leaves the organization", selfAdmin, ActionRemoveMember, &selfAdmin, "", nil},
		{"member cannot suspend self", self, ActionSuspendMember, &self, "", ErrForbidden},
		{"member cannot promote self", self, ActionSetRole, &self, RoleOwner, ErrForbidden},
		{"admin removes member", admin, ActionRemoveMember, &targetMember, "", nil},
		{"admin suspends member", admin, ActionSuspendMember, &targetMember, "", nil},
		{"admin invites", admin, ActionInvite, nil, "", nil},
		{"admin adds user", admin, ActionAddMember, nil, "", nil},
		{"admin cannot remove admin", admin, ActionRemoveMember, &targetAdmin, "", ErrForbidden},
		{"admin cannot suspend owner", admin, ActionSuspendMember, &targetOwner, "", ErrForbidden},
		{"admin cannot promote to admin", admin, ActionSetRole, &targetMember, RoleAdmin, ErrForbidden},
		{"admin cannot promote to owner", admin, ActionSetRole, &targetMember, RoleOwner, ErrForbidden},
		{"admin cannot demote admin", admin, ActionSetRole, &targetAdmin, RoleMember, ErrForbidden},
		{"owner promotes member to admin", owner, ActionSetRole, &targetMember, RoleAdmin, nil},
		{"owner promotes admin to owner", owner, ActionSetRole, &targetAdmin, RoleOwner, nil},
		{"owner demotes owner", owner, ActionSetRole, &targetOwner, RoleAdmin, nil},
		{"owner removes admin", owner, ActionRemoveMember, &targetAdmin, "", nil},
		{"owner sets unknown role", owner, ActionSetRole, &targetMember, "root", ErrInvalidRole},
		{"owner sets empty role", owner, ActionSetRole, &targetMember, "", ErrInvalidRole},
		{"suspended owner cannot add", suspendedOwner, ActionAddMember, nil, "", ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AuthorizeMembershipAction(tt.caller, tt.action, tt.target, tt.newRole)
			if !errors.Is(got, tt.want) && got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
