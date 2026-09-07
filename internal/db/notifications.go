package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/matoy/mypresence/internal/models"
)

// migrateNotifications creates the notifications table if it does not exist.
func (d *DB) migrateNotifications() error {
	dl := d.dialect
	ai := dl.autoincrement()
	dt := dl.datetimeType()
	bool_ := dl.boolType()

	stmt := dl.createTableIfNotExists("notifications", fmt.Sprintf(`
  id              %s,
  user_id         BIGINT NOT NULL,
  actor_id        BIGINT NOT NULL DEFAULT 0,
  type            %s NOT NULL,
  title           %s NOT NULL,
  message         %s NOT NULL,
  link            %s NOT NULL DEFAULT '',
  acknowledged    %s NOT NULL DEFAULT %s,
  acknowledged_at %s,
  created_at      %s DEFAULT CURRENT_TIMESTAMP
`, ai, dl.varcharType(64), dl.varcharType(255), dl.textType(), dl.varcharType(255), bool_, dl.boolDefault(false), dt, dt))

	if _, err := d.core.Exec(dl.rebind(stmt)); err != nil {
		return err
	}
	return nil
}

// CreateNotification inserts a new notification for the given user.
func (d *DB) CreateNotification(userID, actorID int64, notifType, title, message, link string) (int64, error) {
	dl := d.dialect
	query := `INSERT INTO notifications (user_id, actor_id, type, title, message, link, acknowledged) VALUES (?, ?, ?, ?, ?, ?, ` + dl.boolDefault(false) + `)`
	res, err := d.core.Exec(dl.rebind(query), userID, actorID, notifType, title, message, link)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, nil
	}
	return id, nil
}

// GetUnreadNotifications returns all unacknowledged notifications for a user, newest first.
func (d *DB) GetUnreadNotifications(userID int64) ([]models.Notification, error) {
	dl := d.dialect
	query := `
SELECT id, user_id, actor_id, type, title, message, link, acknowledged, acknowledged_at, created_at
FROM notifications
WHERE user_id = ? AND acknowledged = ` + dl.boolDefault(false) + `
ORDER BY created_at DESC, id DESC LIMIT 50`

	rows, err := d.core.Query(dl.rebind(query), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var notifs []models.Notification
	actorIDs := make(map[int64]struct{})
	for rows.Next() {
		var n models.Notification
		var ackAt sql.NullTime
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.ActorID, &n.Type, &n.Title, &n.Message, &n.Link,
			&n.Acknowledged, &ackAt, &n.CreatedAt,
		); err != nil {
			return nil, err
		}
		if ackAt.Valid {
			n.AcknowledgedAt = &ackAt.Time
		}
		if n.ActorID > 0 {
			actorIDs[n.ActorID] = struct{}{}
		}
		notifs = append(notifs, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(actorIDs) > 0 {
		names := d.fetchUserNames(actorIDs)
		for i := range notifs {
			if notifs[i].ActorID > 0 {
				notifs[i].ActorName = names[notifs[i].ActorID]
			}
		}
	}

	return notifs, nil
}

// AcknowledgeNotification marks a specific notification as acknowledged for the user.
func (d *DB) AcknowledgeNotification(id int64, userID int64) error {
	dl := d.dialect
	query := `UPDATE notifications SET acknowledged = ` + dl.boolDefault(true) + `, acknowledged_at = ` + dl.now() + ` WHERE id = ? AND user_id = ?`
	_, err := d.core.Exec(dl.rebind(query), id, userID)
	return err
}

// GetUserNotificationLogs returns all notifications (acknowledged and unacknowledged) for a user, newest first.
func (d *DB) GetUserNotificationLogs(userID int64, since time.Time) ([]models.Notification, error) {
	dl := d.dialect
	query := `
SELECT id, user_id, actor_id, type, title, message, link, acknowledged, acknowledged_at, created_at
FROM notifications
WHERE user_id = ?`
	args := []interface{}{userID}
	if !since.IsZero() {
		query += " AND created_at >= ?"
		if d.dialect.isSQLite() {
			args = append(args, since.UTC().Format("2006-01-02 15:04:05"))
		} else {
			args = append(args, since)
		}
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT 1000"

	rows, err := d.core.Query(dl.rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var notifs []models.Notification
	actorIDs := make(map[int64]struct{})
	for rows.Next() {
		var n models.Notification
		var ackAt sql.NullTime
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.ActorID, &n.Type, &n.Title, &n.Message, &n.Link,
			&n.Acknowledged, &ackAt, &n.CreatedAt,
		); err != nil {
			return nil, err
		}
		if ackAt.Valid {
			n.AcknowledgedAt = &ackAt.Time
		}
		if n.ActorID > 0 {
			actorIDs[n.ActorID] = struct{}{}
		}
		notifs = append(notifs, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(actorIDs) > 0 {
		names := d.fetchUserNames(actorIDs)
		for i := range notifs {
			if notifs[i].ActorID > 0 {
				notifs[i].ActorName = names[notifs[i].ActorID]
			}
		}
	}

	return notifs, nil
}

// GetAllNotifications returns recent notifications across all users, newest first.
func (d *DB) GetAllNotifications(limit int) ([]models.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	dl := d.dialect
	query := fmt.Sprintf(`
SELECT id, user_id, actor_id, type, title, message, link, acknowledged, acknowledged_at, created_at
FROM notifications
ORDER BY created_at DESC, id DESC LIMIT %d`, limit)

	rows, err := d.core.Query(dl.rebind(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var notifs []models.Notification
	userIDs := make(map[int64]struct{})
	for rows.Next() {
		var n models.Notification
		var ackAt sql.NullTime
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.ActorID, &n.Type, &n.Title, &n.Message, &n.Link,
			&n.Acknowledged, &ackAt, &n.CreatedAt,
		); err != nil {
			return nil, err
		}
		if ackAt.Valid {
			n.AcknowledgedAt = &ackAt.Time
		}
		if n.ActorID > 0 {
			userIDs[n.ActorID] = struct{}{}
		}
		if n.UserID > 0 {
			userIDs[n.UserID] = struct{}{}
		}
		notifs = append(notifs, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(userIDs) > 0 {
		names := d.fetchUserNames(userIDs)
		for i := range notifs {
			if notifs[i].ActorID > 0 {
				notifs[i].ActorName = names[notifs[i].ActorID]
			}
			if notifs[i].UserID > 0 {
				notifs[i].RecipientName = names[notifs[i].UserID]
			}
		}
	}

	return notifs, nil
}

// DeleteNotification deletes a notification by ID.
func (d *DB) DeleteNotification(id int64) error {
	dl := d.dialect
	query := `DELETE FROM notifications WHERE id = ?`
	_, err := d.core.Exec(dl.rebind(query), id)
	return err
}

// HasNotificationToday checks if a notification of a given type was sent to a user since the specified time.
func (d *DB) HasNotificationToday(userID int64, notifType string, since time.Time) (bool, error) {
	dl := d.dialect
	query := `SELECT COUNT(*) FROM notifications WHERE user_id = ? AND type = ? AND created_at >= ?`
	var arg interface{} = since
	if dl.isSQLite() {
		arg = since.UTC().Format("2006-01-02 15:04:05")
	}
	var count int
	err := d.core.QueryRow(dl.rebind(query), userID, notifType, arg).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

