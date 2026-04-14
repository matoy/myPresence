package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"presence-app/internal/models"

	_ "modernc.org/sqlite"
)

// DB wraps the SQL database connection.
type DB struct {
	*sql.DB
}

// Open opens or creates the SQLite database and runs migrations.
func Open(dataDir string) (*DB, error) {
	path := dataDir + "/app.db"
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)

	d := &DB{sqlDB}

	if _, err := d.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	if _, err := d.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return nil, err
	}
	if _, err := d.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return nil, err
	}

	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'basic',
			password_hash TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS teams (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS user_teams (
			user_id INTEGER NOT NULL,
			team_id INTEGER NOT NULL,
			PRIMARY KEY (user_id, team_id),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (team_id) REFERENCES teams(id) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS statuses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			color TEXT NOT NULL DEFAULT '#3b82f6',
			billable BOOLEAN NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS presences (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			date TEXT NOT NULL,
			status_id INTEGER NOT NULL,
			UNIQUE(user_id, date),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (status_id) REFERENCES statuses(id)
		);
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at DATETIME NOT NULL,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS holidays (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			date TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			allow_imputed BOOLEAN NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS presence_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			actor_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			date TEXT NOT NULL,
			status_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (actor_id) REFERENCES users(id) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS admin_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_id INTEGER NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id INTEGER NOT NULL DEFAULT 0,
			action TEXT NOT NULL,
			details TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (actor_id) REFERENCES users(id) ON DELETE CASCADE
		);
	`)
	if err != nil {
		return err
	}

	// Migrate old 'admin' role to 'global'
	d.Exec(`UPDATE users SET role = 'global' WHERE role = 'admin'`)

	// Add disabled column if it doesn't exist (idempotent)
	d.Exec(`ALTER TABLE users ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT 0`)

	// Add on_site column to statuses if it doesn't exist (idempotent)
	d.Exec(`ALTER TABLE statuses ADD COLUMN on_site BOOLEAN NOT NULL DEFAULT 0`)

	// Rename stats_viewer role to activity_viewer (idempotent)
	d.Exec(`UPDATE users SET role = REPLACE(role, 'stats_viewer', 'activity_viewer') WHERE role LIKE '%stats_viewer%'`)
	// Rename cra_viewer role to activity_viewer (idempotent)
	d.Exec(`UPDATE users SET role = REPLACE(role, 'cra_viewer', 'activity_viewer') WHERE role LIKE '%cra_viewer%'`)

	// Migrate presences table to add half-day support (idempotent)
	var halfColExists int
	d.QueryRow("SELECT COUNT(*) FROM pragma_table_info('presences') WHERE name='half'").Scan(&halfColExists)
	if halfColExists == 0 {
		d.Exec(`CREATE TABLE presences_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			date TEXT NOT NULL,
			half TEXT NOT NULL DEFAULT 'full',
			status_id INTEGER NOT NULL,
			UNIQUE(user_id, date, half),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (status_id) REFERENCES statuses(id)
		)`)
		d.Exec(`INSERT INTO presences_new (id, user_id, date, half, status_id) SELECT id, user_id, date, 'full', status_id FROM presences`)
		d.Exec(`DROP TABLE presences`)
		d.Exec(`ALTER TABLE presences_new RENAME TO presences`)
	}
	// Add half column to presence_logs (idempotent)
	d.Exec(`ALTER TABLE presence_logs ADD COLUMN half TEXT NOT NULL DEFAULT 'full'`)

	// Floorplan tables (idempotent)
	d.Exec(`CREATE TABLE IF NOT EXISTS floorplans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		image_path TEXT NOT NULL DEFAULT '',
		sort_order INTEGER NOT NULL DEFAULT 0
	)`)
	d.Exec(`CREATE TABLE IF NOT EXISTS seats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		floorplan_id INTEGER NOT NULL,
		label TEXT NOT NULL,
		x_pct REAL NOT NULL DEFAULT 0,
		y_pct REAL NOT NULL DEFAULT 0,
		FOREIGN KEY (floorplan_id) REFERENCES floorplans(id) ON DELETE CASCADE
	)`)
	d.Exec(`CREATE TABLE IF NOT EXISTS seat_reservations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		seat_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		date TEXT NOT NULL,
		half TEXT NOT NULL DEFAULT 'full',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(seat_id, date, half),
		FOREIGN KEY (seat_id) REFERENCES seats(id) ON DELETE CASCADE,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	)`)

	// Personal Access Tokens (idempotent)
	d.Exec(`CREATE TABLE IF NOT EXISTS personal_access_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		token_hash TEXT NOT NULL UNIQUE,
		token_prefix TEXT NOT NULL,
		expires_at DATETIME,
		last_used_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	)`)

	return nil
}

// SeedDefaults creates the admin user and default statuses if they don't exist.
func (d *DB) SeedDefaults(adminUser, adminPass string) error {
	// Create admin user with global role
	_, err := d.Exec(`
		INSERT OR IGNORE INTO users (email, name, role, password_hash)
		VALUES (?, ?, 'global', ?)
	`, adminUser, "Administrateur", adminPass)
	if err != nil {
		return err
	}

	// Ensure global role and password in case it was changed
	_, err = d.Exec(`UPDATE users SET role = 'global', password_hash = ? WHERE email = ?`, adminPass, adminUser)
	if err != nil {
		return err
	}

	// Seed default statuses
	var count int
	d.QueryRow("SELECT COUNT(*) FROM statuses").Scan(&count)
	if count == 0 {
		defaults := []struct {
			name     string
			color    string
			billable bool
			onSite   bool
			order    int
		}{
			{"Présent sur site", "#22c55e", true, true, 1},
			{"Télétravail", "#a855f7", true, false, 2},
			{"Déplacement", "#3b82f6", true, true, 3},
			{"Congé", "#f97316", false, false, 4},
			{"Maladie", "#ef4444", false, false, 5},
			{"Formation", "#eab308", false, false, 6},
			{"Absence", "#85888e", false, false, 7},
		}
		for _, s := range defaults {
			_, err := d.Exec(
				"INSERT INTO statuses (name, color, billable, on_site, sort_order) VALUES (?, ?, ?, ?, ?)",
				s.name, s.color, s.billable, s.onSite, s.order,
			)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// --- Session management ---

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateSession creates a new session for the user and returns the token.
func (d *DB) CreateSession(userID int64) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(24 * time.Hour * 30) // 30 days
	_, err = d.Exec("INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)",
		token, userID, expires)
	if err != nil {
		return "", err
	}
	return token, nil
}

// GetSessionUser returns the user associated with a session token.
func (d *DB) GetSessionUser(token string) (*models.User, error) {
	var u models.User
	err := d.QueryRow(`
		SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at
		FROM sessions s JOIN users u ON s.user_id = u.id
		WHERE s.id = ? AND s.expires_at > datetime('now') AND u.disabled = 0
	`, token).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsLocal = u.PasswordHash != ""
	return &u, nil
}

// DeleteSession removes a session.
func (d *DB) DeleteSession(token string) error {
	_, err := d.Exec("DELETE FROM sessions WHERE id = ?", token)
	return err
}

// CleanExpiredSessions removes expired sessions.
func (d *DB) CleanExpiredSessions() {
	d.Exec("DELETE FROM sessions WHERE expires_at < datetime('now')")
}

// --- Personal Access Tokens ---

// CreatePAT generates a new PAT (prefix "mpa_"), stores its SHA-256 hash, and returns the raw token.
func (d *DB) CreatePAT(userID int64, description string, expiresAt *time.Time) (string, *models.PersonalAccessToken, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw := "mpa_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	tokenHash := hex.EncodeToString(sum[:])
	prefix := raw[:12] // "mpa_" + first 8 hex chars

	var expiresSQL interface{}
	if expiresAt != nil {
		expiresSQL = expiresAt.UTC().Format("2006-01-02 15:04:05")
	}

	result, err := d.Exec(
		`INSERT INTO personal_access_tokens (user_id, description, token_hash, token_prefix, expires_at) VALUES (?, ?, ?, ?, ?)`,
		userID, description, tokenHash, prefix, expiresSQL,
	)
	if err != nil {
		return "", nil, err
	}
	id, _ := result.LastInsertId()
	pat := &models.PersonalAccessToken{
		ID:          id,
		UserID:      userID,
		Description: description,
		TokenPrefix: prefix,
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now(),
	}
	return raw, pat, nil
}

// ListUserPATs returns all PATs for a user (token hash never included).
func (d *DB) ListUserPATs(userID int64) ([]models.PersonalAccessToken, error) {
	rows, err := d.Query(`
		SELECT id, user_id, description, token_prefix, expires_at, last_used_at, created_at
		FROM personal_access_tokens
		WHERE user_id = ?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pats []models.PersonalAccessToken
	for rows.Next() {
		var p models.PersonalAccessToken
		var expiresAt, lastUsedAt sql.NullTime
		if err := rows.Scan(&p.ID, &p.UserID, &p.Description, &p.TokenPrefix, &expiresAt, &lastUsedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			p.ExpiresAt = &expiresAt.Time
		}
		if lastUsedAt.Valid {
			p.LastUsedAt = &lastUsedAt.Time
		}
		pats = append(pats, p)
	}
	return pats, rows.Err()
}

// RevokePAT deletes a PAT owned by the given user.
func (d *DB) RevokePAT(id, userID int64) error {
	res, err := d.Exec(`DELETE FROM personal_access_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("token not found")
	}
	return nil
}

// GetUserByPAT verifies a raw PAT (SHA-256 hash match) and returns the owning user.
// It also updates last_used_at asynchronously.
func (d *DB) GetUserByPAT(token string) (*models.User, error) {
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])

	var u models.User
	err := d.QueryRow(`
		SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at
		FROM personal_access_tokens t
		JOIN users u ON t.user_id = u.id
		WHERE t.token_hash = ?
		  AND (t.expires_at IS NULL OR t.expires_at > datetime('now'))
		  AND u.disabled = 0
	`, tokenHash).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsLocal = u.PasswordHash != ""
	go d.Exec(`UPDATE personal_access_tokens SET last_used_at = datetime('now') WHERE token_hash = ?`, tokenHash) //nolint
	return &u, nil
}

// GetUserByEmail finds a user by email.
func (d *DB) GetUserByEmail(email string) (*models.User, error) {
	var u models.User
	err := d.QueryRow(
		"SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at FROM users WHERE email = ?",
		email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsLocal = u.PasswordHash != ""
	return &u, nil
}

// GetUserByID finds a user by ID.
func (d *DB) GetUserByID(id int64) (*models.User, error) {
	var u models.User
	err := d.QueryRow(
		"SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at FROM users WHERE id = ?",
		id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsLocal = u.PasswordHash != ""
	return &u, nil
}

// UpsertUser creates a user or updates their name if they already exist (for SAML provisioning).
func (d *DB) UpsertUser(email, name string) (*models.User, error) {
	_, err := d.Exec(`
		INSERT INTO users (email, name, role) VALUES (?, ?, 'basic')
		ON CONFLICT(email) DO UPDATE SET name = excluded.name
	`, email, name)
	if err != nil {
		return nil, err
	}
	return d.GetUserByEmail(email)
}

// ListUsers returns all users.
func (d *DB) ListUsers() ([]models.User, error) {
	rows, err := d.Query("SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at FROM users ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.IsLocal = u.PasswordHash != ""
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateUserRoles updates a user's roles (comma-separated string).
func (d *DB) UpdateUserRoles(id int64, roles string) error {
	// Validate all roles
	valid := map[string]bool{
		models.RoleBasic: true, models.RoleTeamManager: true,
		models.RoleTeamLeader: true, models.RoleStatusManager: true,
		models.RoleActivityViewer: true, models.RoleFloorplanManager: true,
		models.RoleGlobal: true,
	}
	for _, r := range strings.Split(roles, ",") {
		r = strings.TrimSpace(r)
		if r != "" && !valid[r] {
			return fmt.Errorf("invalid role: %s", r)
		}
	}
	_, err := d.Exec("UPDATE users SET role = ? WHERE id = ?", roles, id)
	return err
}

// CreateLocalUser creates a new local user with a password.
func (d *DB) CreateLocalUser(email, name, password string) error {
	_, err := d.Exec(
		`INSERT INTO users (email, name, role, password_hash) VALUES (?, ?, 'basic', ?)`,
		email, name, password,
	)
	return err
}

// UpdateLocalUser updates a user's email and display name.
func (d *DB) UpdateLocalUser(id int64, email, name string) error {
	_, err := d.Exec(`UPDATE users SET email = ?, name = ? WHERE id = ?`, email, name, id)
	return err
}

// SetUserPassword changes the password for a local user.
func (d *DB) SetUserPassword(id int64, password string) error {
	_, err := d.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, password, id)
	return err
}

// SetUserDisabled enables or disables a user account.
func (d *DB) SetUserDisabled(id int64, disabled bool) error {
	_, err := d.Exec(`UPDATE users SET disabled = ? WHERE id = ?`, disabled, id)
	return err
}

// DeleteLocalUser permanently deletes a user account.
func (d *DB) DeleteLocalUser(id int64) error {
	_, err := d.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// --- Team management ---

// ListTeams returns all teams.
func (d *DB) ListTeams() ([]models.Team, error) {
	rows, err := d.Query("SELECT id, name, created_at FROM teams ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []models.Team
	for rows.Next() {
		var t models.Team
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// CreateTeam creates a new team.
func (d *DB) CreateTeam(name string) error {
	_, err := d.Exec("INSERT INTO teams (name) VALUES (?)", name)
	return err
}

// UpdateTeam renames a team.
func (d *DB) UpdateTeam(id int64, name string) error {
	_, err := d.Exec("UPDATE teams SET name = ? WHERE id = ?", name, id)
	return err
}

// DeleteTeam removes a team.
func (d *DB) DeleteTeam(id int64) error {
	_, err := d.Exec("DELETE FROM teams WHERE id = ?", id)
	return err
}

// GetTeamMembers returns users belonging to a team.
func (d *DB) GetTeamMembers(teamID int64) ([]models.User, error) {
	rows, err := d.Query(`
		SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at
		FROM users u
		JOIN user_teams ut ON u.id = ut.user_id
		WHERE ut.team_id = ?
		ORDER BY u.name
	`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.IsLocal = u.PasswordHash != ""
		users = append(users, u)
	}
	return users, rows.Err()
}

// AddTeamMember adds a user to a team.
func (d *DB) AddTeamMember(teamID, userID int64) error {
	_, err := d.Exec("INSERT OR IGNORE INTO user_teams (team_id, user_id) VALUES (?, ?)", teamID, userID)
	return err
}

// RemoveTeamMember removes a user from a team.
func (d *DB) RemoveTeamMember(teamID, userID int64) error {
	_, err := d.Exec("DELETE FROM user_teams WHERE team_id = ? AND user_id = ?", teamID, userID)
	return err
}

// GetUserTeams returns teams a user belongs to.
func (d *DB) GetUserTeams(userID int64) ([]models.Team, error) {
	rows, err := d.Query(`
		SELECT t.id, t.name, t.created_at
		FROM teams t
		JOIN user_teams ut ON t.id = ut.team_id
		WHERE ut.user_id = ?
		ORDER BY t.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []models.Team
	for rows.Next() {
		var t models.Team
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// --- Status management ---

// ListStatuses returns all statuses ordered by sort_order.
func (d *DB) ListStatuses() ([]models.Status, error) {
	rows, err := d.Query("SELECT id, name, color, billable, on_site, sort_order FROM statuses ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statuses []models.Status
	for rows.Next() {
		var s models.Status
		if err := rows.Scan(&s.ID, &s.Name, &s.Color, &s.Billable, &s.OnSite, &s.SortOrder); err != nil {
			return nil, err
		}
		statuses = append(statuses, s)
	}
	return statuses, rows.Err()
}

// CreateStatus adds a new status.
func (d *DB) CreateStatus(s models.Status) error {
	_, err := d.Exec(
		"INSERT INTO statuses (name, color, billable, on_site, sort_order) VALUES (?, ?, ?, ?, ?)",
		s.Name, s.Color, s.Billable, s.OnSite, s.SortOrder,
	)
	return err
}

// UpdateStatus modifies a status.
func (d *DB) UpdateStatus(s models.Status) error {
	_, err := d.Exec(
		"UPDATE statuses SET name = ?, color = ?, billable = ?, on_site = ?, sort_order = ? WHERE id = ?",
		s.Name, s.Color, s.Billable, s.OnSite, s.SortOrder, s.ID,
	)
	return err
}

// DeleteStatus removes a status.
func (d *DB) DeleteStatus(id int64) error {
	_, err := d.Exec("DELETE FROM statuses WHERE id = ?", id)
	return err
}

// --- Presence management ---

// GetPresences returns a map: userID -> date -> half -> statusID for the given users and date range.
func (d *DB) GetPresences(userIDs []int64, startDate, endDate string) (map[int64]map[string]map[string]int64, error) {
	result := make(map[int64]map[string]map[string]int64)
	if len(userIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, len(userIDs))
	args := make([]interface{}, 0, len(userIDs)+2)
	for i, id := range userIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, startDate, endDate)

	query := fmt.Sprintf(
		"SELECT user_id, date, half, status_id FROM presences WHERE user_id IN (%s) AND date >= ? AND date <= ?",
		strings.Join(placeholders, ","),
	)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var userID, statusID int64
		var date, half string
		if err := rows.Scan(&userID, &date, &half, &statusID); err != nil {
			return nil, err
		}
		if result[userID] == nil {
			result[userID] = make(map[string]map[string]int64)
		}
		if result[userID][date] == nil {
			result[userID][date] = make(map[string]int64)
		}
		result[userID][date][half] = statusID
	}
	return result, rows.Err()
}

// SetPresences sets the status for a user on multiple dates (upsert).
// half must be "full", "AM", or "PM". Empty defaults to "full".
// Setting "full" removes any existing AM/PM entries. Setting AM/PM removes any full-day entry.
func (d *DB) SetPresences(userID int64, dates []string, statusID int64, half string) error {
	if half == "" {
		half = "full"
	}
	if half != "full" && half != "AM" && half != "PM" {
		return fmt.Errorf("invalid half value: %s", half)
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, date := range dates {
		if half == "full" {
			// Remove any existing half-day entries for this date
			if _, err := tx.Exec("DELETE FROM presences WHERE user_id = ? AND date = ? AND half IN ('AM', 'PM')", userID, date); err != nil {
				return err
			}
		} else {
			// Remove any existing full-day entry for this date
			if _, err := tx.Exec("DELETE FROM presences WHERE user_id = ? AND date = ? AND half = 'full'", userID, date); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`
			INSERT INTO presences (user_id, date, half, status_id) VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id, date, half) DO UPDATE SET status_id = excluded.status_id
		`, userID, date, half, statusID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearPresences removes presences for a user on specific dates.
// If half is empty, all halves are cleared. Otherwise only the specified half is cleared.
func (d *DB) ClearPresences(userID int64, dates []string, half string) error {
	if len(dates) == 0 {
		return nil
	}
	if half != "" && half != "full" && half != "AM" && half != "PM" {
		return fmt.Errorf("invalid half value: %s", half)
	}
	placeholders := make([]string, len(dates))
	for i := range dates {
		placeholders[i] = "?"
	}
	datePlaceholders := strings.Join(placeholders, ",")

	var query string
	var args []interface{}
	if half == "" {
		// Clear all halves for the given dates
		query = fmt.Sprintf("DELETE FROM presences WHERE user_id = ? AND date IN (%s)", datePlaceholders)
		args = make([]interface{}, 0, 1+len(dates))
		args = append(args, userID)
		for _, d := range dates {
			args = append(args, d)
		}
	} else {
		// Clear only the specified half
		query = fmt.Sprintf("DELETE FROM presences WHERE user_id = ? AND half = ? AND date IN (%s)", datePlaceholders)
		args = make([]interface{}, 0, 2+len(dates))
		args = append(args, userID, half)
		for _, d := range dates {
			args = append(args, d)
		}
	}
	_, err := d.Exec(query, args...)
	return err
}

// --- Stats ---

// GetTeamStats returns stats for each member of a team over a date range.
func (d *DB) GetTeamStats(teamID int64, startDate, endDate string) ([]models.UserStats, error) {
	members, err := d.GetTeamMembers(teamID)
	if err != nil {
		return nil, err
	}

	statuses, err := d.ListStatuses()
	if err != nil {
		return nil, err
	}
	billableMap := make(map[int64]bool)
	onSiteMap := make(map[int64]bool)
	for _, s := range statuses {
		billableMap[s.ID] = s.Billable
		onSiteMap[s.ID] = s.OnSite
	}

	userIDs := make([]int64, len(members))
	for i, m := range members {
		userIDs[i] = m.ID
	}

	presences, err := d.GetPresences(userIDs, startDate, endDate)
	if err != nil {
		return nil, err
	}

	var stats []models.UserStats
	for _, member := range members {
		us := models.UserStats{
			User:         member,
			StatusCounts: make(map[int64]float64),
		}
		if up, ok := presences[member.ID]; ok {
			for _, halves := range up {
				for half, statusID := range halves {
					weight := 1.0
					if half == "AM" || half == "PM" {
						weight = 0.5
					}
					us.StatusCounts[statusID] += weight
					if billableMap[statusID] {
						us.BillableDays += weight
					}
					if onSiteMap[statusID] {
						us.OnSiteDays += weight
					}
				}
			}
		}
		stats = append(stats, us)
	}
	return stats, nil
}

// --- Holiday management ---

// ListHolidays returns all holidays ordered by date.
func (d *DB) ListHolidays() ([]models.Holiday, error) {
	rows, err := d.Query("SELECT id, date, name, allow_imputed FROM holidays ORDER BY date")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var holidays []models.Holiday
	for rows.Next() {
		var h models.Holiday
		if err := rows.Scan(&h.ID, &h.Date, &h.Name, &h.AllowImputed); err != nil {
			return nil, err
		}
		holidays = append(holidays, h)
	}
	return holidays, rows.Err()
}

// GetHolidayMap returns a date-keyed map of holidays for the given date range.
func (d *DB) GetHolidayMap(startDate, endDate string) (map[string]models.Holiday, error) {
	rows, err := d.Query(
		"SELECT id, date, name, allow_imputed FROM holidays WHERE date >= ? AND date <= ?",
		startDate, endDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]models.Holiday)
	for rows.Next() {
		var h models.Holiday
		if err := rows.Scan(&h.ID, &h.Date, &h.Name, &h.AllowImputed); err != nil {
			return nil, err
		}
		result[h.Date] = h
	}
	return result, rows.Err()
}

// CreateHoliday adds a new public holiday.
func (d *DB) CreateHoliday(date, name string, allowImputed bool) error {
	_, err := d.Exec(
		"INSERT INTO holidays (date, name, allow_imputed) VALUES (?, ?, ?)",
		date, name, allowImputed,
	)
	return err
}

// UpdateHoliday modifies an existing holiday.
func (d *DB) UpdateHoliday(id int64, date, name string, allowImputed bool) error {
	_, err := d.Exec(
		"UPDATE holidays SET date = ?, name = ?, allow_imputed = ? WHERE id = ?",
		date, name, allowImputed, id,
	)
	return err
}

// DeleteHoliday removes a holiday by ID.
func (d *DB) DeleteHoliday(id int64) error {
	_, err := d.Exec("DELETE FROM holidays WHERE id = ?", id)
	return err
}

// --- Presence logs ---

// LogPresenceAction records a set or clear action for each date.
func (d *DB) LogPresenceAction(actorID, userID int64, action string, dates []string, statusID int64, half string) error {
	if half == "" {
		half = "full"
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if action == "set" {
		s, err := tx.Prepare(
			"INSERT INTO presence_logs (user_id, actor_id, action, date, status_id, half) VALUES (?, ?, ?, ?, ?, ?)",
		)
		if err != nil {
			return err
		}
		defer s.Close()
		for _, date := range dates {
			if _, err := s.Exec(userID, actorID, action, date, statusID, half); err != nil {
				return err
			}
		}
	} else {
		s, err := tx.Prepare(
			"INSERT INTO presence_logs (user_id, actor_id, action, date, half) VALUES (?, ?, ?, ?, ?)",
		)
		if err != nil {
			return err
		}
		defer s.Close()
		for _, date := range dates {
			if _, err := s.Exec(userID, actorID, action, date, half); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// GetUserLogs returns the presence logs for a given user, most recent first.
// If since is non-zero, only logs after that date are returned.
func (d *DB) GetUserLogs(userID int64, since time.Time) ([]models.PresenceLog, error) {
	query := `
		SELECT pl.id, pl.user_id, pl.actor_id, u.name,
		       pl.action, pl.date, pl.half,
		       COALESCE(pl.status_id, 0), COALESCE(s.name, ''), COALESCE(s.color, ''),
		       pl.created_at
		FROM presence_logs pl
		JOIN users u ON pl.actor_id = u.id
		LEFT JOIN statuses s ON pl.status_id = s.id
		WHERE pl.user_id = ?`
	args := []interface{}{userID}
	if !since.IsZero() {
		query += " AND pl.created_at >= ?"
		args = append(args, since)
	}
	query += " ORDER BY pl.created_at DESC LIMIT 1000"

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.PresenceLog
	for rows.Next() {
		var l models.PresenceLog
		if err := rows.Scan(
			&l.ID, &l.UserID, &l.ActorID, &l.ActorName,
			&l.Action, &l.Date, &l.Half,
			&l.StatusID, &l.StatusName, &l.StatusColor,
			&l.CreatedAt,
		); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// GetTeamName returns the name of a team by ID, or empty string if not found.
func (d *DB) GetTeamName(id int64) string {
	var name string
	d.QueryRow("SELECT name FROM teams WHERE id = ?", id).Scan(&name)
	return name
}

// GetStatusName returns the name of a status by ID, or empty string if not found.
func (d *DB) GetStatusName(id int64) string {
	var name string
	d.QueryRow("SELECT name FROM statuses WHERE id = ?", id).Scan(&name)
	return name
}

// GetHolidayName returns the name of a holiday by ID, or empty string if not found.
func (d *DB) GetHolidayName(id int64) string {
	var name string
	d.QueryRow("SELECT name FROM holidays WHERE id = ?", id).Scan(&name)
	return name
}

// LogAdminAction records an admin operation on an entity (team, status, holiday, user).
func (d *DB) LogAdminAction(actorID int64, entityType string, entityID int64, action, details string) {
	d.Exec(
		"INSERT INTO admin_logs (actor_id, entity_type, entity_id, action, details) VALUES (?, ?, ?, ?, ?)",
		actorID, entityType, entityID, action, details,
	)
}

// GetAdminLogsByActor returns the admin action logs performed by a given user, most recent first.
// If since is non-zero, only logs after that date are returned.
func (d *DB) GetAdminLogsByActor(actorID int64, since time.Time) ([]models.AdminLog, error) {
	query := `
		SELECT al.id, al.actor_id, u.name, al.entity_type, al.entity_id, al.action, al.details, al.created_at,
		       COALESCE(
		           CASE WHEN al.entity_type = 'team'    THEN t.name    END,
		           CASE WHEN al.entity_type = 'status'  THEN s.name    END,
		           CASE WHEN al.entity_type = 'holiday' THEN h.name    END,
		           CASE WHEN al.entity_type = 'user' AND al.entity_id > 0 THEN u2.name END,
		           ''
		       ) AS entity_name
		FROM admin_logs al
		JOIN users u ON al.actor_id = u.id
		LEFT JOIN teams    t  ON al.entity_type = 'team'    AND al.entity_id = t.id
		LEFT JOIN statuses s  ON al.entity_type = 'status'  AND al.entity_id = s.id
		LEFT JOIN holidays h  ON al.entity_type = 'holiday' AND al.entity_id = h.id
		LEFT JOIN users    u2 ON al.entity_type = 'user'    AND al.entity_id = u2.id AND al.entity_id > 0
		WHERE al.actor_id = ?`
	args := []interface{}{actorID}
	if !since.IsZero() {
		query += " AND al.created_at >= ?"
		args = append(args, since)
	}
	query += " ORDER BY al.created_at DESC LIMIT 1000"

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.AdminLog
	for rows.Next() {
		var l models.AdminLog
		if err := rows.Scan(&l.ID, &l.ActorID, &l.ActorName, &l.EntityType, &l.EntityID, &l.Action, &l.Details, &l.CreatedAt, &l.EntityName); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// --- Floorplan management ---

// ListFloorplans returns all floorplans ordered by sort_order.
func (d *DB) ListFloorplans() ([]models.Floorplan, error) {
	rows, err := d.Query("SELECT id, name, image_path, sort_order FROM floorplans ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fps []models.Floorplan
	for rows.Next() {
		var f models.Floorplan
		if err := rows.Scan(&f.ID, &f.Name, &f.ImagePath, &f.SortOrder); err != nil {
			return nil, err
		}
		fps = append(fps, f)
	}
	return fps, rows.Err()
}

// GetFloorplan returns a single floorplan by ID.
func (d *DB) GetFloorplan(id int64) (*models.Floorplan, error) {
	var f models.Floorplan
	err := d.QueryRow("SELECT id, name, image_path, sort_order FROM floorplans WHERE id = ?", id).
		Scan(&f.ID, &f.Name, &f.ImagePath, &f.SortOrder)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// CreateFloorplan creates a new floorplan.
func (d *DB) CreateFloorplan(name string, sortOrder int) (int64, error) {
	res, err := d.Exec("INSERT INTO floorplans (name, sort_order) VALUES (?, ?)", name, sortOrder)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateFloorplan updates a floorplan's name and sort order.
func (d *DB) UpdateFloorplan(id int64, name string, sortOrder int) error {
	_, err := d.Exec("UPDATE floorplans SET name = ?, sort_order = ? WHERE id = ?", name, sortOrder, id)
	return err
}

// SetFloorplanImage stores the image path for a floorplan.
func (d *DB) SetFloorplanImage(id int64, imagePath string) error {
	_, err := d.Exec("UPDATE floorplans SET image_path = ? WHERE id = ?", imagePath, id)
	return err
}

// DeleteFloorplan removes a floorplan and all its seats.
func (d *DB) DeleteFloorplan(id int64) error {
	_, err := d.Exec("DELETE FROM floorplans WHERE id = ?", id)
	return err
}

// ListSeats returns all seats for a floorplan.
func (d *DB) ListSeats(floorplanID int64) ([]models.Seat, error) {
	rows, err := d.Query("SELECT id, floorplan_id, label, x_pct, y_pct FROM seats WHERE floorplan_id = ? ORDER BY id", floorplanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var seats []models.Seat
	for rows.Next() {
		var s models.Seat
		if err := rows.Scan(&s.ID, &s.FloorplanID, &s.Label, &s.XPct, &s.YPct); err != nil {
			return nil, err
		}
		seats = append(seats, s)
	}
	return seats, rows.Err()
}

// CreateSeat adds a seat to a floorplan.
func (d *DB) CreateSeat(floorplanID int64, label string, xPct, yPct float64) (int64, error) {
	res, err := d.Exec("INSERT INTO seats (floorplan_id, label, x_pct, y_pct) VALUES (?, ?, ?, ?)", floorplanID, label, xPct, yPct)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSeat updates a seat's label and position.
func (d *DB) UpdateSeat(id int64, label string, xPct, yPct float64) error {
	_, err := d.Exec("UPDATE seats SET label = ?, x_pct = ?, y_pct = ? WHERE id = ?", label, xPct, yPct, id)
	return err
}

// DeleteSeat removes a seat.
func (d *DB) DeleteSeat(id int64) error {
	_, err := d.Exec("DELETE FROM seats WHERE id = ?", id)
	return err
}

// GetSeatsWithStatus returns all seats enriched with booking status for a given user/date/half.
func (d *DB) GetSeatsWithStatus(floorplanID, userID int64, date, half string) ([]models.SeatWithStatus, error) {
	seats, err := d.ListSeats(floorplanID)
	if err != nil {
		return nil, err
	}

	rows, err := d.Query(`
		SELECT sr.seat_id, sr.user_id, sr.half, sr.id
		FROM seat_reservations sr
		JOIN seats s ON sr.seat_id = s.id
		WHERE s.floorplan_id = ? AND sr.date = ?
	`, floorplanID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type resEntry struct {
		uid   int64
		h     string
		resID int64
	}
	reserved := make(map[int64][]resEntry)
	for rows.Next() {
		var seatID, uid, resID int64
		var h string
		if err := rows.Scan(&seatID, &uid, &h, &resID); err != nil {
			return nil, err
		}
		reserved[seatID] = append(reserved[seatID], resEntry{uid, h, resID})
	}

	result := make([]models.SeatWithStatus, len(seats))
	for i, s := range seats {
		status := "free"
		var myResID int64
		for _, r := range reserved[s.ID] {
			conflicts := r.h == "full" || half == "full" || r.h == half
			if !conflicts {
				continue
			}
			if r.uid == userID {
				status = "mine"
				myResID = r.resID
			} else if status != "mine" {
				status = "taken"
			}
		}
		result[i] = models.SeatWithStatus{Seat: s, Status: status, ReservationID: myResID}
	}
	return result, nil
}

// ReserveSeat books a seat for a user. Returns an error if already taken or if the
// user already has a different seat reservation on the same day.
func (d *DB) ReserveSeat(seatID, userID int64, date, half string) error {
	if half == "" {
		half = "full"
	}
	// Check the seat is not already taken for that period.
	var count int
	d.QueryRow(`
		SELECT COUNT(*) FROM seat_reservations
		WHERE seat_id = ? AND date = ? AND (half = ? OR half = 'full' OR ? = 'full')
	`, seatID, date, half, half).Scan(&count)
	if count > 0 {
		return fmt.Errorf("ce siège est déjà réservé pour cette période")
	}
	// Check the user does not already have a reservation on that day (any seat).
	var userCount int
	d.QueryRow(`
		SELECT COUNT(*) FROM seat_reservations
		WHERE user_id = ? AND date = ? AND (half = ? OR half = 'full' OR ? = 'full')
	`, userID, date, half, half).Scan(&userCount)
	if userCount > 0 {
		return fmt.Errorf("vous avez déjà réservé un siège pour cette journée")
	}
	_, err := d.Exec(
		"INSERT INTO seat_reservations (seat_id, user_id, date, half) VALUES (?, ?, ?, ?)",
		seatID, userID, date, half,
	)
	return err
}

// CancelReservation removes a reservation (only the owner can cancel).
func (d *DB) CancelReservation(reservationID, userID int64) error {
	_, err := d.Exec("DELETE FROM seat_reservations WHERE id = ? AND user_id = ?", reservationID, userID)
	return err
}

// GetUserOnSiteStatus checks whether a user has an on-site presence for the given date.
func (d *DB) GetUserOnSiteStatus(userID int64, date string) (bool, error) {
	var count int
	err := d.QueryRow(`
		SELECT COUNT(*) FROM presences p
		JOIN statuses s ON p.status_id = s.id
		WHERE p.user_id = ? AND p.date = ? AND s.on_site = 1
	`, userID, date).Scan(&count)
	return count > 0, err
}

// GetUserReservationDates returns the set of dates within [startDate, endDate] for which
// the user has at least one seat reservation. The map value is always true.
func (d *DB) GetUserReservationDates(userID int64, startDate, endDate string) (map[string]bool, error) {
	rows, err := d.Query(
		`SELECT DISTINCT date FROM seat_reservations WHERE user_id = ? AND date >= ? AND date <= ?`,
		userID, startDate, endDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]bool)
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return nil, err
		}
		m[date] = true
	}
	return m, rows.Err()
}

// BulkReserveSeat attempts to reserve seatID for userID on each of the given dates.
// It silently skips dates where the user is not on-site or the seat is already taken.
// Returns the number of successful bookings.
func (d *DB) BulkReserveSeat(seatID, userID int64, dates []string, half string) int {
	if half == "" {
		half = "full"
	}
	count := 0
	for _, date := range dates {
		isOnSite, _ := d.GetUserOnSiteStatus(userID, date)
		if !isOnSite {
			continue
		}
		if err := d.ReserveSeat(seatID, userID, date, half); err == nil {
			count++
		}
	}
	return count
}

// CancelUserReservationsForDates removes all seat reservations for userID on the given dates.
func (d *DB) CancelUserReservationsForDates(userID int64, dates []string) error {
	if len(dates) == 0 {
		return nil
	}
	placeholders := make([]string, len(dates))
	args := []interface{}{userID}
	for i, date := range dates {
		placeholders[i] = "?"
		args = append(args, date)
	}
	_, err := d.Exec(
		"DELETE FROM seat_reservations WHERE user_id = ? AND date IN ("+strings.Join(placeholders, ",")+")",
		args...,
	)
	return err
}
