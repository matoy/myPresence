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
		{"single role no match", "basic", RoleTeamManager, false},
		{"multiple roles match", "basic,team_manager", RoleTeamManager, true},
		{"multiple roles no match", "basic,team_manager", RoleActivityViewer, false},
		{"empty roles", "", RoleBasic, false},
		{"whitespace trimmed", "basic, team_manager", RoleTeamManager, true},
		{"floorplan_manager matches", "floorplan_manager", RoleFloorplanManager, true},
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

// TestCanUseTokens verifies that only users with a role beyond "basic" can create PATs.
func TestCanUseTokens(t *testing.T) {
	tests := []struct {
		roles string
		want  bool
	}{
		{"basic", false},
		{"", false},
		{"basic,team_manager", true},
		{"global", true},
		{"activity_viewer", true},
		{"basic,global", true},
		{"status_manager", true},
	}
	for _, tt := range tests {
		t.Run(tt.roles, func(t *testing.T) {
			u := &User{Roles: tt.roles}
			if got := u.CanUseTokens(); got != tt.want {
				t.Errorf("CanUseTokens() with roles=%q = %v, want %v", tt.roles, got, tt.want)
			}
		})
	}
}

func TestCanUseTokens_Nil(t *testing.T) {
	var u *User
	if u.CanUseTokens() {
		t.Error("nil user should return false for CanUseTokens")
	}
}

// TestFilterUsersByText verifies case-insensitive name/email filtering.
func TestFilterUsersByText(t *testing.T) {
	users := []User{
		{Name: "Alice Martin", Email: "alice@example.com"},
		{Name: "Bob Dupont", Email: "bob@company.org"},
		{Name: "Charlie", Email: "charlie@example.com"},
	}

	tests := []struct {
		q    string
		want int
	}{
		{"", 3},
		{"alice", 1},
		{"ALICE", 1},
		{"example", 2},
		{"xyz", 0},
		{"bob", 1},
		{"company", 1},
		{"charlie@", 1},
	}

	for _, tt := range tests {
		t.Run(tt.q, func(t *testing.T) {
			got := FilterUsersByText(users, tt.q)
			if len(got) != tt.want {
				t.Errorf("FilterUsersByText(q=%q): got %d results, want %d", tt.q, len(got), tt.want)
			}
		})
	}
}

// TestFilterUsersByText_Empty verifies behaviour on an empty input slice.
func TestFilterUsersByText_Empty(t *testing.T) {
	if got := FilterUsersByText(nil, "alice"); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
	if got := FilterUsersByText([]User{}, "alice"); len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

// TestFilterUsersByText_EmptyQuery verifies that an empty query returns all users unchanged
// (models the "search field starts empty → all users visible" behaviour on page load).
func TestFilterUsersByText_EmptyQuery(t *testing.T) {
	users := []User{
		{Name: "Alice", Email: "alice@example.com"},
		{Name: "Bob", Email: "bob@example.com"},
	}
	got := FilterUsersByText(users, "")
	if len(got) != len(users) {
		t.Errorf("empty query: got %d results, want %d", len(got), len(users))
	}
}

// TestBasicUserHasNoAdminRole verifies that a user with only the basic role
// has no elevated roles (drives the HideAdminSection logic in MyLogsPage).
func TestBasicUserHasNoAdminRole(t *testing.T) {
	adminRoles := []string{
		RoleGlobal, RoleTeamManager,
		RoleStatusManager, RoleActivityViewer, RoleFloorplanManager,
	}
	u := &User{Roles: RoleBasic}
	for _, role := range adminRoles {
		if u.HasRole(role) {
			t.Errorf("basic-only user should not have role %q", role)
		}
	}
}

// TestNonBasicUserHasAdminRole verifies that users with any elevated role
// are correctly detected (drives HideAdminSection = false).
func TestNonBasicUserHasAdminRole(t *testing.T) {
	elevated := []string{
		RoleGlobal, RoleTeamManager,
		RoleStatusManager, RoleActivityViewer, RoleFloorplanManager,
	}
	for _, role := range elevated {
		u := &User{Roles: role}
		if !u.HasAnyRole(RoleGlobal, RoleTeamManager, RoleStatusManager, RoleActivityViewer, RoleFloorplanManager) {
			t.Errorf("user with role %q should be detected as having an admin role", role)
		}
	}
}

// TestBasicPlusElevatedUserIsDetected ensures "basic,team_manager" is treated as elevated.
func TestBasicPlusElevatedUserIsDetected(t *testing.T) {
	u := &User{Roles: "basic,team_manager"}
	if !u.HasAnyRole(RoleGlobal, RoleTeamManager, RoleStatusManager, RoleActivityViewer, RoleFloorplanManager) {
		t.Error("user with basic+team_manager should be detected as elevated")
	}
}

func TestNotification_Localize(t *testing.T) {
	tr := map[string]string{
		"notifications.reminder_presence_title": "Rappel présence FR",
		"notifications.reminder_presence_msg":   "Déclarer présence pour %d jours ouvrés.",
		"notifications.reminder_activity_title": "Rappel activité FR",
		"notifications.reminder_activity_msg":   "Déclarer activités pour %d jours.",
		"notifications.team_added_title":        "Ajout équipe FR",
		"notifications.team_added_msg":          "Ajouté à %s par %s.",
	}

	t.Run("nil tr does nothing", func(t *testing.T) {
		n := &Notification{Type: "reminder_presence", Title: "Old", Message: "Old msg"}
		n.Localize(nil)
		if n.Title != "Old" || n.Message != "Old msg" {
			t.Errorf("expected unchanged notification")
		}
	})

	t.Run("reminder_presence localization extracts days", func(t *testing.T) {
		n := &Notification{
			Type:    "reminder_presence",
			Title:   "Presence declaration reminder",
			Message: "Please declare your presence for the next 5 working day(s).",
		}
		n.Localize(tr)
		if n.Title != "Rappel présence FR" {
			t.Errorf("expected localized title, got %q", n.Title)
		}
		if n.Message != "Déclarer présence pour 5 jours ouvrés." {
			t.Errorf("expected localized message with 5 days, got %q", n.Message)
		}
	})

	t.Run("reminder_activity localization extracts days", func(t *testing.T) {
		n := &Notification{
			Type:    "reminder_activity",
			Title:   "Activity declaration reminder",
			Message: "Please declare your project activities for your 3 billable day(s).",
		}
		n.Localize(tr)
		if n.Title != "Rappel activité FR" {
			t.Errorf("expected localized title, got %q", n.Title)
		}
		if n.Message != "Déclarer activités pour 3 jours." {
			t.Errorf("expected localized message with 3 days, got %q", n.Message)
		}
	})

	t.Run("team_added localization formats link and actor", func(t *testing.T) {
		n := &Notification{
			Type:      "team_added",
			Title:     "Team assignment",
			Message:   "You were added to team Dev by Admin.",
			Link:      "Dev",
			ActorName: "Alice",
		}
		n.Localize(tr)
		if n.Title != "Ajout équipe FR" {
			t.Errorf("expected localized title, got %q", n.Title)
		}
		if n.Message != "Ajouté à Dev par Alice." {
			t.Errorf("expected localized message, got %q", n.Message)
		}
	})
}

