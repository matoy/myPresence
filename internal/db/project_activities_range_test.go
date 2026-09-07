package db

import (
	"testing"

	"github.com/matoy/mypresence/internal/models"
)

func TestGetUserBillableDatesForRange(t *testing.T) {
	d := newTestDB(t)
	uid := seedUser(t, d, "range_billable@test.com")
	statusID := seedBillableStatus(t, d, "Billable")

	// Set presences on May 4, May 5, May 10, May 15
	_ = d.SetPresences(uid, []string{"2026-05-04"}, statusID, "full")
	_ = d.SetPresences(uid, []string{"2026-05-05"}, statusID, "AM")
	_ = d.SetPresences(uid, []string{"2026-05-10"}, statusID, "full")
	_ = d.SetPresences(uid, []string{"2026-05-15"}, statusID, "full")

	// Range from May 5 to May 12: should only include May 5 (0.5) and May 10 (1.0)
	weights, err := d.GetUserBillableDatesForRange(uid, "2026-05-05", "2026-05-12")
	if err != nil {
		t.Fatalf("GetUserBillableDatesForRange: %v", err)
	}

	if len(weights) != 2 {
		t.Fatalf("expected 2 dates in range, got %d", len(weights))
	}
	if weights["2026-05-05"] != 0.5 {
		t.Errorf("expected 0.5 for May 5, got %v", weights["2026-05-05"])
	}
	if weights["2026-05-10"] != 1.0 {
		t.Errorf("expected 1.0 for May 10, got %v", weights["2026-05-10"])
	}
	if _, ok := weights["2026-05-04"]; ok {
		t.Errorf("May 4 should not be included in range [05-05, 05-12]")
	}
	if _, ok := weights["2026-05-15"]; ok {
		t.Errorf("May 15 should not be included in range [05-05, 05-12]")
	}
}

func TestListUserActivitiesForRange(t *testing.T) {
	d := newTestDB(t)
	uid := seedUser(t, d, "range_act@test.com")

	_, _ = d.CreateProjectActivity(uid, "2026-05-02", models.ActivityTypeOther, "", "", "", 50)
	_, _ = d.CreateProjectActivity(uid, "2026-05-08", models.ActivityTypeOther, "", "", "", 100)
	_, _ = d.CreateProjectActivity(uid, "2026-05-14", models.ActivityTypeOther, "", "", "", 100)
	_, _ = d.CreateProjectActivity(uid, "2026-05-20", models.ActivityTypeOther, "", "", "", 75)

	// Query range from May 5 to May 15: should return May 8 and May 14
	acts, err := d.ListUserActivitiesForRange(uid, "2026-05-05", "2026-05-15")
	if err != nil {
		t.Fatalf("ListUserActivitiesForRange: %v", err)
	}

	if len(acts) != 2 {
		t.Fatalf("expected 2 activities, got %d", len(acts))
	}
	if acts[0].Date != "2026-05-08" {
		t.Errorf("expected first activity on 2026-05-08, got %s", acts[0].Date)
	}
	if acts[1].Date != "2026-05-14" {
		t.Errorf("expected second activity on 2026-05-14, got %s", acts[1].Date)
	}
}
