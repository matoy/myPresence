package models

import (
	"fmt"
	"strings"
	"time"
)

// Valid roles
const (
	RoleBasic            = "basic"
	RoleTeamManager      = "team_manager"
	RoleStatusManager    = "status_manager"
	RoleActivityViewer   = "activity_viewer"
	RoleFloorplanManager = "floorplan_manager"
	RoleProjectsManager  = "projects_manager"
	RoleProjectsViewer   = "projects_viewer"
	RoleGlobal           = "global"
)

// AllRoles lists all available roles with display labels.
var AllRoles = []struct {
	ID    string
	Label string
}{
	{RoleBasic, "Basic"},
	{RoleTeamManager, "Team manager"},
	{RoleStatusManager, "Status manager"},
	{RoleActivityViewer, "Activity viewer"},
	{RoleFloorplanManager, "Floorplan manager"},
	{RoleProjectsManager, "Projects manager"},
	{RoleProjectsViewer, "Projects viewer"},
	{RoleGlobal, "Global (admin)"},
}

// User represents an application user.
type User struct {
	ID              int64     `json:"id"`
	Email           string    `json:"email"`
	Name            string    `json:"name"`
	Roles           string    `json:"roles"`
	PasswordHash    string    `json:"-"`
	IsLocal         bool      `json:"is_local"`
	Disabled        bool      `json:"disabled"`
	SiteID          int64     `json:"site_id"`
	SiteName        string    `json:"site_name,omitempty"`
	SiteCountryCode string    `json:"site_country_code,omitempty"`
	Language        string    `json:"language,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
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

// Country represents a country with its ISO 3166-1 alpha-2 code, name, and flag emoji.
type Country struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Flag string `json:"flag"`
}

// AllCountries is a curated catalog of countries with ISO 3166-1 alpha-2 codes and flag emojis.
var AllCountries = []Country{
	{Code: "FR", Name: "France", Flag: "🇫🇷"},
	{Code: "MA", Name: "Morocco", Flag: "🇲🇦"},
	{Code: "CZ", Name: "Czech Republic", Flag: "🇨🇿"},
	{Code: "US", Name: "United States", Flag: "🇺🇸"},
	{Code: "DE", Name: "Germany", Flag: "🇩🇪"},
	{Code: "ES", Name: "Spain", Flag: "🇪🇸"},
	{Code: "IT", Name: "Italy", Flag: "🇮🇹"},
	{Code: "CH", Name: "Switzerland", Flag: "🇨🇭"},
	{Code: "GB", Name: "United Kingdom", Flag: "🇬🇧"},
	{Code: "BE", Name: "Belgium", Flag: "🇧🇪"},
	{Code: "PT", Name: "Portugal", Flag: "🇵🇹"},
	{Code: "TN", Name: "Tunisia", Flag: "🇹🇳"},
	{Code: "SN", Name: "Senegal", Flag: "🇸🇳"},
	{Code: "CA", Name: "Canada", Flag: "🇨🇦"},
	{Code: "IN", Name: "India", Flag: "🇮🇳"},
	{Code: "PL", Name: "Poland", Flag: "🇵🇱"},
	{Code: "RO", Name: "Romania", Flag: "🇷🇴"},
	{Code: "BR", Name: "Brazil", Flag: "🇧🇷"},
	{Code: "JP", Name: "Japan", Flag: "🇯🇵"},
	{Code: "AU", Name: "Australia", Flag: "🇦🇺"},
	{Code: "SG", Name: "Singapore", Flag: "🇸🇬"},
	{Code: "NL", Name: "Netherlands", Flag: "🇳🇱"},
	{Code: "IE", Name: "Ireland", Flag: "🇮🇪"},
	{Code: "SE", Name: "Sweden", Flag: "🇸🇪"},
	{Code: "NO", Name: "Norway", Flag: "🇳🇴"},
	{Code: "DK", Name: "Denmark", Flag: "🇩🇰"},
	{Code: "FI", Name: "Finland", Flag: "🇫🇮"},
	{Code: "AT", Name: "Austria", Flag: "🇦🇹"},
	{Code: "LU", Name: "Luxembourg", Flag: "🇱🇺"},
	{Code: "GR", Name: "Greece", Flag: "🇬🇷"},
	{Code: "MX", Name: "Mexico", Flag: "🇲🇽"},
	{Code: "AR", Name: "Argentina", Flag: "🇦🇷"},
	{Code: "CO", Name: "Colombia", Flag: "🇨🇴"},
	{Code: "CL", Name: "Chile", Flag: "🇨🇱"},
	{Code: "ZA", Name: "South Africa", Flag: "🇿🇦"},
	{Code: "EG", Name: "Egypt", Flag: "🇪🇬"},
	{Code: "CI", Name: "Ivory Coast", Flag: "🇨🇮"},
	{Code: "CM", Name: "Cameroon", Flag: "🇨🇲"},
	{Code: "AE", Name: "United Arab Emirates", Flag: "🇦🇪"},
	{Code: "SA", Name: "Saudi Arabia", Flag: "🇸🇦"},
	{Code: "QA", Name: "Qatar", Flag: "🇶🇦"},
	{Code: "TR", Name: "Turkey", Flag: "🇹🇷"},
	{Code: "UA", Name: "Ukraine", Flag: "🇺🇦"},
	{Code: "HU", Name: "Hungary", Flag: "🇭🇺"},
	{Code: "SK", Name: "Slovakia", Flag: "🇸🇰"},
	{Code: "BG", Name: "Bulgaria", Flag: "🇧🇬"},
	{Code: "HR", Name: "Croatia", Flag: "🇭🇷"},
	{Code: "RS", Name: "Serbia", Flag: "🇷🇸"},
	{Code: "NZ", Name: "New Zealand", Flag: "🇳🇿"},
	{Code: "KR", Name: "South Korea", Flag: "🇰🇷"},
	{Code: "VN", Name: "Vietnam", Flag: "🇻🇳"},
	{Code: "TH", Name: "Thailand", Flag: "🇹🇭"},
	{Code: "ID", Name: "Indonesia", Flag: "🇮🇩"},
	{Code: "PH", Name: "Philippines", Flag: "🇵🇭"},
	{Code: "MY", Name: "Malaysia", Flag: "🇲🇾"},
}

// FindCountry returns the Country for a given ISO code, or a synthetic Country if not in the catalog.
func FindCountry(code string) Country {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return Country{}
	}
	for _, c := range AllCountries {
		if c.Code == code {
			return c
		}
	}
	return Country{Code: code, Name: code, Flag: "🌐"}
}

// FlagForCountry returns the flag emoji for a given ISO code.
func FlagForCountry(code string) string {
	return FindCountry(code).Flag
}

// TeamMember is a User enriched with their departure date from a team.
// LeftAt is nil when the member is currently active.
type TeamMember struct {
	User
	LeftAt *string `json:"left_at"` // YYYY-MM-DD or nil
}

// Team represents a team of users.
type Team struct {
	ID                        int64     `json:"id"`
	Name                      string    `json:"name"`
	JiraSpaceKey              string    `json:"jira_space_key"`
	TimesheetsManagedManually bool      `json:"timesheets_managed_manually"`
	RequireActivityComment    bool      `json:"require_activity_comment"`
	DomainID                  int64     `json:"domain_id"`
	CountryCodes              string    `json:"country_codes"` // comma-separated ISO codes e.g. "FR,MA,US"
	RemindPresence            bool      `json:"remind_presence"`
	PresenceReminderDays      int       `json:"presence_reminder_days"`
	RemindActivity            bool      `json:"remind_activity"`
	ActivityReminderDays      int       `json:"activity_reminder_days"`
	CreatedAt                 time.Time `json:"created_at"`
}

// CountryList returns the list of uppercase country codes for the team.
func (t *Team) CountryList() []string {
	if t == nil || t.CountryCodes == "" {
		return nil
	}
	var list []string
	for _, c := range strings.Split(t.CountryCodes, ",") {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c != "" {
			list = append(list, c)
		}
	}
	return list
}

// Domain groups several teams under one or more managers for aggregated
// activity reporting.
type Domain struct {
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
	Disabled  bool   `json:"disabled"`
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
	HolidayCountryCode  string
}

// Holiday represents a public holiday.
type Holiday struct {
	ID           int64  `json:"id"`
	Date         string `json:"date"` // YYYY-MM-DD
	Name         string `json:"name"`
	AllowImputed bool   `json:"allow_imputed"` // allow presences to be set on this day
	CountryCode  string `json:"country_code"`  // optional comma-separated country codes e.g. "FR", "FR, MA", or "" for global/all
}

// CountryList returns the list of uppercase country codes for the holiday.
func (h Holiday) CountryList() []string {
	if h.CountryCode == "" {
		return nil
	}
	var list []string
	for _, c := range strings.Split(h.CountryCode, ",") {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c != "" {
			list = append(list, c)
		}
	}
	return list
}

// UserMatchesHoliday returns true if a holiday applies to a user with the given site country code.
//   - Global holidays (no country specified) apply to all users.
//   - If the user has no site country, only global holidays apply.
//   - If the user has a site country, a country-specific holiday applies if the user's country is in the holiday's countries.
func UserMatchesHoliday(userCountry string, hol Holiday) bool {
	hCountries := hol.CountryList()
	if len(hCountries) == 0 {
		return true
	}
	userCountry = strings.ToUpper(strings.TrimSpace(userCountry))
	if userCountry == "" {
		return false
	}
	for _, c := range hCountries {
		if c == userCountry {
			return true
		}
	}
	return false
}

// CountriesMatchHoliday returns true if all of the given country codes are covered by the holiday.
// For a multi-country team (e.g. [MA, CZ]), a holiday applies to the whole team only if both MA and CZ are covered.
func CountriesMatchHoliday(countries []string, hol Holiday) bool {
	hCountries := hol.CountryList()
	if len(hCountries) == 0 {
		return true
	}
	if len(countries) == 0 {
		return false
	}
	hMap := make(map[string]bool, len(hCountries))
	for _, c := range hCountries {
		hMap[c] = true
	}
	for _, c := range countries {
		if !hMap[strings.ToUpper(strings.TrimSpace(c))] {
			return false
		}
	}
	return true
}

// TeamMatchesHoliday returns true if a holiday applies to a team with the given country codes.
//   - Global holidays (no country specified) apply to all teams.
//   - If the team has no countries configured, only global holidays apply.
//   - If the team has countries configured, a country-specific holiday applies if and only if
//     ALL of the team's countries are included in the holiday's countries.
func TeamMatchesHoliday(teamCountries []string, hol Holiday) bool {
	hCountries := hol.CountryList()
	if len(hCountries) == 0 {
		return true
	}
	if len(teamCountries) == 0 {
		return false
	}
	hMap := make(map[string]bool, len(hCountries))
	for _, c := range hCountries {
		hMap[c] = true
	}
	for _, tc := range teamCountries {
		if !hMap[tc] {
			return false
		}
	}
	return true
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

// PresenceOverride indicates that a presence day was declared, modified, or cleared by a third party.
type PresenceOverride struct {
	ActorID   int64  `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Action    string `json:"action"` // "set" or "clear"
	Half      string `json:"half"`   // "full", "AM", or "PM"
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

// Site represents a physical building/site with a country and linked floorplans.
type Site struct {
	ID               int64        `json:"id"`
	Name             string       `json:"name"`
	CountryCode      string       `json:"country_code"`
	NotCorporateSite bool         `json:"not_corporate_site"`
	Seats            int          `json:"seats"`
	FloorplanIDs     []int64      `json:"floorplan_ids,omitempty"`
	Floorplans       []*Floorplan `json:"floorplans,omitempty"`
}

// CountryList returns the list of uppercase country codes for the site.
func (s *Site) CountryList() []string {
	if s == nil || s.CountryCode == "" {
		return nil
	}
	var list []string
	for _, c := range strings.Split(s.CountryCode, ",") {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c != "" {
			list = append(list, c)
		}
	}
	return list
}

// Floorplan represents a floor map with seats.
type Floorplan struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ImagePath  string `json:"image_path"`
	SortOrder  int    `json:"sort_order"`
	SiteID     int64  `json:"site_id"`
	SiteName   string `json:"site_name,omitempty"`
	IsFavorite bool   `json:"is_favorite,omitempty"`
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

// NewsMessage represents an active news/announcement banner.
type NewsMessage struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	StartDate string `json:"start_date"` // YYYY-MM-DD
	EndDate   string `json:"end_date"`   // YYYY-MM-DD
	BgColor   string `json:"bg_color"`   // hex color, e.g. "#dc2626"
	BgOpacity int    `json:"bg_opacity"`  // 0-100 percentage, default 100
	TextColor string `json:"text_color"` // hex color, e.g. "#ffffff", default "#ffffff"
	Recurring bool   `json:"recurring"`  // repeat every month using start/end day-of-month
}

// Notification represents an in-app user notification.
type Notification struct {
	ID             int64      `json:"id"`
	UserID         int64      `json:"user_id"`
	RecipientName  string     `json:"recipient_name,omitempty"`
	ActorID        int64      `json:"actor_id"`
	ActorName      string     `json:"actor_name,omitempty"`
	Type           string     `json:"type"` // e.g. "team_added", "info"
	Title          string     `json:"title"`
	Message        string     `json:"message"`
	Link           string     `json:"link"`
	Acknowledged   bool       `json:"acknowledged"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// extractFirstPositiveInt extracts the first integer found in the string, or returns defaultVal.
func extractFirstPositiveInt(s string, defaultVal int) int {
	var num int
	inNum := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			num = num*10 + int(r-'0')
			inNum = true
		} else if inNum {
			break
		}
	}
	if !inNum || num <= 0 {
		return defaultVal
	}
	return num
}

// Localize translates the notification title and message if localized templates are available.
func (n *Notification) Localize(tr map[string]string) {
	if tr == nil {
		return
	}
	switch n.Type {
	case "reminder_presence":
		if t, ok := tr["notifications.reminder_presence_title"]; ok && t != "" {
			n.Title = t
		}
		if tpl, ok := tr["notifications.reminder_presence_msg"]; ok && tpl != "" {
			days := extractFirstPositiveInt(n.Message, 1)
			n.Message = fmt.Sprintf(tpl, days)
		}
	case "reminder_activity":
		if t, ok := tr["notifications.reminder_activity_title"]; ok && t != "" {
			n.Title = t
		}
		if tpl, ok := tr["notifications.reminder_activity_msg"]; ok && tpl != "" {
			days := extractFirstPositiveInt(n.Message, 1)
			n.Message = fmt.Sprintf(tpl, days)
		}
	case "team_added":
		if t, ok := tr["notifications.team_added_title"]; ok && t != "" {
			n.Title = t
		}
		if tpl, ok := tr["notifications.team_added_msg"]; ok && tpl != "" && n.Link != "" {
			actor := n.ActorName
			if actor == "" {
				actor = "Admin"
			}
			n.Message = fmt.Sprintf(tpl, n.Link, actor)
		}
	}
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
	// Features
	DisableProjects bool
	// UserTasksMode is true when the current user belongs to a team with
	// "Timesheets managed manually" enabled, i.e. their /projects page shows
	// the daily-tasks form instead of the monthly project-percentage form.
	UserTasksMode bool
	// IsDomainManager is true when the current user manages at least one
	// domain, granting them scoped access to the Activity Report and
	// Projects Report nav links even without any other role.
	IsDomainManager bool
	// IsTeamLeader is true when the current user is a designated leader of at
	// least one team, granting them scoped access to the Activity Report,
	// Projects Report, and Teams admin nav links even without any other role.
	IsTeamLeader bool
	// TeamCalendarRefreshMinutes is how often (in minutes) the team calendar(s)
	// on the home page auto-refresh. 0 disables auto-refresh.
	TeamCalendarRefreshMinutes int
	// Passkeys
	PasskeysEnabled bool
	// News banners active today
	ActiveNewsMessages []NewsMessage
	// In-app notifications for the current user
	Notifications []Notification
}

// Project represents a billable project that users can log time against.
type Project struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Code           string    `json:"code"`
	TeamID         int64     `json:"team_id"`
	TeamName       string    `json:"team_name"` // populated by JOIN
	Active         bool      `json:"active"`
	MiniProject    bool      `json:"mini_project"`
	StartDate      string    `json:"start_date"`       // YYYY-MM-DD
	EndDate        string    `json:"end_date"`         // YYYY-MM-DD
	InitialEndDate string    `json:"initial_end_date"` // YYYY-MM-DD, set at creation and freely editable afterwards
	CreatedAt      time.Time `json:"created_at"`
}

// ProjectTimeEntry holds a user's declared days for one project in one month.
type ProjectTimeEntry struct {
	ID        int64   `json:"id"`
	ProjectID int64   `json:"project_id"`
	UserID    int64   `json:"user_id"`
	Year      int     `json:"year"`
	Month     int     `json:"month"`
	Days      float64 `json:"days"`
}

// Activity type values for ProjectActivity, used by teams with manually-managed timesheets.
const (
	ActivityTypeJira       = "jira"
	ActivityTypeServiceNow = "servicenow"
	ActivityTypeOther      = "other"
)

// ProjectActivity records one activity entry declared by a user on a given day,
// for teams with "Timesheets managed manually" enabled. Several activities can
// exist for the same user/date; their percentages should sum to the day's
// billable weight (100% for a full billable day, 50% for a half billable day).
type ProjectActivity struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	UserName     string    `json:"user_name"` // populated by JOIN for team reports
	Date         string    `json:"date"`      // YYYY-MM-DD
	ActivityType string    `json:"activity_type"`
	JiraKey      string    `json:"jira_key"`
	JiraTitle    string    `json:"jira_title"`
	Comment      string    `json:"comment"`
	Percentage   float64   `json:"percentage"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// JiraTicket is a lightweight reference to a Jira issue, used to populate the
// ticket picker when declaring a "jira" type activity.
type JiraTicket struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// ProjectUserMonth aggregates time per user for the report view.
type ProjectUserMonth struct {
	User            User
	MonthlyDays     map[string]float64 // "YYYY-MM" -> days
	TotalPastDays   float64            // sum of months strictly before current month
	TotalToDateDays float64            // sum of displayed months (past + current)
	TotalDays       float64            // sum of all months including future
}

// ProjectReportRow combines a project with its user-level breakdown.
type ProjectReportRow struct {
	Project  Project
	UserRows []ProjectUserMonth
	// Column month totals: month key -> total days across all users
	MonthTotals     map[string]float64
	TotalPastDays   float64 // sum of months strictly before current month
	TotalToDateDays float64 // sum of displayed months (past + current)
	TotalDays       float64 // sum of all months including future
}

// SiteReportSummary represents monthly aggregated stats for a site.
type SiteReportSummary struct {
	Site                  *Site
	Seats                 int
	ReservableSeats       int
	AttachedPeople        int
	PeopleVsSeats         float64
	WorkingDays           int
	TotalOnSiteDays       float64
	AvgOnSitePerDay       float64
	OccupancyRate         float64 // (TotalOnSiteDays / (Seats * WorkingDays)) * 100
	TotalReservations     float64
	AvgReservationsPerDay float64
	ReservationRate       float64 // (TotalReservations / (ReservableSeats * WorkingDays)) * 100
}

// SiteDailyReport represents daily breakdown metrics for a site.
type SiteDailyReport struct {
	Site              *Site
	DailyOnSite       map[string]float64 // date -> count of on-site presences
	DailyReservations map[string]float64 // date -> count of seat reservations
	DailyOccupancy    map[string]float64 // date -> occupancy percentage
	DailyResRate      map[string]float64 // date -> reservation percentage
	TotalOnSite       float64
	TotalReservations float64
}

