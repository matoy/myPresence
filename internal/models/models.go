package models

import (
	"strings"
	"time"
)

// Valid roles
const (
	RoleBasic            = "basic"
	RoleTeamManager      = "team_manager"
	RoleTeamLeader       = "team_leader"
	RoleStatusManager    = "status_manager"
	RoleActivityViewer   = "activity_viewer"
	RoleGlobal           = "global"
)

// AllRoles lists all available roles with display labels.
var AllRoles = []struct {
	ID    string
	Label string
}{
	{RoleBasic, "Basic"},
	{RoleTeamManager, "Teams admin"},
	{RoleTeamLeader, "Team leader"},
	{RoleStatusManager, "Status admin"},
	{RoleActivityViewer, "Activity admin"},
	{RoleGlobal, "Global (admin)"},
}

// User represents an application user.
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Roles        string    `json:"roles"`
	PasswordHash string    `json:"-"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
}

// HasRole checks if the user has the given role, or the global role.
func (u *User) HasRole(role string) bool {
	if u == nil {
		return false
	}
	for _, r := range strings.Split(u.Roles, ",") {
		r = strings.TrimSpace(r)
		if r == RoleGlobal || r == role {
			return true
		}
	}
	return false
}

// HasAnyRole checks if the user has any of the given roles.
func (u *User) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if u.HasRole(role) {
			return true
		}
	}
	return false
}

// RoleList returns the roles as a slice.
func (u *User) RoleList() []string {
	if u == nil || u.Roles == "" {
		return nil
	}
	var roles []string
	for _, r := range strings.Split(u.Roles, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			roles = append(roles, r)
		}
	}
	return roles
}

// Team represents a team of users.
type Team struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Status represents a presence status (e.g. remote, on-site, leave).
type Status struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Billable  bool   `json:"billable"`
	OnSite    bool   `json:"on_site"`
	SortOrder int    `json:"sort_order"`
}

// Presence represents a user's status for a given date.
type Presence struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"user_id"`
	Date     string `json:"date"`
	StatusID int64  `json:"status_id"`
}

// CalendarUser holds a user with their presences for the calendar view.
type CalendarUser struct {
	User      User
	Presences map[string]int64 // date (YYYY-MM-DD) -> statusID
}

// DayInfo describes a single day in the calendar.
type DayInfo struct {
	Day                 int
	Date                string // YYYY-MM-DD
	DayName             string // Mon, Tue, etc.
	IsWeekend           bool
	IsHoliday           bool
	HolidayName         string
	HolidayAllowImputed bool
}

// Holiday represents a public holiday.
type Holiday struct {
	ID           int64  `json:"id"`
	Date         string `json:"date"`         // YYYY-MM-DD
	Name         string `json:"name"`
	AllowImputed bool   `json:"allow_imputed"` // allow presences to be set on this day
}

// UserStats holds stats for a single user over a period.
type UserStats struct {
	User         User
	StatusCounts map[int64]int // statusID -> day count
	BillableDays int
	OnSiteDays   int
}

// PresenceLog records a set or clear action on a user's presence.
type PresenceLog struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	ActorID     int64     `json:"actor_id"`
	ActorName   string    `json:"actor_name"`
	Action      string    `json:"action"` // "set" or "clear"
	Date        string    `json:"date"`   // YYYY-MM-DD (presence date)
	StatusID    int64     `json:"status_id"`
	StatusName  string    `json:"status_name"`
	StatusColor string    `json:"status_color"`
	CreatedAt   time.Time `json:"created_at"`
}

// AdminLog records an admin operation on an entity (team, status, holiday, user).
type AdminLog struct {
	ID         int64     `json:"id"`
	ActorID    int64     `json:"actor_id"`
	ActorName  string    `json:"actor_name"`
	EntityType string    `json:"entity_type"` // "team", "status", "holiday", "user"
	EntityID   int64     `json:"entity_id"`
	EntityName string    `json:"entity_name"`
	Action     string    `json:"action"`
	Details    string    `json:"details"`
	CreatedAt  time.Time `json:"created_at"`
}

// PageData is the common data passed to all templates.
type PageData struct {
	Config      interface{}
	User        *User
	Page        string
	Flash       string
	Data        interface{}
	SAMLEnabled bool
}
