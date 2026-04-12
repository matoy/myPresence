package models

import "testing"

func TestUserHasRole(t *testing.T) {
	tests := []struct {
		name     string
		roles    string
		check    string
		expected bool
	}{
		{"basic role matches", "basic", RoleBasic, true},
		{"global grants any role", "global", RoleTeamManager, true},
		{"single role no match", "basic", RoleTeamManager, false},
		{"multiple roles match", "basic,team_manager", RoleTeamManager, true},
		{"multiple roles no match", "basic,team_manager", RoleActivityViewer, false},
		{"empty roles", "", RoleBasic, false},
		{"whitespace trimmed", "basic, team_manager", RoleTeamManager, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &User{Roles: tt.roles}
			if got := u.HasRole(tt.check); got != tt.expected {
				t.Errorf("HasRole(%q) with roles=%q = %v, want %v", tt.check, tt.roles, got, tt.expected)
			}
		})
	}
}

func TestUserHasRole_Nil(t *testing.T) {
	var u *User
	if u.HasRole(RoleBasic) {
		t.Error("nil user should return false for HasRole")
	}
}

func TestUserHasAnyRole(t *testing.T) {
	u := &User{Roles: "basic,team_manager"}

	if !u.HasAnyRole(RoleActivityViewer, RoleTeamManager) {
		t.Error("expected HasAnyRole to return true when one role matches")
	}
	if u.HasAnyRole(RoleActivityViewer, RoleGlobal) {
		t.Error("expected HasAnyRole to return false when no role matches")
	}
}

func TestUserRoleList(t *testing.T) {
	tests := []struct {
		roles    string
		expected int
	}{
		{"basic", 1},
		{"basic,team_manager", 2},
		{"basic, team_manager, global", 3},
		{"", 0},
	}

	for _, tt := range tests {
		u := &User{Roles: tt.roles}
		got := u.RoleList()
		if len(got) != tt.expected {
			t.Errorf("RoleList(%q): got %d roles, want %d", tt.roles, len(got), tt.expected)
		}
	}
}

func TestUserRoleList_Nil(t *testing.T) {
	var u *User
	if u.RoleList() != nil {
		t.Error("nil user should return nil role list")
	}
}
