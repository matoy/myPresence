package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"
)

func TestComputeWorkingDaysFromRange(t *testing.T) {
	// January 2026: 31 days. Jan 1 is Thursday.
	// Weekdays: 22.
	// Suppose Jan 1 is a public holiday with AllowImputed=false.
	holidays := map[string]models.Holiday{
		"2026-01-01": {Date: "2026-01-01", Name: "New Year", AllowImputed: false},
		"2026-01-03": {Date: "2026-01-03", Name: "Saturday Holiday", AllowImputed: false}, // Weekend holiday should not be counted
		"2026-01-06": {Date: "2026-01-06", Name: "Imputed Holiday", AllowImputed: true},  // AllowImputed=true should not count as excluded
	}

	wDays, hCount := computeWorkingDaysFromRange("2026-01-01", "2026-01-31", holidays)
	if wDays != 22 {
		t.Errorf("expected 22 working days in Jan 2026, got %d", wDays)
	}
	if hCount != 1 {
		t.Errorf("expected 1 non-imputable weekday holiday, got %d", hCount)
	}

	// Range spanning Jan 1 to Feb 28, 2026 (22 working days in Jan + 20 in Feb = 42)
	wDays2, _ := computeWorkingDaysFromRange("2026-01-01", "2026-02-28", nil)
	if wDays2 != 42 {
		t.Errorf("expected 42 working days across Jan-Feb 2026, got %d", wDays2)
	}

	// Invalid date format returns 0, 0
	w0, h0 := computeWorkingDaysFromRange("invalid", "2026-02-28", nil)
	if w0 != 0 || h0 != 0 {
		t.Errorf("expected 0, 0 for invalid date, got %d, %d", w0, h0)
	}
}

func TestGetDaysInRange(t *testing.T) {
	days := getDaysInRange("2026-01-01", "2026-01-05")
	if len(days) != 5 {
		t.Fatalf("expected 5 days, got %d", len(days))
	}
	if days[0].Date != "2026-01-01" || days[4].Date != "2026-01-05" {
		t.Errorf("unexpected dates: %s to %s", days[0].Date, days[4].Date)
	}

	// Invalid range returns nil
	if getDaysInRange("invalid", "2026-01-05") != nil {
		t.Error("expected nil for invalid start date")
	}
	if getDaysInRange("2026-01-01", "invalid") != nil {
		t.Error("expected nil for invalid end date")
	}
}

func TestParseActivityDateRange(t *testing.T) {
	// 1. Exact day range
	s, e, mk, isR := parseActivityDateRange("2026-03-05", "2026-03-20")
	if !isR || s != "2026-03-05" || e != "2026-03-20" || len(mk) != 1 || mk[0] != "2026-03" {
		t.Errorf("unexpected for exact day range: isR=%v, s=%s, e=%s, mk=%v", isR, s, e, mk)
	}

	// 2. Cross-month day range
	s, e, mk, isR = parseActivityDateRange("2026-02-25", "2026-03-05")
	if !isR || s != "2026-02-25" || e != "2026-03-05" || len(mk) != 2 || mk[0] != "2026-02" || mk[1] != "2026-03" {
		t.Errorf("unexpected for cross-month range: isR=%v, s=%s, e=%s, mk=%v", isR, s, e, mk)
	}

	// 3. Inverted day range (auto-swapped)
	s, e, mk, isR = parseActivityDateRange("2026-03-20", "2026-03-05")
	if !isR || s != "2026-03-05" || e != "2026-03-20" {
		t.Errorf("unexpected for inverted day range: isR=%v, s=%s, e=%s", isR, s, e)
	}

	// 4. Single day provided
	s, e, mk, isR = parseActivityDateRange("2026-03-15", "")
	if !isR || s != "2026-03-15" || e != "2026-03-15" || len(mk) != 1 || mk[0] != "2026-03" {
		t.Errorf("unexpected for single day: isR=%v, s=%s, e=%s, mk=%v", isR, s, e, mk)
	}

	// 5. Month string fallback
	s, e, mk, isR = parseActivityDateRange("2026-01", "2026-03")
	if !isR || s != "2026-01-01" || e != "2026-03-31" || len(mk) != 3 {
		t.Errorf("unexpected for month strings: isR=%v, s=%s, e=%s, mk=%v", isR, s, e, mk)
	}

	// 6. Inverted month strings
	s, e, mk, isR = parseActivityDateRange("2026-03", "2026-01")
	if !isR || s != "2026-01-01" || e != "2026-03-31" || len(mk) != 3 {
		t.Errorf("unexpected for inverted month strings: isR=%v, s=%s, e=%s, mk=%v", isR, s, e, mk)
	}

	// 7. Invalid dates
	s, e, mk, isR = parseActivityDateRange("invalid", "also-bad")
	if isR || s != "" || e != "" || len(mk) != 0 {
		t.Errorf("expected false for invalid dates, got isR=%v", isR)
	}

	// 8. Both empty
	s, e, mk, isR = parseActivityDateRange("", "")
	if isR {
		t.Errorf("expected false when both empty")
	}
}

func TestActivityPage_DateRangeFiltering(t *testing.T) {
	d := newExtraTestDB(t)

	statusID, _ := d.CreateStatus(models.Status{Name: "Present", Color: "#22c55e", Billable: true, OnSite: true, SortOrder: 1})
	member, _ := d.CreateLocalUser("worker@test.com", "Worker", "password1")
	teamID, _ := d.CreateTeam("Dev Team")
	d.AddTeamMember(teamID, member) //nolint:errcheck

	adminID, _ := d.CreateLocalUser("admin@test.com", "Admin", "password1")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	tok, _ := d.CreateSession(adminID)

	// Set presences in January, February, and March 2026
	d.SetPresences(member, []string{"2026-01-15"}, statusID, "") //nolint:errcheck
	d.SetPresences(member, []string{"2026-02-16"}, statusID, "") //nolint:errcheck
	d.SetPresences(member, []string{"2026-03-17"}, statusID, "") //nolint:errcheck

	var captured map[string]interface{}
	h := &ActivityHandler{
		DB: d,
		Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
			captured = data.(map[string]interface{})
		},
		DisableProjects: true,
	}

	// 1. Single month view (default without date_from / date_to)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?year=2026&month=1&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		if isRange, ok := captured["IsRange"].(bool); !ok || isRange {
			t.Errorf("expected IsRange=false for single month, got %v", captured["IsRange"])
		}
		if captured["FilterDateFrom"] != "2026-01-01" || captured["FilterDateTo"] != "2026-01-31" {
			t.Errorf("unexpected default filter dates: from=%v, to=%v", captured["FilterDateFrom"], captured["FilterDateTo"])
		}
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 1.0 {
			t.Errorf("expected 1.0 billable day in Jan, got %v", totalBillable)
		}
	}

	// 2. Exact day range view (2026-01-10 to 2026-02-20)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-01-10&date_to=2026-02-20&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		if isRange, ok := captured["IsRange"].(bool); !ok || !isRange {
			t.Fatalf("expected IsRange=true for range query, got %v", captured["IsRange"])
		}
		if captured["FilterDateFrom"] != "2026-01-10" || captured["FilterDateTo"] != "2026-02-20" {
			t.Errorf("unexpected filters: from=%v, to=%v", captured["FilterDateFrom"], captured["FilterDateTo"])
		}
		if captured["PeriodStartDay"] != 10 || captured["PeriodStartMonth"] != 1 || captured["PeriodStartYear"] != 2026 {
			t.Errorf("unexpected period start: %v/%v/%v", captured["PeriodStartDay"], captured["PeriodStartMonth"], captured["PeriodStartYear"])
		}
		if captured["PeriodEndDay"] != 20 || captured["PeriodEndMonth"] != 2 || captured["PeriodEndYear"] != 2026 {
			t.Errorf("unexpected period end: %v/%v/%v", captured["PeriodEndDay"], captured["PeriodEndMonth"], captured["PeriodEndYear"])
		}
		// Presences on 2026-01-15 and 2026-02-16 are in range, 2026-03-17 is outside
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 2.0 {
			t.Errorf("expected 2.0 billable days across Jan 10 - Feb 20, got %v", totalBillable)
		}
		// In range view, CanDecertify must be false
		if canDecertify := captured["CanDecertify"].(bool); canDecertify {
			t.Errorf("expected CanDecertify=false in range view, got %v", canDecertify)
		}
		// Navigation span should shift by 42 days (Jan 10 to Feb 20 = 42 days)
		if captured["PrevDateFrom"] == "" || captured["NextDateFrom"] == "" {
			t.Errorf("expected non-empty navigation dates")
		}
	}

	// 3. Inverted range auto-swap (date_from=2026-02-20&date_to=2026-01-10)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-02-20&date_to=2026-01-10&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		if isRange, ok := captured["IsRange"].(bool); !ok || !isRange {
			t.Fatalf("expected IsRange=true for inverted range query, got %v", captured["IsRange"])
		}
		if captured["FilterDateFrom"] != "2026-01-10" || captured["FilterDateTo"] != "2026-02-20" {
			t.Errorf("expected auto-swapped 2026-01-10 to 2026-02-20, got %v to %v", captured["FilterDateFrom"], captured["FilterDateTo"])
		}
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 2.0 {
			t.Errorf("expected 2.0 billable days, got %v", totalBillable)
		}
	}

	// 4. Single day parameter provided (date_from=2026-02-16 only)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-02-16&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		if isRange, ok := captured["IsRange"].(bool); !ok || !isRange {
			t.Fatalf("expected IsRange=true when date_from provided, got %v", captured["IsRange"])
		}
		if captured["FilterDateFrom"] != "2026-02-16" || captured["FilterDateTo"] != "2026-02-16" {
			t.Errorf("expected 2026-02-16 to 2026-02-16, got %v to %v", captured["FilterDateFrom"], captured["FilterDateTo"])
		}
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 1.0 {
			t.Errorf("expected 1.0 billable day on Feb 16, got %v", totalBillable)
		}
	}

	// 5. Month string fallback (date_from=2026-01&date_to=2026-03)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-01&date_to=2026-03&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		if isRange, ok := captured["IsRange"].(bool); !ok || !isRange {
			t.Fatalf("expected IsRange=true for month query, got %v", captured["IsRange"])
		}
		if captured["FilterDateFrom"] != "2026-01-01" || captured["FilterDateTo"] != "2026-03-31" {
			t.Errorf("unexpected month fallback: from=%v, to=%v", captured["FilterDateFrom"], captured["FilterDateTo"])
		}
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 3.0 {
			t.Errorf("expected 3.0 billable days across Jan-Mar, got %v", totalBillable)
		}
	}
}

func TestActivityAPI_DateRange(t *testing.T) {
	d := newExtraTestDB(t)

	statusID, _ := d.CreateStatus(models.Status{Name: "Present", Color: "#22c55e", Billable: true, OnSite: true, SortOrder: 1})
	member, _ := d.CreateLocalUser("apiworker@test.com", "API Worker", "password1")
	teamID, _ := d.CreateTeam("API Team")
	d.AddTeamMember(teamID, member) //nolint:errcheck

	adminID, _ := d.CreateLocalUser("apiadmin@test.com", "API Admin", "password1")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	tok, _ := d.CreateSession(adminID)

	d.SetPresences(member, []string{"2026-01-10", "2026-02-10"}, statusID, "") //nolint:errcheck

	h := &ActivityHandler{DB: d, DisableProjects: true}

	// 1. Exact day range (Jan 5 to Jan 20): only Jan 10 matches
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/activity?team_id=%d&date_from=2026-01-05&date_to=2026-01-20", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityAPI)).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
		}
		var stats []models.UserStats
		if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v", err)
		}
		if len(stats) != 1 || stats[0].BillableDays != 1.0 {
			t.Errorf("expected 1.0 billable day for exact day range, got %v", stats[0].BillableDays)
		}
	}

	// 2. Month-level range: both match
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/activity?team_id=%d&date_from=2026-01&date_to=2026-02", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityAPI)).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		var stats []models.UserStats
		if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v", err)
		}

		if len(stats) != 1 {
			t.Fatalf("expected 1 stat row, got %d", len(stats))
		}
		if stats[0].BillableDays != 2.0 {
			t.Errorf("expected 2.0 billable days across Jan-Feb, got %v", stats[0].BillableDays)
		}
	}
}

func TestActivityPage_DateRangeCertifications(t *testing.T) {
	d := newExtraTestDB(t)

	statusID, _ := d.CreateStatus(models.Status{Name: "Present", Color: "#22c55e", Billable: true, OnSite: true, SortOrder: 1})
	member, _ := d.CreateLocalUser("certworker@test.com", "Cert Worker", "password1")
	teamID, _ := d.CreateTeam("Cert Team")
	d.AddTeamMember(teamID, member) //nolint:errcheck

	adminID, _ := d.CreateLocalUser("certadmin@test.com", "Cert Admin", "password1")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	tok, _ := d.CreateSession(adminID)

	d.SetPresences(member, []string{"2026-01-10", "2026-02-10"}, statusID, "") //nolint:errcheck

	// Certify member in Jan 2026 only, not in Feb 2026
	_ = d.CertifyMonth(member, 2026, 1, adminID)

	var captured map[string]interface{}
	h := &ActivityHandler{
		DB: d,
		Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
			captured = data.(map[string]interface{})
		},
		DisableProjects: true,
	}

	// 1. Single month Jan 2026: user should be certified
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?year=2026&month=1&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		certMap := captured["Certified"].(map[int64]bool)
		if !certMap[member] {
			t.Errorf("expected user to be certified in Jan 2026")
		}
	}

	// 2. Multi-month Jan to Feb 2026: user not certified in Feb, so should NOT be certified overall
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-01&date_to=2026-02&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		certMap := captured["Certified"].(map[int64]bool)
		if certMap[member] {
			t.Errorf("expected user NOT to be certified across Jan-Feb when only certified in Jan")
		}
	}

	// 3. Now certify in Feb as well: user should be certified overall
	_ = d.CertifyMonth(member, 2026, 2, adminID)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-01&date_to=2026-02&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		certMap := captured["Certified"].(map[int64]bool)
		if !certMap[member] {
			t.Errorf("expected user to be certified across Jan-Feb after certifying both months")
		}
	}
}

func TestComputeProjectActivityForMonths_MultiMonth(t *testing.T) {
	d := newExtraTestDB(t)
	h := &ActivityHandler{DB: d}

	uid, _ := d.CreateLocalUser("projuser@test.com", "Proj User", "password1")
	projID, _ := d.CreateProject("Project Range", "PR01", 0, true, "2026-01-01", "2026-12-31")

	// Declare 2 days in Jan 2026, and 3 days in Feb 2026
	_ = d.SetProjectTimeEntry(uid, projID, 2026, 1, 2.0)
	_ = d.SetProjectTimeEntry(uid, projID, 2026, 2, 3.0)

	// User has 10 billable days total across the 2 months
	stats := []models.UserStats{{User: models.User{ID: uid}, BillableDays: 10.0}}
	byUser, total := h.computeProjectActivityForMonths(stats, []string{"2026-01", "2026-02"})

	// Total declared = 5.0, percentage = (5.0 / 10.0) * 100 = 50.0%
	if total != 5.0 {
		t.Errorf("expected total declared 5.0, got %v", total)
	}
	if byUser[uid] != 50.0 {
		t.Errorf("expected 50%% activity, got %v", byUser[uid])
	}
}

func TestComputeManualProjectActivityForMonths_MultiMonth(t *testing.T) {
	d := newExtraTestDB(t)
	h := &ActivityHandler{DB: d}

	uid, _ := d.CreateLocalUser("manualrange@test.com", "Manual Range", "password1")
	statusID, _ := d.CreateStatus(models.Status{Name: "Billable", Color: "#22c55e", Billable: true, SortOrder: 1})

	// Day 1 in Jan: complete (100%)
	_ = d.SetPresences(uid, []string{"2026-01-05"}, statusID, "full")
	_, _ = d.CreateProjectActivity(uid, "2026-01-05", models.ActivityTypeOther, "", "", "", 100)

	// Day 2 in Feb: complete (100%)
	_ = d.SetPresences(uid, []string{"2026-02-05"}, statusID, "full")
	_, _ = d.CreateProjectActivity(uid, "2026-02-05", models.ActivityTypeOther, "", "", "", 100)

	// Day 3 in Feb: incomplete (40%)
	_ = d.SetPresences(uid, []string{"2026-02-06"}, statusID, "full")
	_, _ = d.CreateProjectActivity(uid, "2026-02-06", models.ActivityTypeOther, "", "", "", 40)

	// User has 4 billable days total across Jan and Feb
	stats := []models.UserStats{{User: models.User{ID: uid}, BillableDays: 4.0}}
	byUser, total := h.computeManualProjectActivityForMonths(stats, []string{"2026-01", "2026-02"})

	// Total declared = 2.0 (Jan 5 and Feb 5 count, Feb 6 incomplete doesn't)
	if total != 2.0 {
		t.Errorf("expected total declared 2.0, got %v", total)
	}
	expectedPct := (2.0 / 4.0) * 100.0
	if byUser[uid] != expectedPct {
		t.Errorf("expected %v%% activity, got %v", expectedPct, byUser[uid])
	}
}

func TestComputeManualProjectActivityForRange_DayLevel(t *testing.T) {
	d := newExtraTestDB(t)
	h := &ActivityHandler{DB: d}

	uid, _ := d.CreateLocalUser("manualday@test.com", "Manual Day", "password1")
	statusID, _ := d.CreateStatus(models.Status{Name: "Billable", Color: "#22c55e", Billable: true, SortOrder: 1})

	// Day 1: 2026-03-05: complete (100%)
	_ = d.SetPresences(uid, []string{"2026-03-05"}, statusID, "full")
	_, _ = d.CreateProjectActivity(uid, "2026-03-05", models.ActivityTypeOther, "", "", "", 100)

	// Day 2: 2026-03-06: incomplete (40%)
	_ = d.SetPresences(uid, []string{"2026-03-06"}, statusID, "full")
	_, _ = d.CreateProjectActivity(uid, "2026-03-06", models.ActivityTypeOther, "", "", "", 40)

	// Day 3: 2026-03-20: complete (100%), but later in the month
	_ = d.SetPresences(uid, []string{"2026-03-20"}, statusID, "full")
	_, _ = d.CreateProjectActivity(uid, "2026-03-20", models.ActivityTypeOther, "", "", "", 100)

	// Query partial date range: March 1 to March 10 (only days 05 and 06 are in range)
	stats1 := []models.UserStats{{User: models.User{ID: uid}, BillableDays: 2.0}}
	byUser1, total1 := h.computeManualProjectActivityForRange(stats1, "2026-03-01", "2026-03-10")
	if total1 != 1.0 {
		t.Errorf("expected 1.0 declared day in [03-01, 03-10], got %v", total1)
	}
	if byUser1[uid] != 50.0 {
		t.Errorf("expected 50%% activity, got %v", byUser1[uid])
	}

	// Query entire month: March 1 to March 31 (all 3 days are in range)
	stats2 := []models.UserStats{{User: models.User{ID: uid}, BillableDays: 3.0}}
	byUser2, total2 := h.computeManualProjectActivityForRange(stats2, "2026-03-01", "2026-03-31")
	if total2 != 2.0 {
		t.Errorf("expected 2.0 declared days in [03-01, 03-31], got %v", total2)
	}
	expectedPct := (2.0 / 3.0) * 100.0
	if math.Abs(byUser2[uid]-expectedPct) > 0.001 {
		t.Errorf("expected ~%v%% activity, got %v", expectedPct, byUser2[uid])
	}
}
