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
	RoleFloorplanManager = "floorplan_manager"
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
	{RoleFloorplanManager, "Floorplan manager"},
	{RoleGlobal, "Global (admin)"},
}

// User represents an application user.
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Roles        string    `json:"roles"`
	PasswordHash string    `json:"-"`
	IsLocal      bool      `json:"is_local"`
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

// CanUseTokens returns true if the user has at least one role beyond "basic".
// Users with only the basic role are not allowed to create Personal Access Tokens.
func (u *User) CanUseTokens() bool {
	if u == nil {
		return false
	}
	for _, r := range u.RoleList() {
		if r != RoleBasic {
			return true
		}
	}
	return false
}

// FilterUsersByText returns users whose name or email contains q (case-insensitive).
// A blank query returns all users unchanged.
func FilterUsersByText(users []User, q string) []User {
	if users == nil {
		return nil
	}
	if q == "" {
		return users
	}
	lower := strings.ToLower(q)
	result := make([]User, 0, len(users))
	for _, u := range users {
		if strings.Contains(strings.ToLower(u.Name), lower) ||
			strings.Contains(strings.ToLower(u.Email), lower) {
			result = append(result, u)
		}
	}
	return result
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
	Half     string `json:"half"` // "full", "AM", or "PM"
	StatusID int64  `json:"status_id"`
}

// CalendarUser holds a user with their presences for the calendar view.
type CalendarUser struct {
	User      User
	Presences map[string]map[string]int64 // date -> half -> statusID
}

// DayInfo describes a single day in the calendar.
type DayInfo struct {
	Day                 int
	Date                string // YYYY-MM-DD
	DayIndex            int    // weekday index: 0=Sunday … 6=Saturday
	IsWeekend           bool
	IsHoliday           bool
	HolidayName         string
	HolidayAllowImputed bool
}

// Holiday represents a public holiday.
type Holiday struct {
	ID           int64  `json:"id"`
	Date         string `json:"date"` // YYYY-MM-DD
	Name         string `json:"name"`
	AllowImputed bool   `json:"allow_imputed"` // allow presences to be set on this day
}

// UserStats holds stats for a single user over a period.
type UserStats struct {
	User         User
	StatusCounts map[int64]float64 // statusID -> day count (0.5 per half-day)
	BillableDays float64
	OnSiteDays   float64
}

// PresenceLog records a set or clear action on a user's presence.
type PresenceLog struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	ActorID     int64     `json:"actor_id"`
	ActorName   string    `json:"actor_name"`
	Action      string    `json:"action"` // "set" or "clear"
	Date        string    `json:"date"`   // YYYY-MM-DD (presence date)
	Half        string    `json:"half"`   // "full", "AM", or "PM"
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

// Floorplan represents a floor map with seats.
type Floorplan struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ImagePath string `json:"image_path"`
	SortOrder int    `json:"sort_order"`
}

// Seat represents a bookable seat on a floorplan.
type Seat struct {
	ID          int64   `json:"id"`
	FloorplanID int64   `json:"floorplan_id"`
	Label       string  `json:"label"`
	XPct        float64 `json:"x_pct"` // 0–100, percent from left
	YPct        float64 `json:"y_pct"` // 0–100, percent from top
}

// SeatWithStatus is a Seat enriched with booking status for a given date/half.
type SeatWithStatus struct {
	Seat
	Status        string `json:"status"`         // "free", "mine", "taken"
	ReservationID int64  `json:"reservation_id"` // non-zero if status == "mine"
}

// SeatReservation records a seat booking.
type SeatReservation struct {
	ID        int64     `json:"id"`
	SeatID    int64     `json:"seat_id"`
	UserID    int64     `json:"user_id"`
	UserName  string    `json:"user_name"`
	Date      string    `json:"date"`
	Half      string    `json:"half"` // "full", "AM", "PM"
	CreatedAt time.Time `json:"created_at"`
}

// PersonalAccessToken represents a user-generated API token.
type PersonalAccessToken struct {
	ID          int64      `json:"id"`
	UserID      int64      `json:"user_id"`
	Description string     `json:"description"`
	TokenPrefix string     `json:"token_prefix"` // first chars of the raw token, for display only
	ExpiresAt   *time.Time `json:"expires_at"`   // nil = never expires
	LastUsedAt  *time.Time `json:"last_used_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// PageData is the common data passed to all templates.
type PageData struct {
	Config            interface{}
	User              *User
	Page              string
	Flash             string
	Data              interface{}
	SAMLEnabled       bool
	SMTPEnabled       bool
	HideFooter        bool
	AppVersion        string
	DisableFloorplans bool
	DisableAPI        bool
	// i18n
	T              map[string]string // translation map for the active language
	Lang           string            // active language code ("en", "fr", "de", "es")
	SupportedLangs interface{}       // []i18n.LangInfo — passed from main.go to avoid import cycle
	// CSRF
	CSRFToken string // HMAC-SHA256(secretKey, sessionToken); empty for unauthenticated pages
	// Impersonation
	RealAdmin *User // non-nil when an admin is currently impersonating another user
}
