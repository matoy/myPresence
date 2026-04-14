package models

import (
	"testing"
)

func TestUserHasRole(t *testing.T) {
	tests := []struct {
		name     string
		roles    string
		check    string
		expected bool
	}{
		{"basic role matches", "basic", RoleBasic, true},
		{"global grants any role", "global", RoleTeamManager, true},
		{"global grants floorplan_manager", "global", RoleFloorplanManager, true},
		{"global grants team_leader", "global", RoleTeamLeader, true},
		{"single role no match", "basic", RoleTeamManager, false},
		{"multiple roles match", "basic,team_manager", RoleTeamManager, true},
		{"multiple roles no match", "basic,team_manager", RoleActivityViewer, false},
		{"empty roles", "", RoleBasic, false},
		{"whitespace trimmed", "basic, team_manager", RoleTeamManager, true},
		{"floorplan_manager matches", "floorplan_manager", RoleFloorplanManager, true},
		{"team_leader matches", "team_leader", RoleTeamLeader, true},
		{"team_leader no match for team_manager", "team_leader", RoleTeamManager, false},
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

// TestAllRoles_ContainsAllConstants verifies that AllRoles lists every defined role constant.
func TestAllRoles_ContainsAllConstants(t *testing.T) {
	wantRoles := []string{
		RoleBasic,
		RoleTeamManager,
		RoleTeamLeader,
		RoleStatusManager,
		RoleActivityViewer,
		RoleFloorplanManager,
		RoleGlobal,
	}
	for _, want := range wantRoles {
		found := false
		for _, r := range AllRoles {
			if r.ID == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AllRoles is missing role constant %q", want)
		}
	}
}

// TestAllRoles_NoBlankLabel ensures every entry in AllRoles has a non-empty label.
func TestAllRoles_NoBlankLabel(t *testing.T) {
	for _, r := range AllRoles {
		if r.Label == "" {
			t.Errorf("AllRoles entry %q has an empty label", r.ID)
		}
	}
}

// TestPresence_HalfValues documents the accepted values for Presence.Half.
func TestPresence_HalfValues(t *testing.T) {
	validHalves := []string{"full", "AM", "PM"}
	for _, h := range validHalves {
		p := Presence{Half: h}
		if p.Half != h {
			t.Errorf("Presence.Half round-trip failed for %q", h)
		}
	}
}

// TestUserStats_HalfDayAccounting verifies that BillableDays and OnSiteDays
// can hold decimal values (0.5 per half-day).
func TestUserStats_HalfDayAccounting(t *testing.T) {
	stats := UserStats{
		StatusCounts: map[int64]float64{1: 0.5, 2: 1.5},
		BillableDays: 2.0,
		OnSiteDays:   1.5,
	}
	if stats.BillableDays != 2.0 {
		t.Errorf("BillableDays = %v, want 2.0", stats.BillableDays)
	}
	if stats.OnSiteDays != 1.5 {
		t.Errorf("OnSiteDays = %v, want 1.5", stats.OnSiteDays)
	}
	sum := 0.0
	for _, v := range stats.StatusCounts {
		sum += v
	}
	if sum != 2.0 {
		t.Errorf("StatusCounts sum = %v, want 2.0", sum)
	}
}
