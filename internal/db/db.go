package db

import (
"crypto/rand"
"crypto/sha256"
"database/sql"
"encoding/hex"
"fmt"
"log"
"os"
"strings"
"time"

"presence-app/internal/models"

_ "modernc.org/sqlite"
)

// DB holds separate SQLite connections for each domain.
type DB struct {
dataDir   string
core      *sql.DB // users, teams, user_teams, sessions, personal_access_tokens
presence  *sql.DB // statuses, presences, presence_logs, holidays
floorplan *sql.DB // floorplans, seats, seat_reservations
audit     *sql.DB // admin_logs
}

func openSQLite(path string) (*sql.DB, error) {
db, err := sql.Open("sqlite", path)
if err != nil {
return nil, err
}
db.SetMaxOpenConns(10)
db.SetMaxIdleConns(5)
if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
db.Close()
return nil, err
}
if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
db.Close()
return nil, err
}
if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
db.Close()
return nil, err
}
return db, nil
}

// Open opens or creates the 4 domain databases and runs schema migrations.
func Open(dataDir string) (*DB, error) {
coreDB, err := openSQLite(dataDir + "/core.db")
if err != nil {
return nil, fmt.Errorf("open core.db: %w", err)
}
presenceDB, err := openSQLite(dataDir + "/presence.db")
if err != nil {
coreDB.Close()
return nil, fmt.Errorf("open presence.db: %w", err)
}
floorplanDB, err := openSQLite(dataDir + "/floorplan.db")
if err != nil {
coreDB.Close()
presenceDB.Close()
return nil, fmt.Errorf("open floorplan.db: %w", err)
}
auditDB, err := openSQLite(dataDir + "/audit.db")
if err != nil {
coreDB.Close()
presenceDB.Close()
floorplanDB.Close()
return nil, fmt.Errorf("open audit.db: %w", err)
}

d := &DB{
dataDir:   dataDir,
core:      coreDB,
presence:  presenceDB,
floorplan: floorplanDB,
audit:     auditDB,
}

if err := d.migrateCore(); err != nil {
d.Close()
return nil, fmt.Errorf("migrateCore: %w", err)
}
if err := d.migratePresence(); err != nil {
d.Close()
return nil, fmt.Errorf("migratePresence: %w", err)
}
if err := d.migrateFloorplan(); err != nil {
d.Close()
return nil, fmt.Errorf("migrateFloorplan: %w", err)
}
if err := d.migrateAudit(); err != nil {
d.Close()
return nil, fmt.Errorf("migrateAudit: %w", err)
}

// Migrate from legacy single-file app.db once, if present.
legacyPath := dataDir + "/app.db"
if _, err := os.Stat(legacyPath); err == nil {
if err := d.migrateLegacy(legacyPath); err != nil {
log.Printf("WARNING: legacy migration from app.db failed: %v", err)
} else {
if err := os.Rename(legacyPath, legacyPath+".bak"); err != nil {
log.Printf("WARNING: could not rename app.db to app.db.bak: %v", err)
} else {
log.Printf("INFO: app.db migrated to 4 domain databases; renamed to app.db.bak")
}
}
}

return d, nil
}

// Ping checks connectivity to all 4 databases.
func (d *DB) Ping() error {
	if err := d.core.Ping(); err != nil {
		return fmt.Errorf("core.db: %w", err)
	}
	if err := d.presence.Ping(); err != nil {
		return fmt.Errorf("presence.db: %w", err)
	}
	if err := d.floorplan.Ping(); err != nil {
		return fmt.Errorf("floorplan.db: %w", err)
	}
	if err := d.audit.Ping(); err != nil {
		return fmt.Errorf("audit.db: %w", err)
	}
	return nil
}

// Close closes all database connections.
func (d *DB) Close() {
if d.core != nil {
d.core.Close()
}
if d.presence != nil {
d.presence.Close()
}
if d.floorplan != nil {
d.floorplan.Close()
}
if d.audit != nil {
d.audit.Close()
}
}

// --- Schema migrations ---

func (d *DB) migrateCore() error {
_, err := d.core.Exec(`
CREATE TABLE IF NOT EXISTS users (
id INTEGER PRIMARY KEY AUTOINCREMENT,
email TEXT UNIQUE NOT NULL,
name TEXT NOT NULL,
role TEXT NOT NULL DEFAULT 'basic',
password_hash TEXT,
disabled BOOLEAN NOT NULL DEFAULT 0,
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
CREATE TABLE IF NOT EXISTS sessions (
id TEXT PRIMARY KEY,
user_id INTEGER NOT NULL,
expires_at DATETIME NOT NULL,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS personal_access_tokens (
id INTEGER PRIMARY KEY AUTOINCREMENT,
user_id INTEGER NOT NULL,
description TEXT NOT NULL DEFAULT '',
token_hash TEXT NOT NULL UNIQUE,
token_prefix TEXT NOT NULL,
expires_at DATETIME,
last_used_at DATETIME,
created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
`)
if err != nil {
return err
}
d.core.Exec(`UPDATE users SET role = 'global' WHERE role = 'admin'`)
d.core.Exec(`ALTER TABLE users ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT 0`)
d.core.Exec(`UPDATE users SET role = REPLACE(role, 'stats_viewer', 'activity_viewer') WHERE role LIKE '%stats_viewer%'`)
d.core.Exec(`UPDATE users SET role = REPLACE(role, 'cra_viewer', 'activity_viewer') WHERE role LIKE '%cra_viewer%'`)
return nil
}

func (d *DB) migratePresence() error {
_, err := d.presence.Exec(`
CREATE TABLE IF NOT EXISTS statuses (
id INTEGER PRIMARY KEY AUTOINCREMENT,
name TEXT NOT NULL,
color TEXT NOT NULL DEFAULT '#3b82f6',
billable BOOLEAN NOT NULL DEFAULT 0,
on_site BOOLEAN NOT NULL DEFAULT 0,
sort_order INTEGER NOT NULL DEFAULT 0,
created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS presences (
id INTEGER PRIMARY KEY AUTOINCREMENT,
user_id INTEGER NOT NULL,
date TEXT NOT NULL,
half TEXT NOT NULL DEFAULT 'full',
status_id INTEGER NOT NULL,
UNIQUE(user_id, date, half),
FOREIGN KEY (status_id) REFERENCES statuses(id)
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
half TEXT NOT NULL DEFAULT 'full',
status_id INTEGER,
created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`)
if err != nil {
return err
}
d.presence.Exec(`ALTER TABLE statuses ADD COLUMN on_site BOOLEAN NOT NULL DEFAULT 0`)
d.presence.Exec(`ALTER TABLE presence_logs ADD COLUMN half TEXT NOT NULL DEFAULT 'full'`)
var halfColExists int
d.presence.QueryRow("SELECT COUNT(*) FROM pragma_table_info('presences') WHERE name='half'").Scan(&halfColExists)
if halfColExists == 0 {
d.presence.Exec(`CREATE TABLE presences_new (
id INTEGER PRIMARY KEY AUTOINCREMENT,
user_id INTEGER NOT NULL,
date TEXT NOT NULL,
half TEXT NOT NULL DEFAULT 'full',
status_id INTEGER NOT NULL,
UNIQUE(user_id, date, half),
FOREIGN KEY (status_id) REFERENCES statuses(id)
)`)
d.presence.Exec(`INSERT INTO presences_new (id, user_id, date, half, status_id) SELECT id, user_id, date, 'full', status_id FROM presences`)
d.presence.Exec(`DROP TABLE presences`)
d.presence.Exec(`ALTER TABLE presences_new RENAME TO presences`)
}
return nil
}

func (d *DB) migrateFloorplan() error {
_, err := d.floorplan.Exec(`
CREATE TABLE IF NOT EXISTS floorplans (
id INTEGER PRIMARY KEY AUTOINCREMENT,
name TEXT NOT NULL,
image_path TEXT NOT NULL DEFAULT '',
sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS seats (
id INTEGER PRIMARY KEY AUTOINCREMENT,
floorplan_id INTEGER NOT NULL,
label TEXT NOT NULL,
x_pct REAL NOT NULL DEFAULT 0,
y_pct REAL NOT NULL DEFAULT 0,
FOREIGN KEY (floorplan_id) REFERENCES floorplans(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS seat_reservations (
id INTEGER PRIMARY KEY AUTOINCREMENT,
seat_id INTEGER NOT NULL,
user_id INTEGER NOT NULL,
date TEXT NOT NULL,
half TEXT NOT NULL DEFAULT 'full',
created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
UNIQUE(seat_id, date, half),
FOREIGN KEY (seat_id) REFERENCES seats(id) ON DELETE CASCADE
);
`)
return err
}

func (d *DB) migrateAudit() error {
_, err := d.audit.Exec(`
CREATE TABLE IF NOT EXISTS admin_logs (
id INTEGER PRIMARY KEY AUTOINCREMENT,
actor_id INTEGER NOT NULL,
entity_type TEXT NOT NULL,
entity_id INTEGER NOT NULL DEFAULT 0,
action TEXT NOT NULL,
details TEXT NOT NULL DEFAULT '',
created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`)
return err
}

// migrateLegacy copies all data from a legacy single-file app.db to the 4 domain databases.
func (d *DB) migrateLegacy(legacyPath string) error {
legacy, err := sql.Open("sqlite", legacyPath)
if err != nil {
return fmt.Errorf("open legacy: %w", err)
}
defer legacy.Close()
if err := legacy.Ping(); err != nil {
return fmt.Errorf("ping legacy: %w", err)
}

type tableJob struct {
dst       *sql.DB
srcQuery  string
dstInsert string
}

jobs := []tableJob{
{d.core,
"SELECT id, email, name, role, COALESCE(password_hash,''), COALESCE(disabled,0), created_at FROM users",
"INSERT OR IGNORE INTO users (id,email,name,role,password_hash,disabled,created_at) VALUES (?,?,?,?,?,?,?)"},
{d.core,
"SELECT id, name, created_at FROM teams",
"INSERT OR IGNORE INTO teams (id,name,created_at) VALUES (?,?,?)"},
{d.core,
"SELECT user_id, team_id FROM user_teams",
"INSERT OR IGNORE INTO user_teams (user_id,team_id) VALUES (?,?)"},
{d.core,
"SELECT id, user_id, expires_at FROM sessions",
"INSERT OR IGNORE INTO sessions (id,user_id,expires_at) VALUES (?,?,?)"},
{d.core,
"SELECT id, user_id, description, token_hash, token_prefix, expires_at, last_used_at, created_at FROM personal_access_tokens",
"INSERT OR IGNORE INTO personal_access_tokens (id,user_id,description,token_hash,token_prefix,expires_at,last_used_at,created_at) VALUES (?,?,?,?,?,?,?,?)"},
{d.presence,
"SELECT id, name, color, COALESCE(billable,0), COALESCE(on_site,0), COALESCE(sort_order,0), created_at FROM statuses",
"INSERT OR IGNORE INTO statuses (id,name,color,billable,on_site,sort_order,created_at) VALUES (?,?,?,?,?,?,?)"},
{d.presence,
"SELECT id, user_id, date, COALESCE(half,'full'), status_id FROM presences",
"INSERT OR IGNORE INTO presences (id,user_id,date,half,status_id) VALUES (?,?,?,?,?)"},
{d.presence,
"SELECT id, date, name, COALESCE(allow_imputed,0) FROM holidays",
"INSERT OR IGNORE INTO holidays (id,date,name,allow_imputed) VALUES (?,?,?,?)"},
{d.presence,
"SELECT id, user_id, actor_id, action, date, COALESCE(half,'full'), status_id, created_at FROM presence_logs",
"INSERT OR IGNORE INTO presence_logs (id,user_id,actor_id,action,date,half,status_id,created_at) VALUES (?,?,?,?,?,?,?,?)"},
{d.audit,
"SELECT id, actor_id, entity_type, entity_id, action, details, created_at FROM admin_logs",
"INSERT OR IGNORE INTO admin_logs (id,actor_id,entity_type,entity_id,action,details,created_at) VALUES (?,?,?,?,?,?,?)"},
{d.floorplan,
"SELECT id, name, image_path, sort_order FROM floorplans",
"INSERT OR IGNORE INTO floorplans (id,name,image_path,sort_order) VALUES (?,?,?,?)"},
{d.floorplan,
"SELECT id, floorplan_id, label, x_pct, y_pct FROM seats",
"INSERT OR IGNORE INTO seats (id,floorplan_id,label,x_pct,y_pct) VALUES (?,?,?,?,?)"},
{d.floorplan,
"SELECT id, seat_id, user_id, date, COALESCE(half,'full'), created_at FROM seat_reservations",
"INSERT OR IGNORE INTO seat_reservations (id,seat_id,user_id,date,half,created_at) VALUES (?,?,?,?,?,?)"},
}

for _, job := range jobs {
if err := copyLegacyRows(legacy, job.dst, job.srcQuery, job.dstInsert); err != nil {
log.Printf("migrate warning: %v", err)
}
}
return nil
}

// copyLegacyRows copies rows from src to dst. Errors from the source query
// (e.g. tables missing in older schema versions) are silently ignored.
func copyLegacyRows(src, dst *sql.DB, srcQuery, dstInsert string) error {
rows, err := src.Query(srcQuery)
if err != nil {
return nil // table may not exist in older versions
}
defer rows.Close()

cols, err := rows.Columns()
if err != nil {
return err
}

tx, err := dst.Begin()
if err != nil {
return err
}
stmt, err := tx.Prepare(dstInsert)
if err != nil {
tx.Rollback()
return err
}
defer stmt.Close()

vals := make([]interface{}, len(cols))
ptrs := make([]interface{}, len(cols))
for i := range vals {
ptrs[i] = &vals[i]
}

for rows.Next() {
if err := rows.Scan(ptrs...); err != nil {
tx.Rollback()
return err
}
stmt.Exec(vals...) // INSERT OR IGNORE: per-row conflicts are expected and OK
}
if err := rows.Err(); err != nil {
tx.Rollback()
return err
}
return tx.Commit()
}

// SeedDefaults creates the admin user and default statuses if they don't exist.
func (d *DB) SeedDefaults(adminUser, adminPass string) error {
_, err := d.core.Exec(`
INSERT OR IGNORE INTO users (email, name, role, password_hash)
VALUES (?, ?, 'global', ?)
`, adminUser, "Administrateur", adminPass)
if err != nil {
return err
}
_, err = d.core.Exec(`UPDATE users SET role = 'global', password_hash = ? WHERE email = ?`, adminPass, adminUser)
if err != nil {
return err
}

var count int
d.presence.QueryRow("SELECT COUNT(*) FROM statuses").Scan(&count)
if count == 0 {
defaults := []struct {
name     string
color    string
billable bool
onSite   bool
order    int
}{
{"On site", "#22c55e", true, true, 1},
{"Remote work", "#a855f7", true, false, 2},
{"Business trip", "#3b82f6", true, true, 3},
{"Leave", "#f97316", false, false, 4},
{"Sick leave", "#ef4444", false, false, 5},
{"Training", "#eab308", false, false, 6},
{"Absent", "#85888e", false, false, 7},
}
for _, s := range defaults {
_, err := d.presence.Exec(
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

// --- Token helpers ---

func generateToken() (string, error) {
b := make([]byte, 32)
if _, err := rand.Read(b); err != nil {
return "", err
}
return hex.EncodeToString(b), nil
}

// --- Session management ---

func (d *DB) CreateSession(userID int64) (string, error) {
token, err := generateToken()
if err != nil {
return "", err
}
expires := time.Now().Add(24 * time.Hour * 30)
_, err = d.core.Exec("INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)",
token, userID, expires)
if err != nil {
return "", err
}
return token, nil
}

func (d *DB) GetSessionUser(token string) (*models.User, error) {
var u models.User
err := d.core.QueryRow(`
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

func (d *DB) DeleteSession(token string) error {
_, err := d.core.Exec("DELETE FROM sessions WHERE id = ?", token)
return err
}

func (d *DB) CleanExpiredSessions() {
d.core.Exec("DELETE FROM sessions WHERE expires_at < datetime('now')")
}

// --- Personal Access Tokens ---

func (d *DB) CreatePAT(userID int64, description string, expiresAt *time.Time) (string, *models.PersonalAccessToken, error) {
b := make([]byte, 32)
if _, err := rand.Read(b); err != nil {
return "", nil, err
}
raw := "mpa_" + hex.EncodeToString(b)
sum := sha256.Sum256([]byte(raw))
tokenHash := hex.EncodeToString(sum[:])
prefix := raw[:12]

var expiresSQL interface{}
if expiresAt != nil {
expiresSQL = expiresAt.UTC().Format("2006-01-02 15:04:05")
}

result, err := d.core.Exec(
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

func (d *DB) ListUserPATs(userID int64) ([]models.PersonalAccessToken, error) {
rows, err := d.core.Query(`
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

func (d *DB) RevokePAT(id, userID int64) error {
res, err := d.core.Exec(`DELETE FROM personal_access_tokens WHERE id = ? AND user_id = ?`, id, userID)
if err != nil {
return err
}
n, _ := res.RowsAffected()
if n == 0 {
return fmt.Errorf("token not found")
}
return nil
}

func (d *DB) GetUserByPAT(token string) (*models.User, error) {
sum := sha256.Sum256([]byte(token))
tokenHash := hex.EncodeToString(sum[:])

var u models.User
err := d.core.QueryRow(`
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
go d.core.Exec(`UPDATE personal_access_tokens SET last_used_at = datetime('now') WHERE token_hash = ?`, tokenHash) //nolint
return &u, nil
}

// --- User management ---

func (d *DB) GetUserByEmail(email string) (*models.User, error) {
var u models.User
err := d.core.QueryRow(
"SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at FROM users WHERE email = ?",
email,
).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
if err != nil {
return nil, err
}
u.IsLocal = u.PasswordHash != ""
return &u, nil
}

func (d *DB) GetUserByID(id int64) (*models.User, error) {
var u models.User
err := d.core.QueryRow(
"SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at FROM users WHERE id = ?",
id,
).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
if err != nil {
return nil, err
}
u.IsLocal = u.PasswordHash != ""
return &u, nil
}

func (d *DB) UpsertUser(email, name string) (*models.User, error) {
_, err := d.core.Exec(`
INSERT INTO users (email, name, role) VALUES (?, ?, 'basic')
ON CONFLICT(email) DO UPDATE SET name = excluded.name
`, email, name)
if err != nil {
return nil, err
}
return d.GetUserByEmail(email)
}

func (d *DB) ListUsers() ([]models.User, error) {
rows, err := d.core.Query("SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at FROM users ORDER BY name")
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

func (d *DB) UpdateUserRoles(id int64, roles string) error {
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
_, err := d.core.Exec("UPDATE users SET role = ? WHERE id = ?", roles, id)
return err
}

func (d *DB) CreateLocalUser(email, name, password string) error {
_, err := d.core.Exec(
`INSERT INTO users (email, name, role, password_hash) VALUES (?, ?, 'basic', ?)`,
email, name, password,
)
return err
}

func (d *DB) UpdateLocalUser(id int64, email, name string) error {
_, err := d.core.Exec(`UPDATE users SET email = ?, name = ? WHERE id = ?`, email, name, id)
return err
}

func (d *DB) SetUserPassword(id int64, password string) error {
_, err := d.core.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, password, id)
return err
}

func (d *DB) SetUserDisabled(id int64, disabled bool) error {
_, err := d.core.Exec(`UPDATE users SET disabled = ? WHERE id = ?`, disabled, id)
return err
}

func (d *DB) DeleteLocalUser(id int64) error {
_, err := d.core.Exec(`DELETE FROM users WHERE id = ?`, id)
return err
}

// --- Team management ---

func (d *DB) ListTeams() ([]models.Team, error) {
rows, err := d.core.Query("SELECT id, name, created_at FROM teams ORDER BY name")
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

func (d *DB) CreateTeam(name string) error {
_, err := d.core.Exec("INSERT INTO teams (name) VALUES (?)", name)
return err
}

func (d *DB) UpdateTeam(id int64, name string) error {
_, err := d.core.Exec("UPDATE teams SET name = ? WHERE id = ?", name, id)
return err
}

func (d *DB) DeleteTeam(id int64) error {
_, err := d.core.Exec("DELETE FROM teams WHERE id = ?", id)
return err
}

func (d *DB) GetTeamMembers(teamID int64) ([]models.User, error) {
rows, err := d.core.Query(`
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

func (d *DB) AddTeamMember(teamID, userID int64) error {
_, err := d.core.Exec("INSERT OR IGNORE INTO user_teams (team_id, user_id) VALUES (?, ?)", teamID, userID)
return err
}

func (d *DB) RemoveTeamMember(teamID, userID int64) error {
_, err := d.core.Exec("DELETE FROM user_teams WHERE team_id = ? AND user_id = ?", teamID, userID)
return err
}

func (d *DB) GetUserTeams(userID int64) ([]models.Team, error) {
rows, err := d.core.Query(`
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

func (d *DB) ListStatuses() ([]models.Status, error) {
rows, err := d.presence.Query("SELECT id, name, color, billable, on_site, sort_order FROM statuses ORDER BY sort_order, id")
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

func (d *DB) CreateStatus(s models.Status) error {
_, err := d.presence.Exec(
"INSERT INTO statuses (name, color, billable, on_site, sort_order) VALUES (?, ?, ?, ?, ?)",
s.Name, s.Color, s.Billable, s.OnSite, s.SortOrder,
)
return err
}

func (d *DB) UpdateStatus(s models.Status) error {
_, err := d.presence.Exec(
"UPDATE statuses SET name = ?, color = ?, billable = ?, on_site = ?, sort_order = ? WHERE id = ?",
s.Name, s.Color, s.Billable, s.OnSite, s.SortOrder, s.ID,
)
return err
}

func (d *DB) DeleteStatus(id int64) error {
_, err := d.presence.Exec("DELETE FROM statuses WHERE id = ?", id)
return err
}

// --- Presence management ---

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

rows, err := d.presence.Query(query, args...)
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

func (d *DB) SetPresences(userID int64, dates []string, statusID int64, half string) error {
if half == "" {
half = "full"
}
if half != "full" && half != "AM" && half != "PM" {
return fmt.Errorf("invalid half value: %s", half)
}
tx, err := d.presence.Begin()
if err != nil {
return err
}
defer tx.Rollback()

for _, date := range dates {
if half == "full" {
if _, err := tx.Exec("DELETE FROM presences WHERE user_id = ? AND date = ? AND half IN ('AM', 'PM')", userID, date); err != nil {
return err
}
} else {
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
query = fmt.Sprintf("DELETE FROM presences WHERE user_id = ? AND date IN (%s)", datePlaceholders)
args = make([]interface{}, 0, 1+len(dates))
args = append(args, userID)
for _, dt := range dates {
args = append(args, dt)
}
} else {
query = fmt.Sprintf("DELETE FROM presences WHERE user_id = ? AND half = ? AND date IN (%s)", datePlaceholders)
args = make([]interface{}, 0, 2+len(dates))
args = append(args, userID, half)
for _, dt := range dates {
args = append(args, dt)
}
}
_, err := d.presence.Exec(query, args...)
return err
}

// --- Stats ---

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

func (d *DB) ListHolidays() ([]models.Holiday, error) {
rows, err := d.presence.Query("SELECT id, date, name, allow_imputed FROM holidays ORDER BY date")
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

func (d *DB) GetHolidayMap(startDate, endDate string) (map[string]models.Holiday, error) {
rows, err := d.presence.Query(
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

func (d *DB) CreateHoliday(date, name string, allowImputed bool) error {
_, err := d.presence.Exec(
"INSERT INTO holidays (date, name, allow_imputed) VALUES (?, ?, ?)",
date, name, allowImputed,
)
return err
}

func (d *DB) UpdateHoliday(id int64, date, name string, allowImputed bool) error {
_, err := d.presence.Exec(
"UPDATE holidays SET date = ?, name = ?, allow_imputed = ? WHERE id = ?",
date, name, allowImputed, id,
)
return err
}

func (d *DB) DeleteHoliday(id int64) error {
_, err := d.presence.Exec("DELETE FROM holidays WHERE id = ?", id)
return err
}

// --- Presence logs ---

func (d *DB) LogPresenceAction(actorID, userID int64, action string, dates []string, statusID int64, half string) error {
if half == "" {
half = "full"
}
tx, err := d.presence.Begin()
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
// Actor names are resolved via a batch query to core.db.
func (d *DB) GetUserLogs(userID int64, since time.Time) ([]models.PresenceLog, error) {
query := `
SELECT pl.id, pl.user_id, pl.actor_id,
       pl.action, pl.date, pl.half,
       COALESCE(pl.status_id, 0), COALESCE(s.name, ''), COALESCE(s.color, ''),
       pl.created_at
FROM presence_logs pl
LEFT JOIN statuses s ON pl.status_id = s.id
WHERE pl.user_id = ?`
args := []interface{}{userID}
if !since.IsZero() {
query += " AND pl.created_at >= ?"
args = append(args, since)
}
query += " ORDER BY pl.created_at DESC LIMIT 1000"

rows, err := d.presence.Query(query, args...)
if err != nil {
return nil, err
}
defer rows.Close()

var logs []models.PresenceLog
actorIDs := make(map[int64]struct{})
for rows.Next() {
var l models.PresenceLog
if err := rows.Scan(
&l.ID, &l.UserID, &l.ActorID,
&l.Action, &l.Date, &l.Half,
&l.StatusID, &l.StatusName, &l.StatusColor,
&l.CreatedAt,
); err != nil {
return nil, err
}
logs = append(logs, l)
actorIDs[l.ActorID] = struct{}{}
}
if err := rows.Err(); err != nil {
return nil, err
}

names := d.fetchUserNames(actorIDs)
for i := range logs {
logs[i].ActorName = names[logs[i].ActorID]
}
return logs, nil
}

// --- Name lookup helpers ---

func (d *DB) GetTeamName(id int64) string {
var name string
d.core.QueryRow("SELECT name FROM teams WHERE id = ?", id).Scan(&name)
return name
}

func (d *DB) GetStatusName(id int64) string {
var name string
d.presence.QueryRow("SELECT name FROM statuses WHERE id = ?", id).Scan(&name)
return name
}

func (d *DB) GetHolidayName(id int64) string {
var name string
d.presence.QueryRow("SELECT name FROM holidays WHERE id = ?", id).Scan(&name)
return name
}

// fetchUserNames batch-fetches user names from core.db.
func (d *DB) fetchUserNames(ids map[int64]struct{}) map[int64]string {
if len(ids) == 0 {
return nil
}
placeholders := make([]string, 0, len(ids))
args := make([]interface{}, 0, len(ids))
for id := range ids {
placeholders = append(placeholders, "?")
args = append(args, id)
}
rows, err := d.core.Query(
"SELECT id, name FROM users WHERE id IN ("+strings.Join(placeholders, ",")+")",
args...,
)
if err != nil {
return nil
}
defer rows.Close()
names := make(map[int64]string)
for rows.Next() {
var id int64
var name string
rows.Scan(&id, &name)
names[id] = name
}
return names
}

// fetchTeamNames batch-fetches team names from core.db.
func (d *DB) fetchTeamNames(ids map[int64]struct{}) map[int64]string {
if len(ids) == 0 {
return nil
}
placeholders := make([]string, 0, len(ids))
args := make([]interface{}, 0, len(ids))
for id := range ids {
placeholders = append(placeholders, "?")
args = append(args, id)
}
rows, err := d.core.Query(
"SELECT id, name FROM teams WHERE id IN ("+strings.Join(placeholders, ",")+")",
args...,
)
if err != nil {
return nil
}
defer rows.Close()
names := make(map[int64]string)
for rows.Next() {
var id int64
var name string
rows.Scan(&id, &name)
names[id] = name
}
return names
}

// fetchStatusNames batch-fetches status names from presence.db.
func (d *DB) fetchStatusNames(ids map[int64]struct{}) map[int64]string {
if len(ids) == 0 {
return nil
}
placeholders := make([]string, 0, len(ids))
args := make([]interface{}, 0, len(ids))
for id := range ids {
placeholders = append(placeholders, "?")
args = append(args, id)
}
rows, err := d.presence.Query(
"SELECT id, name FROM statuses WHERE id IN ("+strings.Join(placeholders, ",")+")",
args...,
)
if err != nil {
return nil
}
defer rows.Close()
names := make(map[int64]string)
for rows.Next() {
var id int64
var name string
rows.Scan(&id, &name)
names[id] = name
}
return names
}

// fetchHolidayNames batch-fetches holiday names from presence.db.
func (d *DB) fetchHolidayNames(ids map[int64]struct{}) map[int64]string {
if len(ids) == 0 {
return nil
}
placeholders := make([]string, 0, len(ids))
args := make([]interface{}, 0, len(ids))
for id := range ids {
placeholders = append(placeholders, "?")
args = append(args, id)
}
rows, err := d.presence.Query(
"SELECT id, name FROM holidays WHERE id IN ("+strings.Join(placeholders, ",")+")",
args...,
)
if err != nil {
return nil
}
defer rows.Close()
names := make(map[int64]string)
for rows.Next() {
var id int64
var name string
rows.Scan(&id, &name)
names[id] = name
}
return names
}

// --- Admin logging ---

func (d *DB) LogAdminAction(actorID int64, entityType string, entityID int64, action, details string) {
d.audit.Exec(
"INSERT INTO admin_logs (actor_id, entity_type, entity_id, action, details) VALUES (?, ?, ?, ?, ?)",
actorID, entityType, entityID, action, details,
)
}

// GetAdminLogsByActor returns admin logs for a user. Entity and actor names are
// resolved via batch queries to core.db and presence.db.
func (d *DB) GetAdminLogsByActor(actorID int64, since time.Time) ([]models.AdminLog, error) {
query := `
SELECT id, actor_id, entity_type, entity_id, action, details, created_at
FROM admin_logs WHERE actor_id = ?`
args := []interface{}{actorID}
if !since.IsZero() {
query += " AND created_at >= ?"
args = append(args, since)
}
query += " ORDER BY created_at DESC LIMIT 1000"

rows, err := d.audit.Query(query, args...)
if err != nil {
return nil, err
}
defer rows.Close()

var logs []models.AdminLog
teamIDs := make(map[int64]struct{})
statusIDs := make(map[int64]struct{})
holidayIDs := make(map[int64]struct{})
userEntityIDs := make(map[int64]struct{})
actorIDs := make(map[int64]struct{})

for rows.Next() {
var l models.AdminLog
if err := rows.Scan(&l.ID, &l.ActorID, &l.EntityType, &l.EntityID, &l.Action, &l.Details, &l.CreatedAt); err != nil {
return nil, err
}
logs = append(logs, l)
actorIDs[l.ActorID] = struct{}{}
switch l.EntityType {
case "team":
teamIDs[l.EntityID] = struct{}{}
case "status":
statusIDs[l.EntityID] = struct{}{}
case "holiday":
holidayIDs[l.EntityID] = struct{}{}
case "user":
if l.EntityID > 0 {
userEntityIDs[l.EntityID] = struct{}{}
}
}
}
if err := rows.Err(); err != nil {
return nil, err
}

actorNames := d.fetchUserNames(actorIDs)
teamNames := d.fetchTeamNames(teamIDs)
statusNames := d.fetchStatusNames(statusIDs)
holidayNames := d.fetchHolidayNames(holidayIDs)
userNames := d.fetchUserNames(userEntityIDs)

for i, l := range logs {
logs[i].ActorName = actorNames[l.ActorID]
switch l.EntityType {
case "team":
logs[i].EntityName = teamNames[l.EntityID]
case "status":
logs[i].EntityName = statusNames[l.EntityID]
case "holiday":
logs[i].EntityName = holidayNames[l.EntityID]
case "user":
if l.EntityID > 0 {
logs[i].EntityName = userNames[l.EntityID]
}
}
}
return logs, nil
}

// --- Floorplan management ---

func (d *DB) ListFloorplans() ([]models.Floorplan, error) {
rows, err := d.floorplan.Query("SELECT id, name, image_path, sort_order FROM floorplans ORDER BY sort_order, id")
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

func (d *DB) GetFloorplan(id int64) (*models.Floorplan, error) {
var f models.Floorplan
err := d.floorplan.QueryRow("SELECT id, name, image_path, sort_order FROM floorplans WHERE id = ?", id).
Scan(&f.ID, &f.Name, &f.ImagePath, &f.SortOrder)
if err != nil {
return nil, err
}
return &f, nil
}

func (d *DB) CreateFloorplan(name string, sortOrder int) (int64, error) {
res, err := d.floorplan.Exec("INSERT INTO floorplans (name, sort_order) VALUES (?, ?)", name, sortOrder)
if err != nil {
return 0, err
}
return res.LastInsertId()
}

func (d *DB) UpdateFloorplan(id int64, name string, sortOrder int) error {
_, err := d.floorplan.Exec("UPDATE floorplans SET name = ?, sort_order = ? WHERE id = ?", name, sortOrder, id)
return err
}

func (d *DB) SetFloorplanImage(id int64, imagePath string) error {
_, err := d.floorplan.Exec("UPDATE floorplans SET image_path = ? WHERE id = ?", imagePath, id)
return err
}

func (d *DB) DeleteFloorplan(id int64) error {
_, err := d.floorplan.Exec("DELETE FROM floorplans WHERE id = ?", id)
return err
}

func (d *DB) ListSeats(floorplanID int64) ([]models.Seat, error) {
rows, err := d.floorplan.Query("SELECT id, floorplan_id, label, x_pct, y_pct FROM seats WHERE floorplan_id = ? ORDER BY id", floorplanID)
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

func (d *DB) CreateSeat(floorplanID int64, label string, xPct, yPct float64) (int64, error) {
res, err := d.floorplan.Exec("INSERT INTO seats (floorplan_id, label, x_pct, y_pct) VALUES (?, ?, ?, ?)", floorplanID, label, xPct, yPct)
if err != nil {
return 0, err
}
return res.LastInsertId()
}

func (d *DB) UpdateSeat(id int64, label string, xPct, yPct float64) error {
_, err := d.floorplan.Exec("UPDATE seats SET label = ?, x_pct = ?, y_pct = ? WHERE id = ?", label, xPct, yPct, id)
return err
}

func (d *DB) DeleteSeat(id int64) error {
_, err := d.floorplan.Exec("DELETE FROM seats WHERE id = ?", id)
return err
}

func (d *DB) GetSeatsWithStatus(floorplanID, userID int64, date, half string) ([]models.SeatWithStatus, error) {
seats, err := d.ListSeats(floorplanID)
if err != nil {
return nil, err
}

rows, err := d.floorplan.Query(`
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

func (d *DB) ReserveSeat(seatID, userID int64, date, half string) error {
if half == "" {
half = "full"
}
var count int
d.floorplan.QueryRow(`
SELECT COUNT(*) FROM seat_reservations
WHERE seat_id = ? AND date = ? AND (half = ? OR half = 'full' OR ? = 'full')
`, seatID, date, half, half).Scan(&count)
if count > 0 {
return fmt.Errorf("ce siège est déjà réservé pour cette période")
}
var userCount int
d.floorplan.QueryRow(`
SELECT COUNT(*) FROM seat_reservations
WHERE user_id = ? AND date = ? AND (half = ? OR half = 'full' OR ? = 'full')
`, userID, date, half, half).Scan(&userCount)
if userCount > 0 {
return fmt.Errorf("vous avez déjà réservé un siège pour cette journée")
}
_, err := d.floorplan.Exec(
"INSERT INTO seat_reservations (seat_id, user_id, date, half) VALUES (?, ?, ?, ?)",
seatID, userID, date, half,
)
return err
}

func (d *DB) CancelReservation(reservationID, userID int64) error {
_, err := d.floorplan.Exec("DELETE FROM seat_reservations WHERE id = ? AND user_id = ?", reservationID, userID)
return err
}

func (d *DB) GetUserOnSiteStatus(userID int64, date string) (bool, error) {
var count int
err := d.presence.QueryRow(`
SELECT COUNT(*) FROM presences p
JOIN statuses s ON p.status_id = s.id
WHERE p.user_id = ? AND p.date = ? AND s.on_site = 1
`, userID, date).Scan(&count)
return count > 0, err
}

func (d *DB) GetUserReservationDates(userID int64, startDate, endDate string) (map[string]bool, error) {
rows, err := d.floorplan.Query(
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
_, err := d.floorplan.Exec(
"DELETE FROM seat_reservations WHERE user_id = ? AND date IN ("+strings.Join(placeholders, ",")+")",
args...,
)
return err
}