package handlers

import (
	"encoding/json"
	"fmt"
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
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 1.0 {
			t.Errorf("expected 1.0 billable day in Jan, got %v", totalBillable)
		}
	}

	// 2. Multi-month range view (2026-01 to 2026-03)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-01&date_to=2026-03&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		if isRange, ok := captured["IsRange"].(bool); !ok || !isRange {
			t.Fatalf("expected IsRange=true for range query, got %v", captured["IsRange"])
		}
		if captured["FilterDateFrom"] != "2026-01" || captured["FilterDateTo"] != "2026-03" {
			t.Errorf("unexpected filters: from=%v, to=%v", captured["FilterDateFrom"], captured["FilterDateTo"])
		}
		if captured["PeriodStartMonth"] != 1 || captured["PeriodStartYear"] != 2026 {
			t.Errorf("unexpected period start: %v/%v", captured["PeriodStartMonth"], captured["PeriodStartYear"])
		}
		if captured["PeriodEndMonth"] != 3 || captured["PeriodEndYear"] != 2026 {
			t.Errorf("unexpected period end: %v/%v", captured["PeriodEndMonth"], captured["PeriodEndYear"])
		}
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 3.0 {
			t.Errorf("expected 3.0 billable days across Jan-Mar, got %v", totalBillable)
		}
		// In range view, CanDecertify must be false
		if canDecertify := captured["CanDecertify"].(bool); canDecertify {
			t.Errorf("expected CanDecertify=false in range view, got %v", canDecertify)
		}
		// Navigation span should be 3 months: Prev = 2025-10 to 2025-12, Next = 2026-04 to 2026-06
		if captured["PrevDateFrom"] != "2025-10" || captured["PrevDateTo"] != "2025-12" {
			t.Errorf("unexpected Prev range: %v to %v", captured["PrevDateFrom"], captured["PrevDateTo"])
		}
		if captured["NextDateFrom"] != "2026-04" || captured["NextDateTo"] != "2026-06" {
			t.Errorf("unexpected Next range: %v to %v", captured["NextDateFrom"], captured["NextDateTo"])
		}
	}

	// 3. Inverted range auto-swap (date_from=2026-03&date_to=2026-01)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-03&date_to=2026-01&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		if isRange, ok := captured["IsRange"].(bool); !ok || !isRange {
			t.Fatalf("expected IsRange=true for inverted range query, got %v", captured["IsRange"])
		}
		if captured["FilterDateFrom"] != "2026-01" || captured["FilterDateTo"] != "2026-03" {
			t.Errorf("expected auto-swapped 2026-01 to 2026-03, got %v to %v", captured["FilterDateFrom"], captured["FilterDateTo"])
		}
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 3.0 {
			t.Errorf("expected 3.0 billable days, got %v", totalBillable)
		}
	}

	// 4. Single parameter provided (date_from=2026-02 only)
	{
		req := httptest.NewRequest("GET", fmt.Sprintf("/admin/activity?date_from=2026-02&team=%d", teamID), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		middleware.Auth(d, http.HandlerFunc(h.ActivityPage)).ServeHTTP(w, req)

		if isRange, ok := captured["IsRange"].(bool); !ok || !isRange {
			t.Fatalf("expected IsRange=true when date_from provided, got %v", captured["IsRange"])
		}
		if captured["FilterDateFrom"] != "2026-02" || captured["FilterDateTo"] != "2026-02" {
			t.Errorf("expected 2026-02 to 2026-02, got %v to %v", captured["FilterDateFrom"], captured["FilterDateTo"])
		}
		totalBillable := captured["TotalBillable"].(float64)
		if totalBillable != 1.0 {
			t.Errorf("expected 1.0 billable day in Feb, got %v", totalBillable)
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
