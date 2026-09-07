package reminders

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/matoy/mypresence/internal/config"
	"github.com/matoy/mypresence/internal/db"
	"github.com/matoy/mypresence/internal/i18n"
	"github.com/matoy/mypresence/internal/models"
)

// Service provides reminder evaluation and notification triggering.
type Service struct {
	db  *db.DB
	cfg *config.Config
}

// NewService creates a new reminder Service.
func NewService(db *db.DB, cfg *config.Config) *Service {
	return &Service{db: db, cfg: cfg}
}

// resolveUserLang determines the preferred language for a user.
// Resolution order: user.Language -> DB fresh lookup -> site country code -> team country codes -> defaultLang -> "en".
func (s *Service) resolveUserLang(u models.User, team ...models.Team) string {
	if u.Language != "" {
		for _, sup := range i18n.Supported {
			if sup.Code == u.Language {
				return u.Language
			}
		}
	}
	if s.db != nil && u.ID > 0 {
		if dbLang := s.db.GetUserLanguage(u.ID); dbLang != "" {
			for _, sup := range i18n.Supported {
				if sup.Code == dbLang {
					return dbLang
				}
			}
		}
	}
	switch strings.ToUpper(u.SiteCountryCode) {
	case "FR", "MA":
		return "fr"
	case "DE":
		return "de"
	case "ES":
		return "es"
	case "IT":
		return "it"
	}
	if len(team) > 0 && team[0].CountryCodes != "" {
		for _, code := range strings.Split(team[0].CountryCodes, ",") {
			switch strings.ToUpper(strings.TrimSpace(code)) {
			case "FR", "MA":
				return "fr"
			case "DE":
				return "de"
			case "ES":
				return "es"
			case "IT":
				return "it"
			}
		}
	}
	defaultLang := "en"
	if s.cfg != nil && s.cfg.DefaultLang != "" {
		defaultLang = s.cfg.DefaultLang
	}
	for _, sup := range i18n.Supported {
		if sup.Code == defaultLang {
			return defaultLang
		}
	}
	return "en"
}

// getUpcomingWorkingDays returns the next `count` working days (Mon-Fri, non-holiday for the user)
// starting from `now` (inclusive).
func (s *Service) getUpcomingWorkingDays(userID int64, now time.Time, count int) []string {
	if count <= 0 {
		return nil
	}
	startDate := now.Format("2006-01-02")
	endDate := now.AddDate(0, 0, 90).Format("2006-01-02")
	hMap, _ := s.db.GetUserHolidayMap(userID, startDate, endDate)

	var days []string
	cur := now
	for len(days) < count && len(days) < 60 {
		if cur.Weekday() != time.Saturday && cur.Weekday() != time.Sunday {
			ds := cur.Format("2006-01-02")
			if _, isHol := hMap[ds]; !isHol {
				days = append(days, ds)
			}
		}
		cur = cur.AddDate(0, 0, 1)
	}
	return days
}

// getPastWorkingDays returns the past `count` working days (Mon-Fri, non-holiday for the user)
// starting backwards from yesterday (inclusive).
func (s *Service) getPastWorkingDays(userID int64, now time.Time, count int) []string {
	if count <= 0 {
		return nil
	}
	startDate := now.AddDate(0, 0, -90).Format("2006-01-02")
	endDate := now.Format("2006-01-02")
	hMap, _ := s.db.GetUserHolidayMap(userID, startDate, endDate)

	var days []string
	cur := now.AddDate(0, 0, -1)
	for len(days) < count && len(days) < 60 {
		if cur.Weekday() != time.Saturday && cur.Weekday() != time.Sunday {
			ds := cur.Format("2006-01-02")
			if _, isHol := hMap[ds]; !isHol {
				days = append(days, ds)
			}
		}
		cur = cur.AddDate(0, 0, -1)
	}
	return days
}

// CheckAndSendReminders evaluates team members and sends presence and activity reminders.
// If teamIDFilter > 0, only that team is evaluated.
// If force is true (e.g. manual trigger by global admin), daily deduplication is bypassed.
func (s *Service) CheckAndSendReminders(now time.Time, teamIDFilter int64, force bool) (presenceSent, activitySent int, err error) {
	if now.IsZero() {
		now = time.Now()
	}
	todayStr := now.Format("2006-01-02")
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var teams []models.Team
	if teamIDFilter > 0 {
		t, err := s.db.GetTeam(teamIDFilter)
		if err != nil {
			return 0, 0, fmt.Errorf("get team %d: %w", teamIDFilter, err)
		}
		teams = []models.Team{*t}
	} else {
		var err error
		teams, err = s.db.ListTeams()
		if err != nil {
			return 0, 0, fmt.Errorf("list teams: %w", err)
		}
	}

	for _, team := range teams {
		if !team.RemindPresence && !team.RemindActivity {
			continue
		}

		members, err := s.db.GetTeamMembersAt(team.ID, todayStr)
		if err != nil {
			slog.Error("reminders: failed to get team members", "team_id", team.ID, "error", err)
			continue
		}

		for _, member := range members {
			if member.Disabled {
				continue
			}

			lang := s.resolveUserLang(member, team)
			tr := i18n.T(lang)

			// 1. Presence reminder
			if team.RemindPresence && team.PresenceReminderDays > 0 {
				send := true
				if !force {
					hasNotif, _ := s.db.HasNotificationToday(member.ID, "reminder_presence", startOfToday)
					if hasNotif {
						send = false
					}
				}
				if send {
					upcomingDays := s.getUpcomingWorkingDays(member.ID, now, team.PresenceReminderDays)
					if len(upcomingDays) > 0 {
						startDate := upcomingDays[0]
						endDate := upcomingDays[len(upcomingDays)-1]
						presences, pErr := s.db.GetPresences([]int64{member.ID}, startDate, endDate)
						if pErr == nil {
							userPres := presences[member.ID]
							missing := false
							for _, d := range upcomingDays {
								if halves, ok := userPres[d]; !ok || len(halves) == 0 {
									missing = true
									break
								}
							}
							if missing {
								title := tr["notifications.reminder_presence_title"]
								msg := fmt.Sprintf(tr["notifications.reminder_presence_msg"], team.PresenceReminderDays)
								if _, err := s.db.CreateNotification(member.ID, 0, "reminder_presence", title, msg, "/calendar"); err == nil {
									presenceSent++
								}
							}
						}
					}
				}
			}

			// 2. Activity reminder
			if team.RemindActivity && team.ActivityReminderDays > 0 {
				send := true
				if !force {
					hasNotif, _ := s.db.HasNotificationToday(member.ID, "reminder_activity", startOfToday)
					if hasNotif {
						send = false
					}
				}
				if send {
					pastDays := s.getPastWorkingDays(member.ID, now, team.ActivityReminderDays)
					if len(pastDays) > 0 {
						startDate := pastDays[len(pastDays)-1] // earliest date
						endDate := pastDays[0]                // latest date (yesterday)
						billableMap, bErr := s.db.GetUserBillableDatesForRange(member.ID, startDate, endDate)
						if bErr == nil && len(billableMap) > 0 {
							activities, aErr := s.db.ListUserActivitiesForRange(member.ID, startDate, endDate)
							if aErr == nil {
								actSum := make(map[string]float64)
								for _, a := range activities {
									actSum[a.Date] += a.Percentage
								}
								missingDaysCount := 0
								for _, d := range pastDays {
									weight := billableMap[d]
									if weight > 0 {
										threshold := weight*100.0 - 0.001
										if actSum[d] < threshold {
											missingDaysCount++
										}
									}
								}
								if missingDaysCount > 0 {
									title := tr["notifications.reminder_activity_title"]
									msg := fmt.Sprintf(tr["notifications.reminder_activity_msg"], missingDaysCount)
									if _, err := s.db.CreateNotification(member.ID, 0, "reminder_activity", title, msg, "/projects"); err == nil {
										activitySent++
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return presenceSent, activitySent, nil
}

// StartWorker runs the background ticker that triggers reminder checks every weekday at 09:00.
func (s *Service) StartWorker(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var lastRunDate string
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
				continue
			}
			today := t.Format("2006-01-02")
			if t.Hour() == 9 && t.Minute() < 5 && lastRunDate != today {
				lastRunDate = today
				p, a, err := s.CheckAndSendReminders(t, 0, false)
				if err != nil {
					slog.Error("reminders: error running scheduled reminders", "error", err)
				} else {
					slog.Info("reminders: scheduled run completed", "presence_sent", p, "activity_sent", a)
				}
			}
		}
	}
}
