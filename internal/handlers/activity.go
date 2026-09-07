package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/matoy/mypresence/internal/db"
	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"
)

// ActivityHandler handles the Activity Report page.
type ActivityHandler struct {
	DB              *db.DB
	Render          func(w http.ResponseWriter, r *http.Request, page string, data interface{})
	DisableProjects bool
}

// ActivityPage renders the activity report page.
func (h *ActivityHandler) ActivityPage(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	allTeams, _ := h.DB.ListTeams()
	statuses, _ := h.DB.ListStatuses()

	teams, myTeamIDs := filterTeamsForUser(h.DB, currentUser, allTeams)
	myDomains, teamsByDomain := domainsAccessForUser(h.DB, currentUser, allTeams)
	myDomainIDs := map[int64]bool{}
	for _, dm := range myDomains {
		myDomainIDs[dm.ID] = true
	}
	if len(myDomains) > 0 && (currentUser == nil || !currentUser.HasAnyRole(models.RoleActivityViewer, models.RoleGlobal)) {
		domainTeamIDs := map[int64]bool{}
		for _, ts := range teamsByDomain {
			for _, t := range ts {
				domainTeamIDs[t.ID] = true
			}
		}
		if myTeamIDs == nil {
			// Pure domain manager (no team_leader role): restrict to the
			// teams within their managed domains.
			var filtered []models.Team
			for _, t := range teams {
				if domainTeamIDs[t.ID] {
					filtered = append(filtered, t)
				}
			}
			teams = filtered
		} else {
			// Also a team_leader: merge their own led teams with the teams of
			// the domain(s) they manage, so selecting a domain team they
			// don't personally lead isn't rejected and silently swapped for
			// one of their own teams.
			existingIDs := map[int64]bool{}
			for _, t := range teams {
				existingIDs[t.ID] = true
				myTeamIDs[t.ID] = true
			}
			for _, ts := range teamsByDomain {
				for _, t := range ts {
					myTeamIDs[t.ID] = true
					if !existingIDs[t.ID] {
						teams = append(teams, t)
						existingIDs[t.ID] = true
					}
				}
			}
			sort.Slice(teams, func(i, j int) bool { return teams[i].Name < teams[j].Name })
		}
	}

	// Activity viewers who don't manage a domain default to their own first
	// team rather than an arbitrary one, which may show no data at all.
	var preferredTeamID int64
	if currentUser != nil && len(myDomains) == 0 && currentUser.HasRole(models.RoleActivityViewer) {
		if myOwnTeams, err := h.DB.GetUserTeams(currentUser.ID); err == nil && len(myOwnTeams) > 0 {
			preferredTeamID = myOwnTeams[0].ID
		}
	}

	year, month, viewMode, teamID, domainID := normalizeActivityParams(r, time.Now(), teams, myTeamIDs, myDomains, preferredTeamID)

	q := r.URL.Query()
	dateFromParam := q.Get("date_from")
	dateToParam := q.Get("date_to")

	var isRange bool
	var monthKeys []string
	var filterDateFrom, filterDateTo string

	if dateFromParam != "" || dateToParam != "" {
		df := dateFromParam
		dt := dateToParam
		if df == "" {
			df = dt
		}
		if dt == "" {
			dt = df
		}
		if len(df) >= 7 && len(dt) >= 7 {
			df = df[:7]
			dt = dt[:7]
			if df > dt {
				df, dt = dt, df
			}
			monthKeys = buildMonthKeysFromRange(df, dt)
			if len(monthKeys) > 0 {
				isRange = true
				filterDateFrom = df
				filterDateTo = dt
			}
		}
	}

	var startDate, endDate string
	var periodStartMonth, periodStartYear, periodEndMonth, periodEndYear int
	var prevDateFrom, prevDateTo, nextDateFrom, nextDateTo string
	var prevTime, nextTime time.Time

	if isRange {
		fmt.Sscanf(monthKeys[0], "%04d-%02d", &periodStartYear, &periodStartMonth)
		fmt.Sscanf(monthKeys[len(monthKeys)-1], "%04d-%02d", &periodEndYear, &periodEndMonth)
		startDate = fmt.Sprintf("%04d-%02d-01", periodStartYear, periodStartMonth)
		lastDay := time.Date(periodEndYear, time.Month(periodEndMonth)+1, 0, 0, 0, 0, 0, time.UTC)
		endDate = lastDay.Format("2006-01-02")
		year = periodStartYear
		month = periodStartMonth

		span := len(monthKeys)
		rangeStart := time.Date(periodStartYear, time.Month(periodStartMonth), 1, 0, 0, 0, 0, time.UTC)
		rangeEnd := time.Date(periodEndYear, time.Month(periodEndMonth), 1, 0, 0, 0, 0, time.UTC)
		prevDateFrom = rangeStart.AddDate(0, -span, 0).Format("2006-01")
		prevDateTo = rangeEnd.AddDate(0, -span, 0).Format("2006-01")
		nextDateFrom = rangeStart.AddDate(0, span, 0).Format("2006-01")
		nextDateTo = rangeEnd.AddDate(0, span, 0).Format("2006-01")
		prevTime = rangeStart.AddDate(0, -span, 0)
		nextTime = rangeStart.AddDate(0, span, 0)
	} else {
		monthKeys = []string{fmt.Sprintf("%04d-%02d", year, month)}
		filterDateFrom = monthKeys[0]
		filterDateTo = monthKeys[0]
		periodStartYear, periodStartMonth = year, month
		periodEndYear, periodEndMonth = year, month
		startDate = fmt.Sprintf("%04d-%02d-01", year, month)
		lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
		endDate = lastDay.Format("2006-01-02")
		prevTime = time.Date(year, time.Month(month)-1, 1, 0, 0, 0, 0, time.UTC)
		nextTime = time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC)
	}

	var stats []models.UserStats
	var domainTeams []models.Team
	if domainID > 0 {
		domainTeams = teamsByDomain[domainID]
		stats = h.computeDomainStats(domainTeams, startDate, endDate)
	} else if teamID > 0 {
		stats, _ = h.DB.GetTeamStats(teamID, startDate, endDate)
	}
	showDailyBreakdown := domainID == 0

	totalBillable, totalSetDays, statusTotals := computeStatusTotals(stats)

	// Build daily breakdown data
	holidayMap := make(map[string]models.Holiday)
	if teamID > 0 {
		thm, _ := h.DB.GetTeamHolidayMap(teamID, startDate, endDate)
		for k, hol := range thm {
			holidayMap[k] = hol
		}
	} else if domainID > 0 {
		for _, t := range domainTeams {
			thm, _ := h.DB.GetTeamHolidayMap(t.ID, startDate, endDate)
			for k, hol := range thm {
				holidayMap[k] = hol
			}
		}
	} else {
		for _, t := range allTeams {
			thm, _ := h.DB.GetTeamHolidayMap(t.ID, startDate, endDate)
			for k, hol := range thm {
				holidayMap[k] = hol
			}
		}
		if len(allTeams) == 0 {
			ghm, _ := h.DB.GetHolidayMap(startDate, endDate)
			for k, hol := range ghm {
				if len(hol.CountryList()) == 0 {
					holidayMap[k] = hol
				}
			}
		}
	}
	var holidays []models.Holiday
	for _, hol := range holidayMap {
		holidays = append(holidays, hol)
	}
	var days []models.DayInfo
	if isRange {
		days = getDaysInRange(startDate, endDate)
	} else {
		days = getDaysInMonth(year, month)
	}
	markHolidaysOnDays(days, holidays)
	var members []models.User
	var presenceMap map[int64]map[string]map[string]int64
	if showDailyBreakdown {
		members, presenceMap = h.buildActivityMemberData(stats, teamID, startDate, endDate)
	} else {
		presenceMap = map[int64]map[string]map[string]int64{}
	}

	// Count working days in the period (Mon–Fri) and holidays on those days.
	var workingDays, holidayCount int
	if teamID > 0 {
		thm, _ := h.DB.GetTeamHolidayMap(teamID, startDate, endDate)
		workingDays, holidayCount = computeWorkingDaysFromRange(startDate, endDate, thm)
	} else {
		holMap := make(map[string]models.Holiday)
		for _, hol := range holidays {
			if _, exists := holMap[hol.Date]; !exists || !hol.AllowImputed {
				holMap[hol.Date] = hol
			}
		}
		workingDays, holidayCount = computeWorkingDaysFromRange(startDate, endDate, holMap)
	}
	workingDaysExcluded := workingDays - holidayCount
	totalOnSite := 0.0
	for _, s := range stats {
		totalOnSite += s.OnSiteDays
	}

	projectActivityByUser := make(map[int64]float64)
	totalProjectDeclared := 0.0
	showProjectActivity := !h.DisableProjects && domainID == 0
	if showProjectActivity {
		if teamHasManualTimesheets(allTeams, teamID) {
			projectActivityByUser, totalProjectDeclared = h.computeManualProjectActivityForMonths(stats, monthKeys)
		} else {
			projectActivityByUser, totalProjectDeclared = h.computeProjectActivityForMonths(stats, monthKeys)
		}
	}

	// Compute total expected working days summed accurately across each user's country holidays
	totalWorkingDays := 0.0
	for _, s := range stats {
		uHolMap, _ := h.DB.GetUserHolidayMap(s.User.ID, startDate, endDate)
		uWorkingDays, uHolCount := computeWorkingDaysFromRange(startDate, endDate, uHolMap)
		totalWorkingDays += float64(uWorkingDays - uHolCount)
	}
	totalNotSet := totalWorkingDays - totalSetDays
	if totalNotSet < 0 {
		totalNotSet = 0
	}

	// Per-day billable / on-site counts for daily breakdown footer
	dayBillable, dayOnSite := computeDayBillableOnSite(presenceMap, statuses)

	// YTD billable days per user (Jan 1 of endYear → end of current period)
	ytdBillableByUser := make(map[int64]float64)
	totalYTDBillable := 0.0
	ytdStart := fmt.Sprintf("%04d-01-01", periodEndYear)
	if domainID > 0 {
		ytdStats := h.computeDomainStats(domainTeams, ytdStart, endDate)
		for _, s := range ytdStats {
			ytdBillableByUser[s.User.ID] = s.BillableDays
			totalYTDBillable += s.BillableDays
		}
	} else if teamID > 0 {
		ytdStats, _ := h.DB.GetTeamStats(teamID, ytdStart, endDate)
		for _, s := range ytdStats {
			ytdBillableByUser[s.User.ID] = s.BillableDays
			totalYTDBillable += s.BillableDays
		}
	}

	// Executive summary — only visible to activity_viewer (and global admins)
	showExecSummary := currentUser != nil && currentUser.HasRole(models.RoleActivityViewer)
	execStatusTotals := make(map[int64]float64)
	var execTotalBillable, execTotalOnSite, execTotalNotSet, execTotalWorkingDays, execProjectActivityPct float64
	var execUserCount int
	if showExecSummary && len(allTeams) > 0 {
		execStatusTotals, execTotalBillable, execTotalOnSite, execTotalNotSet, execTotalWorkingDays, execProjectActivityPct, execUserCount =
			h.computeExecSummary(allTeams, startDate, endDate, monthKeys)
	}

	// Certification status per user for the displayed period:
	// For multi-month range, a user must be certified in all months.
	statUserIDs := make([]int64, len(stats))
	for i, s := range stats {
		statUserIDs[i] = s.User.ID
	}
	certifiedUsers := make(map[int64]bool)
	projectCertifiedUsers := make(map[int64]bool)
	if len(statUserIDs) > 0 {
		for _, uid := range statUserIDs {
			certifiedUsers[uid] = true
			projectCertifiedUsers[uid] = true
		}
		for _, mk := range monthKeys {
			var y, m int
			fmt.Sscanf(mk, "%04d-%02d", &y, &m)
			cMap, _ := h.DB.GetCertifiedUserIDs(statUserIDs, y, m)
			pcMap, _ := h.DB.GetCertifiedProjectUserIDs(statUserIDs, y, m)
			for _, uid := range statUserIDs {
				if !cMap[uid] {
					certifiedUsers[uid] = false
				}
				if !pcMap[uid] {
					projectCertifiedUsers[uid] = false
				}
			}
		}
	}

	// Domain groups for the team-selector dropdown: only built for users who
	// manage at least one domain, so the dropdown can list domains with their
	// teams indented underneath.
	var domainGroups []domainGroupView
	for _, dm := range myDomains {
		domainGroups = append(domainGroups, domainGroupView{Domain: dm, Teams: teamsByDomain[dm.ID]})
	}

	h.Render(w, r, "admin_activity", map[string]interface{}{
		"Teams":                  teams,
		"DomainGroups":           domainGroups,
		"IsDomainManager":        len(myDomains) > 0,
		"Statuses":               statuses,
		"Stats":                  stats,
		"ShowProjectActivity":    showProjectActivity,
		"ProjectActivityByUser":  projectActivityByUser,
		"TotalProjectDeclared":   totalProjectDeclared,
		"SelectedTeamID":         teamID,
		"SelectedDomainID":       domainID,
		"ShowDailyBreakdown":     showDailyBreakdown,
		"Year":                   year,
		"Month":                  month,
		"IsRange":                isRange,
		"FilterDateFrom":         filterDateFrom,
		"FilterDateTo":           filterDateTo,
		"PrevDateFrom":           prevDateFrom,
		"PrevDateTo":             prevDateTo,
		"NextDateFrom":           nextDateFrom,
		"NextDateTo":             nextDateTo,
		"PeriodStartMonth":       periodStartMonth,
		"PeriodStartYear":        periodStartYear,
		"PeriodEndMonth":         periodEndMonth,
		"PeriodEndYear":          periodEndYear,
		"ViewMode":               viewMode,
		"TotalBillable":          totalBillable,
		"TotalNotSet":            totalNotSet,
		"TotalOnSite":            totalOnSite,
		"TotalWorkingDays":       totalWorkingDays,
		"WorkingDays":            workingDays,
		"WorkingDaysExcl":        workingDaysExcluded,
		"HolidayCount":           holidayCount,
		"DayBillable":            dayBillable,
		"DayOnSite":              dayOnSite,
		"StatusTotals":           statusTotals,
		"PrevYear":               prevTime.Year(),
		"PrevMonth":              int(prevTime.Month()),
		"NextYear":               nextTime.Year(),
		"NextMonth":              int(nextTime.Month()),
		"Days":                   days,
		"Users":                  members,
		"PresenceMap":            presenceMap,
		"ShowExecSummary":        showExecSummary,
		"ExecStatusTotals":       execStatusTotals,
		"ExecTotalBillable":      execTotalBillable,
		"ExecTotalOnSite":        execTotalOnSite,
		"ExecTotalNotSet":        execTotalNotSet,
		"ExecTotalWorkingDays":   execTotalWorkingDays,
		"ExecProjectActivityPct": execProjectActivityPct,
		"ExecUserCount":          execUserCount,
		"YTDBillableByUser":      ytdBillableByUser,
		"TotalYTDBillable":       totalYTDBillable,
		"Certified":              certifiedUsers,
		"ProjectCertified":       projectCertifiedUsers,
		"CanDecertify":           !isRange && currentUser != nil && (currentUser.HasAnyRole(models.RoleGlobal, models.RoleActivityViewer) || len(myTeamIDs) > 0),
	})
}

// domainGroupView pairs a domain with the teams it contains, for the
// domain-grouped team selector dropdown.
type domainGroupView struct {
	Domain models.Domain
	Teams  []models.Team
}

// domainsAccessForUser returns the domains the given user manages, along with
// the (allTeams-filtered) teams attached to each. Returns nil, nil if the user
// manages no domain.
func domainsAccessForUser(database *db.DB, user *models.User, allTeams []models.Team) ([]models.Domain, map[int64][]models.Team) {
	if user == nil {
		return nil, nil
	}
	myDomains, _ := database.GetUserDomains(user.ID)
	if len(myDomains) == 0 {
		return nil, nil
	}
	teamsByDomain := map[int64][]models.Team{}
	for _, dm := range myDomains {
		var ts []models.Team
		for _, t := range allTeams {
			if t.DomainID == dm.ID {
				ts = append(ts, t)
			}
		}
		teamsByDomain[dm.ID] = ts
	}
	return myDomains, teamsByDomain
}

// computeDomainStats aggregates per-user stats across all teams of a domain,
// deduplicating users who might appear in more than one team.
func (h *ActivityHandler) computeDomainStats(domainTeams []models.Team, startDate, endDate string) []models.UserStats {
	seen := map[int64]bool{}
	var out []models.UserStats
	for _, t := range domainTeams {
		stats, err := h.DB.GetTeamStats(t.ID, startDate, endDate)
		if err != nil {
			continue
		}
		for _, s := range stats {
			if seen[s.User.ID] {
				continue
			}
			seen[s.User.ID] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User.Name < out[j].User.Name })
	return out
}

// ActivityAPI returns activity report data as JSON.
func (h *ActivityHandler) ActivityAPI(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	teamID, _ := strconv.ParseInt(r.URL.Query().Get("team_id"), 10, 64)
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	month, _ := strconv.Atoi(r.URL.Query().Get("month"))
	dateFromParam := r.URL.Query().Get("date_from")
	dateToParam := r.URL.Query().Get("date_to")

	var startDate, endDate string
	if dateFromParam != "" || dateToParam != "" {
		df := dateFromParam
		dt := dateToParam
		if df == "" {
			df = dt
		}
		if dt == "" {
			dt = df
		}
		if len(df) >= 7 && len(dt) >= 7 {
			df = df[:7]
			dt = dt[:7]
			if df > dt {
				df, dt = dt, df
			}
			mKeys := buildMonthKeysFromRange(df, dt)
			if len(mKeys) > 0 {
				var sy, sm, ey, em int
				fmt.Sscanf(mKeys[0], "%04d-%02d", &sy, &sm)
				fmt.Sscanf(mKeys[len(mKeys)-1], "%04d-%02d", &ey, &em)
				startDate = fmt.Sprintf("%04d-%02d-01", sy, sm)
				lastDay := time.Date(ey, time.Month(em)+1, 0, 0, 0, 0, 0, time.UTC)
				endDate = lastDay.Format("2006-01-02")
			}
		}
	}

	if startDate == "" {
		if teamID == 0 || year == 0 || month == 0 {
			jsonError(w, "Paramètres manquants", http.StatusBadRequest)
			return
		}
		startDate = fmt.Sprintf("%04d-%02d-01", year, month)
		lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
		endDate = lastDay.Format("2006-01-02")
	} else if teamID == 0 {
		jsonError(w, "Paramètres manquants", http.StatusBadRequest)
		return
	}

	// Team leaders and Domain managers (without global/activity_viewer role) can only
	// request stats for their led teams or teams attached to a managed domain.
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleActivityViewer, models.RoleGlobal) {
		isLeader, _ := h.DB.IsLeaderOfTeam(currentUser.ID, teamID)
		if !isLeader {
			myDomains, _ := h.DB.GetUserDomains(currentUser.ID)
			myDomainIDs := map[int64]bool{}
			for _, dm := range myDomains {
				myDomainIDs[dm.ID] = true
			}
			allowed := false
			if len(myDomainIDs) > 0 {
				allTeams, _ := h.DB.ListTeams()
				for _, t := range allTeams {
					if t.ID == teamID && myDomainIDs[t.DomainID] {
						allowed = true
						break
					}
				}
			}
			if !allowed {
				jsonError(w, "Access denied", http.StatusForbidden)
				return
			}
		}
	}

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
	ids, _ := database.GetLedTeamIDs(user.ID)
	if len(ids) == 0 {
		return nil, nil
	}
	myTeamIDs := map[int64]bool{}
	for _, id := range ids {
		myTeamIDs[id] = true
	}
	var filtered []models.Team
	for _, t := range allTeams {
		if myTeamIDs[t.ID] {
			filtered = append(filtered, t)
		}
	}
	return filtered, myTeamIDs
}

// computeWorkingDays counts the working days (Mon–Fri) in the given month and
// the number of those working days that are non-imputable public holidays.
func computeWorkingDays(year, month int, holidays []models.Holiday) (workingDays, holidayCount int) {
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	for d := 1; d <= lastDay.Day(); d++ {
		t := time.Date(year, time.Month(month), d, 0, 0, 0, 0, time.UTC)
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			workingDays++
		}
	}
	for _, hol := range holidays {
		t, err := time.Parse("2006-01-02", hol.Date)
		if err != nil {
			continue
		}
		if int(t.Month()) != month || t.Year() != year ||
			t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
			continue
		}
		if !hol.AllowImputed {
			holidayCount++
		}
	}
	return
}

// computeWorkingDaysFromMap counts working days and non-imputable holidays using a map.
func computeWorkingDaysFromMap(year, month int, holidayMap map[string]models.Holiday) (workingDays, holidayCount int) {
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	for d := 1; d <= lastDay.Day(); d++ {
		t := time.Date(year, time.Month(month), d, 0, 0, 0, 0, time.UTC)
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			workingDays++
			dateStr := t.Format("2006-01-02")
			if hol, ok := holidayMap[dateStr]; ok && !hol.AllowImputed {
				holidayCount++
			}
		}
	}
	return
}

// computeWorkingDaysFromRange counts working days (Mon–Fri) and non-imputable holidays
// within [startDate, endDate] (inclusive).
func computeWorkingDaysFromRange(startDate, endDate string, holidayMap map[string]models.Holiday) (workingDays, holidayCount int) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return 0, 0
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return 0, 0
	}
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			workingDays++
			dateStr := t.Format("2006-01-02")
			if hol, ok := holidayMap[dateStr]; ok && !hol.AllowImputed {
				holidayCount++
			}
		}
	}
	return
}

// getDaysInRange returns a slice of DayInfo structs for each day between startDate
// and endDate (inclusive, format "2006-01-02").
func getDaysInRange(startDate, endDate string) []models.DayInfo {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil
	}
	var days []models.DayInfo
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		days = append(days, models.DayInfo{
			Day:       t.Day(),
			Date:      t.Format("2006-01-02"),
			DayIndex:  int(t.Weekday()),
			IsWeekend: t.Weekday() == time.Saturday || t.Weekday() == time.Sunday,
		})
	}
	return days
}

// computeDayBillableOnSite aggregates per-date billable and on-site half-day
// weights from the presence map for the activity daily breakdown footer.
func computeDayBillableOnSite(presenceMap map[int64]map[string]map[string]int64, statuses []models.Status) (dayBillable, dayOnSite map[string]float64) {
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
	dayBillable = make(map[string]float64)
	dayOnSite = make(map[string]float64)
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
	return
}

// computeExecSummary aggregates stats across all teams (deduplicating users) to
// produce a single executive summary row for activity_viewer users.
func (h *ActivityHandler) computeExecSummary(
	allTeams []models.Team,
	startDate, endDate string,
	monthKeys []string,
) (statusTotals map[int64]float64, totalBillable, totalOnSite, totalNotSet, totalWorkingDays, projectActivityPct float64, userCount int) {
	statusTotals = make(map[int64]float64)
	seen := make(map[int64]bool)
	totalSetDays := 0.0
	totalProjectDeclared := 0.0
	for _, team := range allTeams {
		stats, err := h.DB.GetTeamStats(team.ID, startDate, endDate)
		if err != nil {
			continue
		}
		for _, s := range stats {
			if seen[s.User.ID] {
				continue
			}
			seen[s.User.ID] = true
			userCount++
			totalBillable += s.BillableDays
			totalOnSite += s.OnSiteDays
			for sid, count := range s.StatusCounts {
				statusTotals[sid] += count
				totalSetDays += count
			}
			if !h.DisableProjects {
				for _, mk := range monthKeys {
					var y, m int
					fmt.Sscanf(mk, "%04d-%02d", &y, &m)
					declared, err := h.DB.GetUserTotalDeclaredForMonth(s.User.ID, y, m)
					if err == nil {
						totalProjectDeclared += declared
					}
				}
			}
		}
	}
	totalWorkingDays = 0.0
	for uid := range seen {
		uHolMap, _ := h.DB.GetUserHolidayMap(uid, startDate, endDate)
		uWorkingDays, uHolCount := computeWorkingDaysFromRange(startDate, endDate, uHolMap)
		totalWorkingDays += float64(uWorkingDays - uHolCount)
	}
	totalNotSet = totalWorkingDays - totalSetDays
	if totalNotSet < 0 {
		totalNotSet = 0
	}
	if totalBillable > 0 {
		projectActivityPct = (totalProjectDeclared / totalBillable) * 100.0
	}
	return
}

// computeProjectActivity returns the per-user project activity percentage and
// total declared days for the given month across all projects.
func (h *ActivityHandler) computeProjectActivity(stats []models.UserStats, year, month int) (projectActivityByUser map[int64]float64, totalProjectDeclared float64) {
	return h.computeProjectActivityForMonths(stats, []string{fmt.Sprintf("%04d-%02d", year, month)})
}

// computeProjectActivityForMonths returns the per-user project activity percentage and
// total declared days summed across all specified months.
func (h *ActivityHandler) computeProjectActivityForMonths(stats []models.UserStats, monthKeys []string) (projectActivityByUser map[int64]float64, totalProjectDeclared float64) {
	projectActivityByUser = make(map[int64]float64)
	for _, s := range stats {
		var userDeclared float64
		hasData := false
		for _, mk := range monthKeys {
			var y, m int
			fmt.Sscanf(mk, "%04d-%02d", &y, &m)
			declared, err := h.DB.GetUserTotalDeclaredForMonth(s.User.ID, y, m)
			if err == nil {
				userDeclared += declared
				hasData = true
			}
		}
		if !hasData {
			continue
		}
		totalProjectDeclared += userDeclared
		if s.BillableDays > 0 {
			projectActivityByUser[s.User.ID] = (userDeclared / s.BillableDays) * 100.0
		}
	}
	return
}

// teamHasManualTimesheets reports whether the given team ID has "Timesheets
// managed manually" enabled.
func teamHasManualTimesheets(teams []models.Team, teamID int64) bool {
	for _, t := range teams {
		if t.ID == teamID {
			return t.TimesheetsManagedManually
		}
	}
	return false
}

// computeManualProjectActivity returns the per-user project activity percentage
// for a "Timesheets managed manually" team: the percentage of each user's
// billable days whose activities are fully declared (100%, or 50% for half
// days), instead of the sum of declared project-time-entry days.
func (h *ActivityHandler) computeManualProjectActivity(stats []models.UserStats, year, month int) (projectActivityByUser map[int64]float64, totalProjectDeclared float64) {
	return h.computeManualProjectActivityForMonths(stats, []string{fmt.Sprintf("%04d-%02d", year, month)})
}

// computeManualProjectActivityForMonths returns the per-user manual project activity
// percentage summed across all specified months.
func (h *ActivityHandler) computeManualProjectActivityForMonths(stats []models.UserStats, monthKeys []string) (projectActivityByUser map[int64]float64, totalProjectDeclared float64) {
	projectActivityByUser = make(map[int64]float64)
	for _, s := range stats {
		var userDeclared float64
		hasData := false
		for _, mk := range monthKeys {
			var y, m int
			fmt.Sscanf(mk, "%04d-%02d", &y, &m)
			weights, err := h.DB.GetUserBillableDatesForMonth(s.User.ID, y, m)
			if err != nil {
				continue
			}
			activities, err := h.DB.ListUserActivitiesForMonth(s.User.ID, y, m)
			if err != nil {
				continue
			}
			hasData = true
			sumByDate := make(map[string]float64)
			for _, a := range activities {
				sumByDate[a.Date] += a.Percentage
			}
			for date, weight := range weights {
				if isDateComplete(sumByDate[date], weight) {
					userDeclared += weight
				}
			}
		}
		if !hasData {
			continue
		}
		totalProjectDeclared += userDeclared
		if s.BillableDays > 0 {
			projectActivityByUser[s.User.ID] = (userDeclared / s.BillableDays) * 100.0
		}
	}
	return
}

// normalizeActivityParams parses and normalizes the year, month, viewMode, teamID
// and domainID query parameters, applying defaults and enforcing team-leader /
// domain-manager access restrictions. preferredTeamID, when set, is used as the
// default team instead of the alphabetically first one (e.g. the user's own team).
func normalizeActivityParams(r *http.Request, now time.Time, teams []models.Team, myTeamIDs map[int64]bool, myDomains []models.Domain, preferredTeamID int64) (year, month int, viewMode string, teamID, domainID int64) {
	myDomainIDs := map[int64]bool{}
	for _, dm := range myDomains {
		myDomainIDs[dm.ID] = true
	}
	year, _ = strconv.Atoi(r.URL.Query().Get("year"))
	month, _ = strconv.Atoi(r.URL.Query().Get("month"))
	teamID, _ = strconv.ParseInt(r.URL.Query().Get("team"), 10, 64)
	domainID, _ = strconv.ParseInt(r.URL.Query().Get("domain"), 10, 64)
	viewMode = r.URL.Query().Get("view")
	if year == 0 {
		year = now.Year()
	}
	if month == 0 {
		month = int(now.Month())
	}
	if viewMode == "" {
		viewMode = "month"
	}
	if domainID > 0 && (len(myDomainIDs) == 0 || !myDomainIDs[domainID]) {
		domainID = 0
	}
	// When no team/domain was explicitly requested, a domain manager
	// defaults to the aggregated view of their first managed domain rather
	// than an arbitrary team, which may show no data at all.
	if domainID == 0 && teamID == 0 && len(myDomains) > 0 {
		domainID = myDomains[0].ID
	}
	if domainID > 0 {
		// A domain selection takes precedence over any team selection.
		return year, month, viewMode, 0, domainID
	}
	if teamID == 0 && preferredTeamID > 0 {
		teamID = preferredTeamID
	}
	if teamID == 0 && len(teams) > 0 {
		teamID = teams[0].ID
	}
	// Team leaders cannot request stats for teams they don't belong to.
	if myTeamIDs != nil && teamID > 0 && !myTeamIDs[teamID] {
		if len(teams) > 0 {
			teamID = teams[0].ID
		} else {
			teamID = 0
		}
	}
	return
}

// computeStatusTotals aggregates billable days, total set days and per-status
// counts from a slice of UserStats.
func computeStatusTotals(stats []models.UserStats) (totalBillable, totalSetDays float64, statusTotals map[int64]float64) {
	statusTotals = make(map[int64]float64)
	for _, s := range stats {
		totalBillable += s.BillableDays
		for sid, count := range s.StatusCounts {
			statusTotals[sid] += count
			totalSetDays += count
		}
	}
	return
}

// markHolidaysOnDays sets the IsHoliday and HolidayName fields on days that
// match a holiday in the provided list.
func markHolidaysOnDays(days []models.DayInfo, holidays []models.Holiday) {
	for i, d := range days {
		for _, hol := range holidays {
			if hol.Date == d.Date {
				days[i].IsHoliday = true
				days[i].HolidayName = hol.Name
				days[i].HolidayAllowImputed = hol.AllowImputed
				days[i].HolidayCountryCode = hol.CountryCode
				break
			}
		}
	}
}

// buildActivityMemberData returns the ordered member list and presence map for
// the given team stats. Returns nil members and an empty map when teamID is 0.
func (h *ActivityHandler) buildActivityMemberData(stats []models.UserStats, teamID int64, startDate, endDate string) (members []models.User, presenceMap map[int64]map[string]map[string]int64) {
	presenceMap = make(map[int64]map[string]map[string]int64)
	if teamID == 0 {
		return
	}
	members = make([]models.User, len(stats))
	userIDs := make([]int64, len(stats))
	for i, s := range stats {
		members[i] = s.User
		userIDs[i] = s.User.ID
	}
	presenceMap, _ = h.DB.GetPresences(userIDs, startDate, endDate)
	return
}
