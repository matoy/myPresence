package db

import (
	"testing"
)

// newTestDB opens an isolated in-memory-style SQLite DB in a temp directory.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedUser inserts a minimal user and returns its id.
func seedUser(t *testing.T, db *DB, email string) int64 {
	t.Helper()
	res, err := db.core.Exec(
		"INSERT INTO users (email, name, role) VALUES (?, ?, 'basic')", email, email,
	)
	if err != nil {
		t.Fatalf("seedUser: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// seedOnSiteStatus inserts a status with on_site=1 and returns its id.
func seedOnSiteStatus(t *testing.T, db *DB) int64 {
	t.Helper()
	res, err := db.presence.Exec(
		"INSERT INTO statuses (name, color, billable, on_site, sort_order) VALUES ('Présent', '#22c55e', 1, 1, 1)",
	)
	if err != nil {
		t.Fatalf("seedOnSiteStatus: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// seedFloorplanAndSeat inserts a floorplan + one seat, returns (floorplanID, seatID).
func seedFloorplanAndSeat(t *testing.T, db *DB, label string) (int64, int64) {
	t.Helper()
	fpRes, err := db.floorplan.Exec("INSERT INTO floorplans (name) VALUES ('Test FP')")
	if err != nil {
		t.Fatalf("seedFloorplan: %v", err)
	}
	fpID, _ := fpRes.LastInsertId()

	sRes, err := db.floorplan.Exec(
		"INSERT INTO seats (floorplan_id, label, x_pct, y_pct) VALUES (?, ?, 50, 50)", fpID, label,
	)
	if err != nil {
		t.Fatalf("seedSeat: %v", err)
	}
	seatID, _ := sRes.LastInsertId()
	return fpID, seatID
}

// -----------------------------------------------------------------------
// GetUserReservationDates
// -----------------------------------------------------------------------

func TestGetUserReservationDates_Empty(t *testing.T) {
	db := newTestDB(t)
	m, err := db.GetUserReservationDates(1, "2026-04-01", "2026-04-30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestGetUserReservationDates_WithReservations(t *testing.T) {
	d := newTestDB(t)
	userID := seedUser(t, d, "alice@test.com")
	_, seatID := seedFloorplanAndSeat(t, d, "A1")

	dates := []string{"2026-04-14", "2026-04-15", "2026-04-16"}
	for _, date := range dates {
		_, err := d.floorplan.Exec(
			"INSERT INTO seat_reservations (seat_id, user_id, date, half) VALUES (?, ?, ?, 'full')",
			seatID, userID, date,
		)
		if err != nil {
			t.Fatalf("insert reservation %s: %v", date, err)
		}
	}

	// Full range — should find all 3
	m, err := d.GetUserReservationDates(userID, "2026-04-01", "2026-04-30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range dates {
		if !m[want] {
			t.Errorf("expected date %s in result, got %v", want, m)
		}
	}

	// Narrower range — only first two
	m2, err := d.GetUserReservationDates(userID, "2026-04-14", "2026-04-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m2) != 2 {
		t.Errorf("expected 2 dates in narrow range, got %d: %v", len(m2), m2)
	}
	if m2["2026-04-16"] {
		t.Error("2026-04-16 should be outside the query range")
	}
}

func TestGetUserReservationDates_OtherUserIsolation(t *testing.T) {
	d := newTestDB(t)
	alice := seedUser(t, d, "alice@test.com")
	bob := seedUser(t, d, "bob@test.com")
	_, seatID := seedFloorplanAndSeat(t, d, "B1")

	// Only Bob has a reservation
	d.floorplan.Exec("INSERT INTO seat_reservations (seat_id, user_id, date, half) VALUES (?, ?, '2026-04-14', 'full')", seatID, bob)

	m, err := d.GetUserReservationDates(alice, "2026-04-01", "2026-04-30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("Alice should have no reservations, got %v", m)
	}
}

// -----------------------------------------------------------------------
// BulkReserveSeat
// -----------------------------------------------------------------------

func TestBulkReserveSeat_SkipsWhenNotOnSite(t *testing.T) {
	d := newTestDB(t)
	userID := seedUser(t, d, "user@test.com")
	_, seatID := seedFloorplanAndSeat(t, d, "C1")

	// No on-site presence → should book 0
	count := d.BulkReserveSeat(seatID, userID, []string{"2026-04-14", "2026-04-15"}, "full")
	if count != 0 {
		t.Errorf("expected 0 bookings (no on-site presence), got %d", count)
	}
}

func TestBulkReserveSeat_SuccessWhenOnSite(t *testing.T) {
	d := newTestDB(t)
	userID := seedUser(t, d, "user2@test.com")
	statusID := seedOnSiteStatus(t, d)
	_, seatID := seedFloorplanAndSeat(t, d, "D1")

	// Declare on-site on two dates
	d.presence.Exec("INSERT INTO presences (user_id, date, half, status_id) VALUES (?, '2026-04-14', 'full', ?)", userID, statusID)
	d.presence.Exec("INSERT INTO presences (user_id, date, half, status_id) VALUES (?, '2026-04-15', 'full', ?)", userID, statusID)

	count := d.BulkReserveSeat(seatID, userID, []string{"2026-04-14", "2026-04-15", "2026-04-16"}, "full")
	if count != 2 {
		t.Errorf("expected 2 bookings, got %d", count)
	}
}

func TestBulkReserveSeat_SkipsTakenSeat(t *testing.T) {
	d := newTestDB(t)
	alice := seedUser(t, d, "alice2@test.com")
	bob := seedUser(t, d, "bob2@test.com")
	statusID := seedOnSiteStatus(t, d)
	_, seatID := seedFloorplanAndSeat(t, d, "E1")

	// Both on site
	d.presence.Exec("INSERT INTO presences (user_id, date, half, status_id) VALUES (?, '2026-04-14', 'full', ?)", alice, statusID)
	d.presence.Exec("INSERT INTO presences (user_id, date, half, status_id) VALUES (?, '2026-04-14', 'full', ?)", bob, statusID)

	// Alice books first
	d.floorplan.Exec("INSERT INTO seat_reservations (seat_id, user_id, date, half) VALUES (?, ?, '2026-04-14', 'full')", seatID, alice)

	// Bob tries to bulk-reserve the same seat/date — should be skipped (conflict)
	count := d.BulkReserveSeat(seatID, bob, []string{"2026-04-14"}, "full")
	if count != 0 {
		t.Errorf("expected 0 (seat taken), got %d", count)
	}
}

// -----------------------------------------------------------------------
// CancelUserReservationsForDates
// -----------------------------------------------------------------------

func TestCancelUserReservationsForDates_Empty(t *testing.T) {
	d := newTestDB(t)
	// Should be a no-op, not an error
	if err := d.CancelUserReservationsForDates(1, []string{}); err != nil {
		t.Errorf("unexpected error for empty dates: %v", err)
	}
}

func TestCancelUserReservationsForDates_RemovesOwn(t *testing.T) {
	d := newTestDB(t)
	userID := seedUser(t, d, "usr@test.com")
	_, seatID := seedFloorplanAndSeat(t, d, "F1")

	dates := []string{"2026-04-14", "2026-04-15"}
	for _, date := range dates {
		d.floorplan.Exec("INSERT INTO seat_reservations (seat_id, user_id, date, half) VALUES (?, ?, ?, 'full')", seatID, userID, date)
	}

	if err := d.CancelUserReservationsForDates(userID, dates); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, _ := d.GetUserReservationDates(userID, "2026-04-01", "2026-04-30")
	if len(m) != 0 {
		t.Errorf("expected reservations to be deleted, still have %v", m)
	}
}

func TestCancelUserReservationsForDates_PreservesOtherUser(t *testing.T) {
	d := newTestDB(t)
	alice := seedUser(t, d, "alice3@test.com")
	bob := seedUser(t, d, "bob3@test.com")
	_, seatID := seedFloorplanAndSeat(t, d, "G1")

	d.floorplan.Exec("INSERT INTO seat_reservations (seat_id, user_id, date, half) VALUES (?, ?, '2026-04-14', 'full')", seatID, alice)
	d.floorplan.Exec("INSERT INTO seat_reservations (seat_id, user_id, date, half) VALUES (?, ?, '2026-04-15', 'full')", seatID, bob)

	// Cancel only alice's dates
	d.CancelUserReservationsForDates(alice, []string{"2026-04-14"})

	// Bob's reservation on 2026-04-15 should remain
	m, _ := d.GetUserReservationDates(bob, "2026-04-01", "2026-04-30")
	if !m["2026-04-15"] {
		t.Error("bob's reservation should not have been deleted")
	}
}
