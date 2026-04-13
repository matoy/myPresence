package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"presence-app/internal/db"
	"presence-app/internal/middleware"
	"presence-app/internal/models"
)

// ActivityHandler handles the Activity Report page.
type ActivityHandler struct {
	DB     *db.DB
	Render func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// ActivityPage renders the activity report page.
func (h *ActivityHandler) ActivityPage(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	allTeams, _ := h.DB.ListTeams()
	statuses, _ := h.DB.ListStatuses()

	teams, myTeamIDs := filterTeamsForUser(h.DB, currentUser, allTeams)

	now := time.Now()
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	month, _ := strconv.Atoi(r.URL.Query().Get("month"))
	teamID, _ := strconv.ParseInt(r.URL.Query().Get("team"), 10, 64)
	viewMode := r.URL.Query().Get("view") // "month" or "week"

	if year == 0 {
		year = now.Year()
	}
	if month == 0 {
		month = int(now.Month())
	}
	if viewMode == "" {
		viewMode = "month"
	}
	if teamID == 0 && len(teams) > 0 {
		teamID = teams[0].ID
	}
	// Team leaders cannot request stats for teams they don't belong to
	if myTeamIDs != nil && teamID > 0 && !myTeamIDs[teamID] {
		if len(teams) > 0 {
			teamID = teams[0].ID
		} else {
			teamID = 0
		}
	}

	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	endDate := lastDay.Format("2006-01-02")

	var stats []models.UserStats
	if teamID > 0 {
		stats, _ = h.DB.GetTeamStats(teamID, startDate, endDate)
	}

	// Calculate totals (per-status and billable)
	totalBillable := 0.0
	totalSetDays := 0.0
	statussTotals := make(map[int64]float64)
	for _, s := range stats {
		totalBillable += s.BillableDays
		for sid, count := range s.StatusCounts {
			statussTotals[sid] += count
			totalSetDays += count
		}
	}

	// Build daily breakdown data
	allHolidays, _ := h.DB.ListHolidays()
	days := getDaysInMonth(year, month)
	// Mark holidays on days
	for i, d := range days {
		for _, hol := range allHolidays {
			if hol.Date == d.Date {
				days[i].IsHoliday = true
				days[i].HolidayName = hol.Name
				break
			}
		}
	}
	var members []models.User
	presenceMap := make(map[int64]map[string]map[string]int64)
	if teamID > 0 {
		members = make([]models.User, len(stats))
		userIDs := make([]int64, len(stats))
		for i, s := range stats {
			members[i] = s.User
			userIDs[i] = s.User.ID
		}
		presenceMap, _ = h.DB.GetPresences(userIDs, startDate, endDate)
	}

	// Count working days in the month (Mon–Fri)
	workingDays := 0
	for d := 1; d <= lastDay.Day(); d++ {
		t := time.Date(year, time.Month(month), d, 0, 0, 0, 0, time.UTC)
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			workingDays++
		}
	}

	// Count holidays falling on working days in the month
	holidayCount := 0
	for _, hol := range allHolidays {
		t, err := time.Parse("2006-01-02", hol.Date)
		if err != nil {
			continue
		}
		if int(t.Month()) == month && t.Year() == year &&
			t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			holidayCount++
		}
	}
	workingDaysExcluded := workingDays - holidayCount
	totalOnSite := 0.0
	for _, s := range stats {
		totalOnSite += s.OnSiteDays
	}
	totalWorkingDays := float64(workingDaysExcluded) * float64(len(stats))
	totalNotSet := totalWorkingDays - totalSetDays
	if totalNotSet < 0 {
		totalNotSet = 0
	}

	// Per-day billable / on-site counts for daily breakdown footer
	billableIDs := make(map[int64]bool)
	onSiteIDs := make(map[int64]bool)
	for _, s := range statuses {
		if s.Billable {
			billableIDs[s.ID] = true
		}
		if s.OnSite {
			onSiteIDs[s.ID] = true
		}
	}
	dayBillable := make(map[string]float64)
	dayOnSite := make(map[string]float64)
	for _, userPresences := range presenceMap {
		for date, halves := range userPresences {
			for half, statusID := range halves {
				weight := 1.0
				if half == "AM" || half == "PM" {
					weight = 0.5
				}
				if billableIDs[statusID] {
					dayBillable[date] += weight
				}
				if onSiteIDs[statusID] {
					dayOnSite[date] += weight
				}
			}
		}
	}

	prevTime := time.Date(year, time.Month(month)-1, 1, 0, 0, 0, 0, time.UTC)
	nextTime := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC)

	h.Render(w, r, "admin_activity", map[string]interface{}{
		"Teams":            teams,
		"Statuses":         statuses,
		"Stats":            stats,
		"SelectedTeamID":   teamID,
		"Year":             year,
		"Month":            month,
		"MonthName":        frenchMonths[month],
		"ViewMode":         viewMode,
		"TotalBillable":    totalBillable,
		"TotalNotSet":      totalNotSet,
		"TotalOnSite":      totalOnSite,
		"TotalWorkingDays": totalWorkingDays,
		"WorkingDays":      workingDays,
		"WorkingDaysExcl": workingDaysExcluded,
		"HolidayCount":    holidayCount,
		"DayBillable":      dayBillable,
		"DayOnSite":        dayOnSite,
		"StatusTotals":     statussTotals,
		"PrevYear":         prevTime.Year(),
		"PrevMonth":        int(prevTime.Month()),
		"NextYear":         nextTime.Year(),
		"NextMonth":        int(nextTime.Month()),
		"Days":             days,
		"Users":            members,
		"PresenceMap":      presenceMap,
	})
}

// ActivityAPI returns activity report data as JSON.
func (h *ActivityHandler) ActivityAPI(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	teamID, _ := strconv.ParseInt(r.URL.Query().Get("team_id"), 10, 64)
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	month, _ := strconv.Atoi(r.URL.Query().Get("month"))

	if teamID == 0 || year == 0 || month == 0 {
		jsonError(w, "Paramètres manquants", http.StatusBadRequest)
		return
	}

	// Team leaders can only request stats for their own teams
	if currentUser != nil && currentUser.HasRole(models.RoleTeamLeader) && !currentUser.HasAnyRole(models.RoleActivityViewer, models.RoleGlobal) {
		myTeams, _ := h.DB.GetUserTeams(currentUser.ID)
		allowed := false
		for _, t := range myTeams {
			if t.ID == teamID {
				allowed = true
				break
			}
		}
		if !allowed {
			jsonError(w, "Access denied", http.StatusForbidden)
			return
		}
	}

	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	endDate := lastDay.Format("2006-01-02")

	stats, err := h.DB.GetTeamStats(teamID, startDate, endDate)
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}

	jsonOK(w, stats)
}

// filterTeamsForUser returns the teams visible to the given user and, if the user
// is a restricted team leader, the set of their team IDs (nil otherwise).
func filterTeamsForUser(database *db.DB, user *models.User, allTeams []models.Team) ([]models.Team, map[int64]bool) {
	if user == nil || user.HasAnyRole(models.RoleActivityViewer, models.RoleGlobal) {
		return allTeams, nil
	}
	if !user.HasRole(models.RoleTeamLeader) {
		return allTeams, nil
	}
	myTeams, _ := database.GetUserTeams(user.ID)
	myTeamIDs := map[int64]bool{}
	for _, t := range myTeams {
		myTeamIDs[t.ID] = true
	}
	var filtered []models.Team
	for _, t := range allTeams {
		if myTeamIDs[t.ID] {
			filtered = append(filtered, t)
		}
	}
	return filtered, myTeamIDs
}
