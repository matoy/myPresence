package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/matoy/mypresence/internal/models"
)

// GetUserBillableDatesForRange returns the billable weight (1.0 for a full day,
// 0.5 for a half day) for each date in [startDate, endDate] that has a billable
// presence status set for the user.
func (d *DB) GetUserBillableDatesForRange(userID int64, startDate, endDate string) (map[string]float64, error) {
	rows, err := d.presence.Query(`
SELECT p.date, p.half
FROM presences p
JOIN statuses s ON p.status_id = s.id
WHERE p.user_id = ? AND p.date >= ? AND p.date <= ? AND s.billable = ?
ORDER BY p.date`, userID, startDate, endDate, true)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	result := make(map[string]float64)
	for rows.Next() {
		var date, half string
		if err := rows.Scan(&date, &half); err != nil {
			return nil, err
		}
		weight := 1.0
		if half == "AM" || half == "PM" {
			weight = 0.5
		}
		result[date] += weight
	}
	return result, rows.Err()
}

// GetUserBillableDatesForMonth returns the billable weight (1.0 for a full day,
// 0.5 for a half day) for each date in the given month that has a billable
// presence status set for the user.
func (d *DB) GetUserBillableDatesForMonth(userID int64, year, month int) (map[string]float64, error) {
	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	return d.GetUserBillableDatesForRange(userID, startDate, lastDay)
}

// GetUserBillableWeightForDate returns the billable weight (0, 0.5 or 1.0) for
// a single date, summing AM/PM halves when both are billable.
func (d *DB) GetUserBillableWeightForDate(userID int64, date string) (float64, error) {
	rows, err := d.presence.Query(`
SELECT p.half
FROM presences p
JOIN statuses s ON p.status_id = s.id
WHERE p.user_id = ? AND p.date = ? AND s.billable = ?`, userID, date, true)
	if err != nil {
		return 0, err
	}
	defer rows.Close() //nolint:errcheck

	var weight float64
	for rows.Next() {
		var half string
		if err := rows.Scan(&half); err != nil {
			return 0, err
		}
		if half == "AM" || half == "PM" {
			weight += 0.5
		} else {
			weight += 1.0
		}
	}
	return weight, rows.Err()
}

// ListUserActivitiesForRange returns all project activities declared by a user
// between startDate and endDate (inclusive), ordered by date then creation order.
func (d *DB) ListUserActivitiesForRange(userID int64, startDate, endDate string) ([]models.ProjectActivity, error) {
	rows, err := d.projects.Query(`
SELECT id, user_id, date, activity_type, jira_key, jira_title, comment, percentage, created_at, updated_at
FROM project_activities
WHERE user_id = ? AND date >= ? AND date <= ?
ORDER BY date, id`, userID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	return scanProjectActivities(rows)
}

// ListUserActivitiesForMonth returns all project activities declared by a user
// for the given month, ordered by date then creation order.
func (d *DB) ListUserActivitiesForMonth(userID int64, year, month int) ([]models.ProjectActivity, error) {
	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	return d.ListUserActivitiesForRange(userID, startDate, lastDay)
}

// GetActivitiesForUsersMonth returns all project activities declared by any of
// the given users for the given month, ordered by user then date.
func (d *DB) GetActivitiesForUsersMonth(userIDs []int64, year, month int) ([]models.ProjectActivity, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	datePrefix := fmt.Sprintf("%04d-%02d-%%", year, month)
	placeholders := make([]string, len(userIDs))
	args := make([]interface{}, 0, len(userIDs)+1)
	for i, id := range userIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, datePrefix)
	rows, err := d.projects.Query(
		`SELECT id, user_id, date, activity_type, jira_key, jira_title, comment, percentage, created_at, updated_at
         FROM project_activities
         WHERE user_id IN (`+joinStrings(placeholders, ",")+`) AND date LIKE ?
         ORDER BY user_id, date, id`, args...)
	if err != nil {
		return nil, err
	}
	return scanProjectActivities(rows)
}

func scanProjectActivities(rows *sql.Rows) ([]models.ProjectActivity, error) {
	defer rows.Close() //nolint:errcheck
	var activities []models.ProjectActivity
	for rows.Next() {
		var a models.ProjectActivity
		var createdAt, updatedAt string
		if err := rows.Scan(&a.ID, &a.UserID, &a.Date, &a.ActivityType, &a.JiraKey, &a.JiraTitle, &a.Comment, &a.Percentage, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
		a.UpdatedAt, _ = time.Parse("2006-01-02T15:04:05Z", updatedAt)
		activities = append(activities, a)
	}
	return activities, rows.Err()
}

// GetProjectActivity returns a single activity by ID.
func (d *DB) GetProjectActivity(id int64) (models.ProjectActivity, error) {
	var a models.ProjectActivity
	var createdAt, updatedAt string
	err := d.projects.QueryRow(`
SELECT id, user_id, date, activity_type, jira_key, jira_title, comment, percentage, created_at, updated_at
FROM project_activities WHERE id = ?`, id).Scan(
		&a.ID, &a.UserID, &a.Date, &a.ActivityType, &a.JiraKey, &a.JiraTitle, &a.Comment, &a.Percentage, &createdAt, &updatedAt)
	if err != nil {
		return a, err
	}
	a.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
	a.UpdatedAt, _ = time.Parse("2006-01-02T15:04:05Z", updatedAt)
	return a, nil
}

// GetUserActivitiesTotalForDate returns the sum of percentages already declared
// by a user for a given date, optionally excluding one activity ID (used when
// validating an update).
func (d *DB) GetUserActivitiesTotalForDate(userID int64, date string, excludeID int64) (float64, error) {
	var total float64
	err := d.projects.QueryRow(`
SELECT COALESCE(SUM(percentage), 0) FROM project_activities
WHERE user_id = ? AND date = ? AND id != ?`, userID, date, excludeID).Scan(&total)
	return total, err
}

// CreateProjectActivity inserts a new activity entry and returns its ID.
func (d *DB) CreateProjectActivity(userID int64, date, activityType, jiraKey, jiraTitle, comment string, percentage float64) (int64, error) {
	return d.projects.InsertGetID(`
INSERT INTO project_activities (user_id, date, activity_type, jira_key, jira_title, comment, percentage)
VALUES (?, ?, ?, ?, ?, ?, ?)`, userID, date, activityType, jiraKey, jiraTitle, comment, percentage)
}

// UpdateProjectActivity updates an existing activity entry's fields.
func (d *DB) UpdateProjectActivity(id int64, activityType, jiraKey, jiraTitle, comment string, percentage float64) error {
	_, err := d.projects.Exec(d.dialect.rebind(`
UPDATE project_activities SET activity_type=?, jira_key=?, jira_title=?, comment=?, percentage=?, updated_at=`+d.dialect.now()+`
WHERE id=?`), activityType, jiraKey, jiraTitle, comment, percentage, id)
	return err
}

// DeleteProjectActivity removes an activity entry by ID.
func (d *DB) DeleteProjectActivity(id int64) error {
	_, err := d.projects.Exec(`DELETE FROM project_activities WHERE id = ?`, id)
	return err
}

// SetUserDayActivities replaces all project activities declared by a user for a given date.
// It deletes existing entries for (userID, date) and inserts the provided activities.
func (d *DB) SetUserDayActivities(userID int64, date string, activities []models.ProjectActivity) ([]models.ProjectActivity, error) {
	if _, err := d.projects.Exec("DELETE FROM project_activities WHERE user_id = ? AND date = ?", userID, date); err != nil {
		return nil, err
	}
	var created []models.ProjectActivity
	for _, a := range activities {
		id, err := d.projects.InsertGetID(`
INSERT INTO project_activities (user_id, date, activity_type, jira_key, jira_title, comment, percentage)
VALUES (?, ?, ?, ?, ?, ?, ?)`, userID, date, a.ActivityType, a.JiraKey, a.JiraTitle, a.Comment, a.Percentage)
		if err != nil {
			return nil, err
		}
		a.ID = id
		a.UserID = userID
		a.Date = date
		created = append(created, a)
	}
	return created, nil
}
