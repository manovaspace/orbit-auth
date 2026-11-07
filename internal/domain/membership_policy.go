package domain

// MembershipAction names an organization-administration operation that
// MembershipActor says who makes a membership change, so the store can run
// the policy again under the organization lock and close the gap between the
// application's read and its write (a caller demoted or a target promoted in
// between). The zero value is a system change and skips the check.
type MembershipActor struct {
	// CallerID is the acting member.
	CallerID string
	// Action is the operation being performed.
	Action MembershipAction
}

// AuthorizeMembershipAction decides.
type MembershipAction string

const (
	ActionAddMember     MembershipAction = "add_member"
	ActionRemoveMember  MembershipAction = "remove_member"
	ActionSuspendMember MembershipAction = "suspend_member"
	ActionSetRole       MembershipAction = "set_role"
	ActionInvite        MembershipAction = "invite"
	ActionRevokeInvite  MembershipAction = "revoke_invite"
	ActionListInvites   MembershipAction = "list_invites"
)

// AuthorizeMembershipAction decides whether caller may perform action. target
// is the membership being changed, or nil for operations without one (inviting,
// adding a user who is not yet a member, listing invites). newRole is the role
// being granted and is read only for ActionSetRole.
//
// Rules, in order:
//  0. An active member may remove their own membership (leave the
//     organization); the store still refuses it for the last active owner.
//  1. The caller must be an active owner or admin.
//  2. A target who is an owner can be changed only by an owner.
//  3. Granting a role needs a valid role; granting owner or admin needs an
//     owner.
//  4. An admin can change only plain members.
//
// Every refusal is ErrForbidden except an unknown role, which is
// ErrInvalidRole. The last-owner rule is enforced by the store, which holds the
// organization lock the count needs.
func AuthorizeMembershipAction(caller Membership, action MembershipAction, target *Membership, newRole MembershipRole) error {
	if caller.Status != MembershipActive {
		return ErrForbidden
	}
	if action == ActionRemoveMember && target != nil && target.UserID == caller.UserID {
		return nil
	}
	if caller.Role != RoleOwner && caller.Role != RoleAdmin {
		return ErrForbidden
	}
	if action == ActionSetRole {
		if _, err := ParseMembershipRole(string(newRole)); err != nil {
			return err
		}
		if (newRole == RoleOwner || newRole == RoleAdmin) && caller.Role != RoleOwner {
			return ErrForbidden
		}
	}
	if target != nil {
		if target.Role == RoleOwner && caller.Role != RoleOwner {
			return ErrForbidden
		}
		if caller.Role == RoleAdmin && target.Role != RoleMember {
			return ErrForbidden
		}
	}
	return nil
}
