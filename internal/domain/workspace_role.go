package domain

import "strings"

const (
	WorkspaceRoleOwner        = "owner"
	WorkspaceRoleAdmin        = "admin"
	WorkspaceRoleViewer       = "viewer"
	WorkspaceRoleLegacyMember = "member"
)

func NormalizeWorkspaceRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case WorkspaceRoleOwner:
		return WorkspaceRoleOwner
	case WorkspaceRoleAdmin, WorkspaceRoleLegacyMember:
		return WorkspaceRoleAdmin
	case WorkspaceRoleViewer:
		return WorkspaceRoleViewer
	default:
		return ""
	}
}

func ValidWorkspaceRole(role string) bool {
	return NormalizeWorkspaceRole(role) != ""
}

func ValidWorkspaceInvitationRole(role string) bool {
	switch NormalizeWorkspaceRole(role) {
	case WorkspaceRoleAdmin, WorkspaceRoleViewer:
		return true
	default:
		return false
	}
}

func IsWorkspaceOwner(role string) bool {
	return NormalizeWorkspaceRole(role) == WorkspaceRoleOwner
}

func CanManageWorkspace(role string) bool {
	switch NormalizeWorkspaceRole(role) {
	case WorkspaceRoleOwner, WorkspaceRoleAdmin:
		return true
	default:
		return false
	}
}

func CanManageMeetingSessions(role string) bool {
	switch NormalizeWorkspaceRole(role) {
	case WorkspaceRoleOwner, WorkspaceRoleAdmin:
		return true
	default:
		return false
	}
}
