package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestT_KnownLanguages(t *testing.T) {
	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		if m == nil {
			t.Errorf("T(%q) returned nil", lang)
		}
	}
}

func TestT_FallsBackToEnglish(t *testing.T) {
	m := T("xx")
	if m == nil {
		t.Fatal("T(unknown) should fall back to English map, got nil")
	}
	// English map must be non-empty
	if len(m) == 0 {
		t.Error("English fallback map is empty")
	}
}

func TestT_EnglishAndFrenchDiffer(t *testing.T) {
	en := T("en")
	fr := T("fr")
	if len(en) == 0 || len(fr) == 0 {
		t.Fatal("translation maps should not be empty")
	}
	// At least one key should differ between English and French
	different := false
	for k, vEN := range en {
		if vFR, ok := fr[k]; ok && vFR != vEN {
			different = true
			break
		}
	}
	if !different {
		t.Error("English and French translations appear identical")
	}
}

func TestLangFromRequest_CookieOverrides(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})
	got := LangFromRequest(req, "en")
	if got != "fr" {
		t.Errorf("expected 'fr' from cookie, got %q", got)
	}
}

func TestLangFromRequest_InvalidCookieFallsToDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "xx"})
	got := LangFromRequest(req, "de")
	if got != "de" {
		t.Errorf("invalid cookie lang should fall back to default 'de', got %q", got)
	}
}

func TestLangFromRequest_NoCookie_ValidDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	got := LangFromRequest(req, "it")
	if got != "it" {
		t.Errorf("no cookie: expected default 'it', got %q", got)
	}
}

func TestLangFromRequest_NoCookie_InvalidDefault_FallsToEnglish(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	got := LangFromRequest(req, "zz")
	if got != "en" {
		t.Errorf("invalid default lang should fall back to 'en', got %q", got)
	}
}

func TestLangFromRequest_AllSupportedLangs(t *testing.T) {
	for _, s := range Supported {
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: s.Code})
		got := LangFromRequest(req, "en")
		if got != s.Code {
			t.Errorf("supported lang %q from cookie: got %q", s.Code, got)
		}
	}
}

func TestHelpKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"help.button_title",
		"help.title",
		"help.topic_select",
		"help.close",
		"help.general_hint",
		"help.topic.calendar.title",
		"help.topic.calendar.desc",
		"help.topic.calendar.item1",
		"help.topic.floorplan.title",
		"help.topic.projects.title",
		"help.topic.admin_activity.title",
		"help.topic.admin_projects_report.title",
		"help.topic.admin_teams.title",
		"help.topic.admin_statuses.title",
		"help.topic.admin_floorplans.title",
		"help.topic.admin_projects.title",
		"help.topic.admin_users.title",
		"help.topic.admin_domains.title",
		"help.topic.admin_holidays.title",
		"help.topic.admin_general_settings.title",
		"help.topic.admin_news.title",
		"help.topic.settings.title",
		"help.topic.impersonate.title",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required help key %q", lang, key)
			}
		}
	}
}

func TestProjectsManualKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"projects.manual.none",
		"projects.manual.non_billable",
		"projects.manual.complete",
		"projects.manual.type_jira",
		"projects.manual.type_servicenow",
		"projects.manual.type_other",
		"projects.manual.jira_placeholder",
		"projects.manual.no_tickets",
		"projects.manual.comments",
		"projects.manual.comments_mandatory",
		"projects.manual.comment_required",
		"projects.manual.save_day",
		"projects.manual.day_saved",
		"projects.manual.add_activity",
		"projects.manual.no_activities_day",
		"projects.manual.filter_hide_non_billable",
		"projects.manual.filter_hide_completed",
		"projects.manual.collapse_all",
		"projects.manual.expand_all",
		"projects.manual.unsaved",
		"projects.manual.unsaved_changes",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required manual tasks key %q", lang, key)
			}
		}
	}
}

func TestCountryKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"teams.countries",
		"teams.countries_hint",
		"teams.no_countries",
		"teams.add_country",
		"holidays.country",
		"holidays.countries_hint",
		"holidays.all_countries",
		"holidays.col_country",
		"holidays.country_placeholder",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required country key %q", lang, key)
			}
		}
	}
}

func TestFloorplanFavoriteKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"fp.favorite",
		"fp.unfavorite",
		"fp.favorite_added",
		"fp.favorite_removed",
		"help.topic.floorplan.item4",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required floorplan favorite key %q", lang, key)
			}
		}
	}
}

func TestCertLockedWarningKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"cert.locked_warning",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required cert key %q", lang, key)
			}
		}
	}
}

func TestNotificationKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"notifications.title",
		"notifications.acknowledge",
		"notifications.team_added_title",
		"notifications.team_added_msg",
		"notifications.reminder_presence_title",
		"notifications.reminder_presence_msg",
		"notifications.reminder_activity_title",
		"notifications.reminder_activity_msg",
		"notifications.delete_confirm_title",
		"notifications.delete_confirm_desc",
		"notifications.delete_success",
		"notifications.deleting",
		"notifications.col_actions",
		"logs.notifications_section",
		"logs.no_notifications",
		"logs.notif_type",
		"logs.notif_message",
		"logs.notif_status",
		"logs.notif_actor",
		"logs.notif_acknowledged",
		"logs.notif_unacknowledged",
		"logs.expand_all",
		"logs.collapse_all",
		"logs.toggle_admin",
		"logs.toggle_notifications",
		"logs.toggle_presence",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required notification key %q", lang, key)
			}
		}
	}
}

func TestTeamReminderKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"teams.reminders_section",
		"teams.remind_presence",
		"teams.presence_reminder_days",
		"teams.remind_activity",
		"teams.activity_reminder_days",
		"teams.trigger_reminders",
		"teams.trigger_reminders_tooltip",
		"teams.trigger_reminders_confirm",
		"teams.trigger_reminders_result",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required team reminder key %q", lang, key)
			}
		}
	}
}

func TestCalendarLegendKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"cal.desk_reserved",
		"cal.legend.project_activity",
		"cal.legend.override",
		"cal.legend.holiday",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required calendar legend key %q", lang, key)
			}
		}
	}
}

func TestHolidayDuplicateKey_AllLanguages(t *testing.T) {
	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		val, ok := m["admin.duplicate"]
		if !ok || val == "" {
			t.Errorf("lang %q is missing required key 'admin.duplicate'", lang)
		}
	}
}

func TestSitesReportKeys_AllLanguages(t *testing.T) {
	requiredKeys := []string{
		"nav.sites_report",
		"sites.seats",
		"sites.seats_title",
		"sites.workstations",
		"sites.workstations_title",
		"sites_report.title",
		"sites_report.site",
		"sites_report.all_sites",
		"sites_report.kpi_seats",
		"sites_report.kpi_workstations",
		"sites_report.capacity_days",
		"sites_report.kpi_attached_people",
		"sites_report.ratio",
		"sites_report.people_per_seat",
		"sites_report.people_per_desk",
		"sites_report.kpi_avg_occupancy",
		"sites_report.onsite_days",
		"sites_report.kpi_onsite_detail",
		"sites_report.kpi_reservations",
		"sites_report.reservable_seats",
		"sites_report.reservations_count",
		"sites_report.kpi_res_detail",
		"sites_report.summary",
		"sites_report.col_site",
		"sites_report.col_country",
		"sites_report.col_seats",
		"sites_report.col_workstations",
		"sites_report.col_people",
		"sites_report.col_ratio",
		"sites_report.col_working_days",
		"sites_report.col_avg_onsite",
		"sites_report.col_occupancy_rate",
		"sites_report.col_avg_reservations",
		"sites_report.col_reservation_rate",
		"sites_report.total_row",
		"sites_report.no_sites",
		"sites_report.daily",
		"sites_report.daily_hint",
		"sites_report.metric",
		"sites_report.monthly_total",
		"sites_report.daily_presences",
		"sites_report.daily_reservations",
		"help.topic.admin_sites_report.title",
		"help.topic.admin_sites_report.desc",
		"help.topic.admin_sites_report.item1",
		"help.topic.admin_sites_report.item2",
	}

	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		for _, key := range requiredKeys {
			val, ok := m[key]
			if !ok || val == "" {
				t.Errorf("lang %q is missing required sites report key %q", lang, key)
			}
		}
	}
}



