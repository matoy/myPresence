package reminders

import (
	"context"
	"testing"
	"time"

	"github.com/matoy/mypresence/internal/config"
	"github.com/matoy/mypresence/internal/db"
	"github.com/matoy/mypresence/internal/models"
)

func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(&config.Config{DBDriver: "sqlite", DataDir: dir})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	_ = d.SeedDefaults("admin@example.com", "admin123")
	t.Cleanup(func() { d.Close() })
	return d
}

func TestResolveUserLang(t *testing.T) {
	svc := NewService(nil, &config.Config{DefaultLang: "en"})

	// 1. Explicit user language
	u1 := models.User{Language: "fr", SiteCountryCode: "DE"}
	if got := svc.resolveUserLang(u1); got != "fr" {
		t.Errorf("expected fr, got %s", got)
	}

	// 2. Fallback to site country code
	u2 := models.User{Language: "", SiteCountryCode: "DE"}
	if got := svc.resolveUserLang(u2); got != "de" {
		t.Errorf("expected de, got %s", got)
	}

	// 3. Fallback to team country codes
	u3Team := models.User{Language: "", SiteCountryCode: ""}
	teamFR := models.Team{CountryCodes: "FR,ES"}
	if got := svc.resolveUserLang(u3Team, teamFR); got != "fr" {
		t.Errorf("expected fr from team country codes, got %s", got)
	}

	// 4. Fallback to defaultLang
	u4 := models.User{Language: "", SiteCountryCode: "US"}
	if got := svc.resolveUserLang(u4); got != "en" {
		t.Errorf("expected en, got %s", got)
	}
}

func TestCheckAndSendReminders_Presence(t *testing.T) {
	d := newTestDB(t)
	cfg := &config.Config{DefaultLang: "en"}
	svc := NewService(d, cfg)

	// Create user
	u, err := d.UpsertUser("alice@example.com", "Alice")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	_ = d.UpdateUserLanguage(u.ID, "fr")

	// Create team with presence reminder (next 2 days)
	teamID, err := d.CreateTeam("Team Alpha")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	err = d.UpdateTeamReminders(teamID, true, 2, false, 0)
	if err != nil {
		t.Fatalf("update team reminders: %v", err)
	}
	_ = d.AddTeamMember(teamID, u.ID)

	testNow := time.Now()
	upcomingDays := svc.getUpcomingWorkingDays(u.ID, testNow, 2)
	if len(upcomingDays) < 2 {
		t.Fatalf("expected at least 2 upcoming working days, got %v", upcomingDays)
	}

	// 1. First run: Alice has not declared presence for upcoming days -> 1 reminder sent
	pSent, aSent, err := svc.CheckAndSendReminders(testNow, teamID, false)
	if err != nil {
		t.Fatalf("CheckAndSendReminders: %v", err)
	}
	if pSent != 1 || aSent != 0 {
		t.Fatalf("expected pSent=1, aSent=0, got pSent=%d, aSent=%d", pSent, aSent)
	}

	// Verify notification content and language
	notifs, err := d.GetUnreadNotifications(u.ID)
	if err != nil {
		t.Fatalf("get unread notifs: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notif, got %d", len(notifs))
	}
	if notifs[0].Type != "reminder_presence" {
		t.Errorf("expected type reminder_presence, got %s", notifs[0].Type)
	}
	if notifs[0].Title != "Rappel : déclaration de présence" {
		t.Errorf("expected French title, got %q", notifs[0].Title)
	}

	// 2. Second run without force: deduplication should prevent sending again today
	pSent2, _, _ := svc.CheckAndSendReminders(testNow, teamID, false)
	if pSent2 != 0 {
		t.Errorf("expected pSent2=0 due to deduplication, got %d", pSent2)
	}

	// 3. Third run with force=true: should bypass deduplication
	pSent3, _, _ := svc.CheckAndSendReminders(testNow, teamID, true)
	if pSent3 != 1 {
		t.Errorf("expected pSent3=1 with force, got %d", pSent3)
	}

	// 4. Now declare presence for upcoming days
	statuses, _ := d.ListStatuses()
	var presentStatusID int64
	for _, s := range statuses {
		if s.Billable {
			presentStatusID = s.ID
			break
		}
	}
	err = d.SetPresences(u.ID, upcomingDays, presentStatusID, "full")
	if err != nil {
		t.Fatalf("SetPresences failed: %v", err)
	}

	// Fourth run with force=true: presence is now fully declared -> 0 sent
	pSent4, _, _ := svc.CheckAndSendReminders(testNow, teamID, true)
	if pSent4 != 0 {
		t.Errorf("expected pSent4=0 since presence declared, got %d", pSent4)
	}
}

func TestCheckAndSendReminders_Activity(t *testing.T) {
	d := newTestDB(t)
	cfg := &config.Config{DefaultLang: "en"}
	svc := NewService(d, cfg)

	u, err := d.UpsertUser("bob@example.com", "Bob")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}

	// Create team with activity reminder (past 2 days)
	teamID, err := d.CreateTeam("Team Beta")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	_ = d.UpdateTeamReminders(teamID, false, 0, true, 2)
	_ = d.AddTeamMember(teamID, u.ID)

	testNow := time.Now()
	pastDays := svc.getPastWorkingDays(u.ID, testNow, 2)
	if len(pastDays) < 1 {
		t.Fatalf("expected past working days, got %v", pastDays)
	}

	// 1. Without billable presence in past days, no activity is required -> 0 sent
	pSent, aSent, _ := svc.CheckAndSendReminders(testNow, teamID, true)
	if pSent != 0 || aSent != 0 {
		t.Fatalf("expected pSent=0, aSent=0 when no billable presence, got p=%d, a=%d", pSent, aSent)
	}

	// Set billable presence for most recent past working day
	statuses, _ := d.ListStatuses()
	var billableStatusID int64
	for _, s := range statuses {
		if s.Billable {
			billableStatusID = s.ID
			break
		}
	}
	targetDate := pastDays[0]
	err = d.SetPresences(u.ID, []string{targetDate}, billableStatusID, "full")
	if err != nil {
		t.Fatalf("SetPresences: %v", err)
	}

	// 2. Billable day without activity declared -> 1 reminder sent
	_, aSent2, err := svc.CheckAndSendReminders(testNow, teamID, true)
	if err != nil {
		t.Fatalf("CheckAndSendReminders: %v", err)
	}
	if aSent2 != 1 {
		t.Fatalf("expected aSent2=1, got %d", aSent2)
	}

	// 3. Declare complete project activity for targetDate (100%)
	_, err = d.CreateProjectActivity(u.ID, targetDate, "jira", "PROJ-1", "Feature A", "Doing work", 100.0)
	if err != nil {
		t.Fatalf("CreateProjectActivity: %v", err)
	}

	// 4. Now activity is complete -> 0 sent
	_, aSent3, _ := svc.CheckAndSendReminders(testNow, teamID, true)
	if aSent3 != 0 {
		t.Fatalf("expected aSent3=0 when activity complete, got %d", aSent3)
	}
}

func TestStartWorker_ContextCancel(t *testing.T) {
	d := newTestDB(t)
	svc := NewService(d, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	done := make(chan struct{})
	go func() {
		svc.StartWorker(ctx)
		close(done)
	}()
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("StartWorker did not exit on context cancel")
	}
}
