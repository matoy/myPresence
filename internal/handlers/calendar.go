package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"presence-app/internal/db"
	"presence-app/internal/metrics"
	"presence-app/internal/middleware"
	"presence-app/internal/models"
)

// Month and day names are resolved at template render time via the i18n T map
// using the keys "cal.month.N" (1-12) and "cal.day.N" (0-6, Sunday=0).

// CalendarHandler handles the main calendar view.
type CalendarHandler struct {
	DB                *db.DB
	Render            func(w http.ResponseWriter, r *http.Request, page string, data interface{})
	DisableFloorplans bool
}

// CalendarPage renders the monthly calendar view for the logged-in user.
func (h *CalendarHandler) CalendarPage(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Parse year/month from query
	now := time.Now()
	yearStr := r.URL.Query().Get("year")
	monthStr := r.URL.Query().Get("month")

	year := now.Year()
	month := int(now.Month())

	if y, err := strconv.Atoi(yearStr); err == nil && y >= 2020 && y <= 2100 {
		year = y
	}
	if m, err := strconv.Atoi(monthStr); err == nil && m >= 1 && m <= 12 {
		month = m
	}

	// Calculate prev/next month
	prevTime := time.Date(year, time.Month(month)-1, 1, 0, 0, 0, 0, time.UTC)
	nextTime := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC)

	// Get days of month
	days := getDaysInMonth(year, month)
	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	endDate := lastDay.Format("2006-01-02")

	// Enrich days with holiday data
	holidayMap, _ := h.DB.GetHolidayMap(startDate, endDate)
	for i, d := range days {
		if hol, ok := holidayMap[d.Date]; ok {
			days[i].IsHoliday = true
			days[i].HolidayName = hol.Name
			days[i].HolidayAllowImputed = hol.AllowImputed
		}
	}

	// Get current user's presences only
	presenceMap, err := h.DB.GetPresences([]int64{user.ID}, startDate, endDate)
	if err != nil {
		http.Error(w, "Error loading presences", http.StatusInternalServerError)
		return
	}
	userPresences := presenceMap[user.ID]
	if userPresences == nil {
		userPresences = make(map[string]map[string]int64)
	}

	// Get seat reservations and floorplans (skipped when floor plans are disabled)
	var reservationDates map[string]bool
	var floorplans []models.Floorplan
	if !h.DisableFloorplans {
		reservationDates, _ = h.DB.GetUserReservationDates(user.ID, startDate, endDate)
		floorplans, _ = h.DB.ListFloorplans()
	}
	if reservationDates == nil {
		reservationDates = make(map[string]bool)
	}

	// Get statuses
	statuses, _ := h.DB.ListStatuses()

	h.Render(w, r, "calendar", map[string]interface{}{
		"Year":             year,
		"Month":            month,
		"PrevYear":         prevTime.Year(),
		"PrevMonth":        int(prevTime.Month()),
		"NextYear":         nextTime.Year(),
		"NextMonth":        int(nextTime.Month()),
		"Days":             days,
		"Presences":        userPresences,
		"Statuses":         statuses,
		"CurrentUserID":    user.ID,
		"ReservationDates": reservationDates,
		"Floorplans":       floorplans,
	})
}

// SetPresences handles bulk presence setting via API.
func (h *CalendarHandler) SetPresences(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	var req struct {
		UserID   int64    `json:"user_id"`
		Dates    []string `json:"dates"`
		StatusID int64    `json:"status_id"`
		Half     string   `json:"half"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}

	// Validate: only allow editing own presences (managers/global can edit anyone)
	if !user.HasRole(models.RoleGlobal) && !user.HasRole(models.RoleTeamManager) && req.UserID != user.ID {
		jsonError(w, "Non autorisé", http.StatusForbidden)
		return
	}

	// Validate date format
	for _, d := range req.Dates {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			jsonError(w, "Date invalide: "+d, http.StatusBadRequest)
			return
		}
	}

	if err := h.DB.SetPresences(req.UserID, req.Dates, req.StatusID, req.Half); err != nil {
		jsonError(w, "Erreur sauvegarde", http.StatusInternalServerError)
		return
	}

	h.DB.LogPresenceAction(user.ID, req.UserID, "set", req.Dates, req.StatusID, req.Half)

	half := req.Half
	if half == "" {
		half = "full"
	}
	metrics.PresenceOpsTotal.WithLabelValues("set", half).Inc()
	metrics.PresenceDaysTotal.WithLabelValues("set").Add(float64(len(req.Dates)))

	jsonOK(w, map[string]string{"status": "ok"})
}

// ClearPresences handles presence deletion via API.
func (h *CalendarHandler) ClearPresences(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	var req struct {
		UserID int64    `json:"user_id"`
		Dates  []string `json:"dates"`
		Half   string   `json:"half"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}

	if !user.HasRole(models.RoleGlobal) && !user.HasRole(models.RoleTeamManager) && req.UserID != user.ID {
		jsonError(w, "Non autorisé", http.StatusForbidden)
		return
	}

	if err := h.DB.ClearPresences(req.UserID, req.Dates, req.Half); err != nil {
		jsonError(w, "Erreur suppression", http.StatusInternalServerError)
		return
	}

	h.DB.LogPresenceAction(user.ID, req.UserID, "clear", req.Dates, 0, req.Half)

	clearHalf := req.Half
	if clearHalf == "" {
		clearHalf = "all"
	}
	metrics.PresenceOpsTotal.WithLabelValues("clear", clearHalf).Inc()
	metrics.PresenceDaysTotal.WithLabelValues("clear").Add(float64(len(req.Dates)))

	jsonOK(w, map[string]string{"status": "ok"})
}

// GetPresencesAPI returns presences as JSON.
func (h *CalendarHandler) GetPresencesAPI(w http.ResponseWriter, r *http.Request) {
	teamStr := r.URL.Query().Get("team_id")
	yearStr := r.URL.Query().Get("year")
	monthStr := r.URL.Query().Get("month")

	teamID, _ := strconv.ParseInt(teamStr, 10, 64)
	year, _ := strconv.Atoi(yearStr)
	month, _ := strconv.Atoi(monthStr)

	if teamID == 0 || year == 0 || month == 0 {
		jsonError(w, "Paramètres manquants", http.StatusBadRequest)
		return
	}

	members, err := h.DB.GetTeamMembers(teamID)
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}

	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	endDate := lastDay.Format("2006-01-02")

	userIDs := make([]int64, len(members))
	for i, m := range members {
		userIDs[i] = m.ID
	}

	presences, err := h.DB.GetPresences(userIDs, startDate, endDate)
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}

	jsonOK(w, presences)
}

func getDaysInMonth(year, month int) []models.DayInfo {
	firstDay := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	lastDay := firstDay.AddDate(0, 1, -1)

	var days []models.DayInfo
	for d := 1; d <= lastDay.Day(); d++ {
		t := time.Date(year, time.Month(month), d, 0, 0, 0, 0, time.UTC)
		days = append(days, models.DayInfo{
			Day:       d,
			Date:      t.Format("2006-01-02"),
			DayIndex:  int(t.Weekday()),
			IsWeekend: t.Weekday() == time.Saturday || t.Weekday() == time.Sunday,
		})
	}
	return days
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
