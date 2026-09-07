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

	"golang.org/x/crypto/bcrypt"

	"github.com/matoy/mypresence/internal/config"
	"github.com/matoy/mypresence/internal/models"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
	_ "modernc.org/sqlite"
)

// DB holds separate connections for each domain (all point to the same *sql.DB
// when using a network backend; separate SQLite files when using SQLite).
type DB struct {
	dataDir    string
	driver     string    // "sqlite", "postgres", "mysql", "sqlserver"
	dialect    dialect   // SQL dialect helpers
	shared     *sql.DB   // non-nil when using a single shared connection (network DBs)
	core       *rebindDB // users, teams, user_teams, sessions, personal_access_tokens
	presence   *rebindDB // statuses, presences, presence_logs, holidays
	floorplan  *rebindDB // floorplans, seats, seat_reservations
	audit      *rebindDB // admin_logs
	projects   *rebindDB // projects, project_time_entries
	bcryptCost int       // OWASP recommends ≥12; lowered to bcrypt.MinCost in tests
}

// openSQLiteConn opens a single SQLite file with WAL mode and foreign keys.
func openSQLiteConn(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Open opens or creates the databases and runs schema migrations.
// For SQLite (default) it uses 5 separate domain files in dataDir.
// For other drivers (postgres, mysql, sqlserver) it uses a single shared connection.
func Open(cfg *config.Config) (*DB, error) {
	driver := strings.ToLower(cfg.DBDriver)
	if driver == "" {
		driver = "sqlite"
	}

	dl := newDialect(driver)

	switch driver {
	case "postgres", "mysql", "sqlserver":
		return openNetwork(cfg, driver, dl)
	default:
		return openSQLiteMulti(cfg.DataDir, dl)
	}
}

// openSQLiteMulti opens the legacy 5-file SQLite layout.
func openSQLiteMulti(dataDir string, dl dialect) (*DB, error) {
	coreDB, err := openSQLiteConn(dataDir + "/core.db")
	if err != nil {
		return nil, fmt.Errorf("open core.db: %w", err)
	}
	presenceDB, err := openSQLiteConn(dataDir + "/presence.db")
	if err != nil {
		_ = coreDB.Close()
		return nil, fmt.Errorf("open presence.db: %w", err)
	}
	floorplanDB, err := openSQLiteConn(dataDir + "/floorplan.db")
	if err != nil {
		_ = coreDB.Close()
		_ = presenceDB.Close()
		return nil, fmt.Errorf("open floorplan.db: %w", err)
	}
	auditDB, err := openSQLiteConn(dataDir + "/audit.db")
	if err != nil {
		_ = coreDB.Close()
		_ = presenceDB.Close()
		_ = floorplanDB.Close()
		return nil, fmt.Errorf("open audit.db: %w", err)
	}
	projectsDB, err := openSQLiteConn(dataDir + "/projects.db")
	if err != nil {
		_ = coreDB.Close()
		_ = presenceDB.Close()
		_ = floorplanDB.Close()
		_ = auditDB.Close()
		return nil, fmt.Errorf("open projects.db: %w", err)
	}

	d := &DB{
		dataDir:    dataDir,
		driver:     "sqlite",
		dialect:    dl,
		core:       newRebindDB(coreDB, dl),
		presence:   newRebindDB(presenceDB, dl),
		floorplan:  newRebindDB(floorplanDB, dl),
		audit:      newRebindDB(auditDB, dl),
		projects:   newRebindDB(projectsDB, dl),
		bcryptCost: 12,
	}

	if err := d.migrate(); err != nil {
		d.Close()
		return nil, err
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

// openNetwork opens a single shared connection to a network database backend
// (PostgreSQL, MySQL/MariaDB, SQL Server) and wires all domain fields to it.
func openNetwork(cfg *config.Config, driver string, dl dialect) (*DB, error) {
	dsn, err := buildDSN(cfg, driver)
	if err != nil {
		return nil, err
	}

	conn, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", driver, err)
	}
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)

	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping %s: %w", driver, err)
	}

	wrapped := newRebindDB(conn, dl)
	d := &DB{
		driver:     driver,
		dialect:    dl,
		shared:     conn,
		core:       wrapped,
		presence:   wrapped,
		floorplan:  wrapped,
		audit:      wrapped,
		projects:   wrapped,
		bcryptCost: 12,
	}

	if err := d.migrate(); err != nil {
		d.Close()
		return nil, err
	}

	return d, nil
}

// buildDSN constructs the connection string for the given driver from config.
func buildDSN(cfg *config.Config, driver string) (string, error) {
	host := cfg.DBHost
	name := cfg.DBName
	user := cfg.DBUser
	pass := cfg.DBPassword
	sslMode := cfg.DBSSLMode
	if sslMode == "" {
		sslMode = "disable"
	}

	switch driver {
	case "postgres":
		port := cfg.DBPort
		if port == "" {
			port = "5432"
		}
		return fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=%s",
			host, port, name, user, pass, sslMode), nil
	case "mysql":
		port := cfg.DBPort
		if port == "" {
			port = "3306"
		}
		tls := "false"
		switch sslMode {
		case "require", "verify-full":
			tls = "true"
		case "skip-verify":
			tls = "skip-verify"
		}
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&tls=%s&charset=utf8mb4",
			user, pass, host, port, name, tls), nil
	case "sqlserver":
		port := cfg.DBPort
		if port == "" {
			port = "1433"
		}
		return fmt.Sprintf("sqlserver://%s:%s@%s:%s?database=%s",
			user, pass, host, port, name), nil
	default:
		return "", fmt.Errorf("unsupported driver: %s", driver)
	}
}

// migrate runs all schema migrations on every domain DB.
func (d *DB) migrate() error {
	if err := d.migrateCore(); err != nil {
		return fmt.Errorf("migrateCore: %w", err)
	}
	if err := d.migratePresence(); err != nil {
		return fmt.Errorf("migratePresence: %w", err)
	}
	if err := d.migrateFloorplan(); err != nil {
		return fmt.Errorf("migrateFloorplan: %w", err)
	}
	if err := d.migrateAudit(); err != nil {
		return fmt.Errorf("migrateAudit: %w", err)
	}
	if err := d.migrateProjects(); err != nil {
		return fmt.Errorf("migrateProjects: %w", err)
	}
	if err := d.migrateNews(); err != nil {
		return fmt.Errorf("migrateNews: %w", err)
	}
	if err := d.migrateCertifications(); err != nil {
		return fmt.Errorf("migrateCertifications: %w", err)
	}
	if err := d.migrateProjectCertifications(); err != nil {
		return fmt.Errorf("migrateProjectCertifications: %w", err)
	}
	if err := d.migrateNotifications(); err != nil {
		return fmt.Errorf("migrateNotifications: %w", err)
	}
	return nil
}

// Ping checks connectivity to all databases.
func (d *DB) Ping() error {
	if d.shared != nil {
		return d.shared.Ping()
	}
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
	if err := d.projects.Ping(); err != nil {
		return fmt.Errorf("projects.db: %w", err)
	}
	return nil
}

// SetBcryptCost overrides the bcrypt work factor. Use bcrypt.MinCost (4) in
// tests to avoid spending seconds per user creation on CI runners.
func (d *DB) SetBcryptCost(cost int) { d.bcryptCost = cost }

// DBCounts holds point-in-time record counts from all databases.
type DBCounts struct {
	Users                  int64
	ActiveSessions         int64
	Teams                  int64
	Statuses               int64
	Presences              int64
	Floorplans             int64
	Seats                  int64
	Projects               int64
	ProjectEntries         int64
	Sites                  int64
	SitesCorporate         int64
	SitesNonCorporate      int64
	SiteWorkstations       int64
	SeatReservations       int64
	ReservationsToday      int64
	ActiveProjects         int64
	ProjectMembers         int64
	ProjectActivities      int64
	ProjectActivitiesJira  int64
	ProjectActivitiesSN    int64
	ProjectActivitiesOther int64
	PresenceCertifications int64
	ProjectCertifications  int64
	Domains                int64
	ActivePATs             int64
	Passkeys               int64
	Notifications          int64
	News                   int64
}

// Counts queries lightweight COUNT(*) rows from each database.
// Errors are silently ignored; missing tables return 0.
func (d *DB) Counts() DBCounts {
	var c DBCounts
	today := time.Now().Format("2006-01-02")

	// core DB
	d.core.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&c.Users)                                                                     //nolint:errcheck
	d.core.QueryRow(d.dialect.rebind(`SELECT COUNT(*) FROM sessions WHERE expires_at > ` + d.dialect.now())).Scan(&c.ActiveSessions) //nolint:errcheck
	d.core.QueryRow(`SELECT COUNT(*) FROM teams`).Scan(&c.Teams)                                                                     //nolint:errcheck
	d.core.QueryRow(`SELECT COUNT(*) FROM domains`).Scan(&c.Domains)                                                                 //nolint:errcheck
	d.core.QueryRow(d.dialect.rebind(`SELECT COUNT(*) FROM personal_access_tokens WHERE expires_at IS NULL OR expires_at > ` + d.dialect.now())).Scan(&c.ActivePATs) //nolint:errcheck
	d.core.QueryRow(`SELECT COUNT(*) FROM webauthn_credentials`).Scan(&c.Passkeys)                                                   //nolint:errcheck
	d.core.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&c.Notifications)                                                     //nolint:errcheck
	d.core.QueryRow(`SELECT COUNT(*) FROM news`).Scan(&c.News)                                                                       //nolint:errcheck

	// presence DB
	d.presence.QueryRow(`SELECT COUNT(*) FROM statuses`).Scan(&c.Statuses)                                                           //nolint:errcheck
	d.presence.QueryRow(`SELECT COUNT(*) FROM presences`).Scan(&c.Presences)                                                         //nolint:errcheck
	d.presence.QueryRow(`SELECT COUNT(*) FROM declaration_certifications`).Scan(&c.PresenceCertifications)                          //nolint:errcheck
	d.presence.QueryRow(`SELECT COUNT(*) FROM project_certifications`).Scan(&c.ProjectCertifications)                              //nolint:errcheck

	// floorplan DB
	d.floorplan.QueryRow(`SELECT COUNT(*) FROM floorplans`).Scan(&c.Floorplans)                                                      //nolint:errcheck
	d.floorplan.QueryRow(`SELECT COUNT(*) FROM seats`).Scan(&c.Seats)                                                                //nolint:errcheck
	d.floorplan.QueryRow(`SELECT COUNT(*) FROM sites`).Scan(&c.Sites)                                                                //nolint:errcheck
	d.floorplan.QueryRow(d.dialect.rebind(`SELECT COUNT(*) FROM sites WHERE not_corporate_site = ? OR not_corporate_site IS NULL`), false).Scan(&c.SitesCorporate) //nolint:errcheck
	d.floorplan.QueryRow(d.dialect.rebind(`SELECT COUNT(*) FROM sites WHERE not_corporate_site = ?`), true).Scan(&c.SitesNonCorporate)                             //nolint:errcheck
	d.floorplan.QueryRow(`SELECT COALESCE(SUM(seats), 0) FROM sites`).Scan(&c.SiteWorkstations)                                      //nolint:errcheck
	d.floorplan.QueryRow(`SELECT COUNT(*) FROM seat_reservations`).Scan(&c.SeatReservations)                                          //nolint:errcheck
	d.floorplan.QueryRow(d.dialect.rebind(`SELECT COUNT(*) FROM seat_reservations WHERE date = ?`), today).Scan(&c.ReservationsToday) //nolint:errcheck

	// projects DB
	d.projects.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&c.Projects)                                                           //nolint:errcheck
	d.projects.QueryRow(d.dialect.rebind(`SELECT COUNT(*) FROM projects WHERE active = ?`), true).Scan(&c.ActiveProjects)           //nolint:errcheck
	d.projects.QueryRow(`SELECT COUNT(*) FROM project_members`).Scan(&c.ProjectMembers)                                              //nolint:errcheck
	d.projects.QueryRow(`SELECT COUNT(*) FROM project_time_entries`).Scan(&c.ProjectEntries)                                         //nolint:errcheck
	d.projects.QueryRow(`SELECT COUNT(*) FROM project_activities`).Scan(&c.ProjectActivities)                                        //nolint:errcheck
	d.projects.QueryRow(`SELECT COUNT(*) FROM project_activities WHERE activity_type = 'jira'`).Scan(&c.ProjectActivitiesJira)       //nolint:errcheck
	d.projects.QueryRow(`SELECT COUNT(*) FROM project_activities WHERE activity_type = 'servicenow'`).Scan(&c.ProjectActivitiesSN)  //nolint:errcheck
	d.projects.QueryRow(`SELECT COUNT(*) FROM project_activities WHERE activity_type = 'other'`).Scan(&c.ProjectActivitiesOther)   //nolint:errcheck
	return c
}

// Close closes all database connections.
func (d *DB) Close() {
	// In shared mode (network backends) all domain fields point to the same connection.
	if d.shared != nil {
		_ = d.shared.Close()
		return
	}
	if d.core != nil {
		_ = d.core.Close()
	}
	if d.presence != nil {
		_ = d.presence.Close()
	}
	if d.floorplan != nil {
		_ = d.floorplan.Close()
	}
	if d.audit != nil {
		_ = d.audit.Close()
	}
	if d.projects != nil {
		_ = d.projects.Close()
	}
}

// --- Schema migrations ---

func (d *DB) migrateCore() error {
	dl := d.dialect
	ai := dl.autoincrement()
	bool_ := dl.boolType()
	dt := dl.datetimeType()
	text := dl.textType()
	// For SQLite use TEXT; for others use VARCHAR for indexed/unique columns
	emailType := "TEXT"
	if !dl.isSQLite() {
		emailType = dl.varcharType(255)
	}
	nameType := "TEXT"
	if !dl.isSQLite() {
		nameType = dl.varcharType(255)
	}
	// Credential IDs are base64url-encoded raw bytes; typically ≤128 chars for modern authenticators.
	credIDType := dl.varcharType(256)
	if dl.isSQLite() {
		credIDType = "TEXT"
	}

	stmts := []string{
		dl.createTableIfNotExists("users", fmt.Sprintf(`
id %s,
email %s UNIQUE NOT NULL,
name %s NOT NULL,
role %s NOT NULL DEFAULT 'basic',
password_hash %s,
disabled %s NOT NULL DEFAULT %s,
site_id BIGINT NOT NULL DEFAULT 0,
created_at %s DEFAULT CURRENT_TIMESTAMP
`, ai, emailType, nameType, dl.varcharType(128), text, bool_, dl.boolDefault(false), dt)),

		dl.createTableIfNotExists("teams", fmt.Sprintf(`
id %s,
name %s UNIQUE NOT NULL,
created_at %s DEFAULT CURRENT_TIMESTAMP
`, ai, nameType, dt)),

		dl.createTableIfNotExists("user_teams", `
user_id BIGINT NOT NULL,
team_id BIGINT NOT NULL,
left_at VARCHAR(10) DEFAULT NULL,
PRIMARY KEY (user_id, team_id),
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
FOREIGN KEY (team_id) REFERENCES teams(id) ON DELETE CASCADE
`),

		dl.createTableIfNotExists("team_leaders", `
team_id BIGINT NOT NULL,
user_id BIGINT NOT NULL,
PRIMARY KEY (team_id, user_id),
FOREIGN KEY (team_id) REFERENCES teams(id) ON DELETE CASCADE,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
`),

		// domains group several teams under a shared manager for aggregated
		// activity reporting.
		dl.createTableIfNotExists("domains", fmt.Sprintf(`
id %s,
name %s UNIQUE NOT NULL,
created_at %s DEFAULT CURRENT_TIMESTAMP
`, ai, nameType, dt)),

		dl.createTableIfNotExists("domain_managers", `
domain_id BIGINT NOT NULL,
user_id BIGINT NOT NULL,
PRIMARY KEY (domain_id, user_id),
FOREIGN KEY (domain_id) REFERENCES domains(id) ON DELETE CASCADE,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
`),

		dl.createTableIfNotExists("sessions", fmt.Sprintf(`
id %s PRIMARY KEY,
user_id BIGINT NOT NULL,
expires_at %s NOT NULL,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
`, dl.varcharType(64), dt)),

		dl.createTableIfNotExists("personal_access_tokens", fmt.Sprintf(`
id %s,
user_id BIGINT NOT NULL,
description %s NOT NULL DEFAULT '',
token_hash %s NOT NULL UNIQUE,
token_prefix %s NOT NULL,
expires_at %s,
last_used_at %s,
created_at %s DEFAULT CURRENT_TIMESTAMP,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
`, ai, text, dl.varcharType(64), dl.varcharType(16), dt, dt, dt)),

		dl.createTableIfNotExists("password_reset_tokens", fmt.Sprintf(`
id %s,
user_id BIGINT NOT NULL,
token_hash %s NOT NULL UNIQUE,
expires_at %s NOT NULL,
created_at %s DEFAULT CURRENT_TIMESTAMP,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
`, ai, dl.varcharType(64), dt, dt)),

		dl.createTableIfNotExists("webauthn_credentials", fmt.Sprintf(`
id %s NOT NULL PRIMARY KEY,
user_id BIGINT NOT NULL,
name %s NOT NULL DEFAULT '',
credential_json %s NOT NULL,
created_at %s DEFAULT CURRENT_TIMESTAMP,
last_used_at %s,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
`, credIDType, dl.varcharType(128), text, dt, dt)),
	}

	for _, stmt := range stmts {
		if _, err := d.core.Exec(dl.rebind(stmt)); err != nil {
			return err
		}
	}

	// Additive migrations (safe to run multiple times — errors ignored)
	d.core.Exec(`UPDATE users SET role = 'global' WHERE role = 'admin'`)                                                                                         //nolint:errcheck
	d.core.Exec(dl.rebind(dl.addColumnIfNotExists("users", "disabled", fmt.Sprintf("%s NOT NULL DEFAULT %s", bool_, dl.boolDefault(false)))))                    //nolint:errcheck
	d.core.Exec(dl.rebind(dl.addColumnIfNotExists("users", "site_id", "BIGINT NOT NULL DEFAULT 0")))                                                            //nolint:errcheck
	d.core.Exec(`UPDATE users SET role = REPLACE(role, 'stats_viewer', 'activity_viewer') WHERE role LIKE '%stats_viewer%'`)                                     //nolint:errcheck
	d.core.Exec(`UPDATE users SET role = REPLACE(role, 'cra_viewer', 'activity_viewer') WHERE role LIKE '%cra_viewer%'`)                                         //nolint:errcheck
	d.core.Exec(`UPDATE users SET role = REPLACE(role, 'projects_admin', 'projects_manager') WHERE role LIKE '%projects_admin%'`)                                //nolint:errcheck
	d.core.Exec(dl.rebind(dl.modifyColumnType("users", "role", dl.varcharType(128), "VARCHAR(64)")))                                                             //nolint:errcheck
	d.core.Exec(dl.rebind(dl.addColumnIfNotExists("user_teams", "left_at", dl.varcharType(10)+" DEFAULT NULL")))                                                 //nolint:errcheck
	d.core.Exec(dl.rebind(dl.addColumnIfNotExists("teams", "jira_space_key", nameType+" DEFAULT ''")))                                                           //nolint:errcheck
	d.core.Exec(dl.rebind(dl.addColumnIfNotExists("teams", "timesheets_managed_manually", fmt.Sprintf("%s NOT NULL DEFAULT %s", bool_, dl.boolDefault(false))))) //nolint:errcheck
	d.core.Exec(dl.rebind(dl.addColumnIfNotExists("teams", "require_activity_comment", fmt.Sprintf("%s NOT NULL DEFAULT %s", bool_, dl.boolDefault(false)))))    //nolint:errcheck
	d.core.Exec(dl.rebind(dl.addColumnIfNotExists("teams", "domain_id", "BIGINT NOT NULL DEFAULT 0")))                                                           //nolint:errcheck
	d.core.Exec(dl.rebind(dl.addColumnIfNotExists("teams", "country_codes", dl.varcharType(255)+" NOT NULL DEFAULT ''")))                                        //nolint:errcheck

	// Migrate legacy team_leader role users into team_leaders table and clean up users.role
	if rows, err := d.core.Query(`SELECT u.id, ut.team_id FROM users u JOIN user_teams ut ON u.id = ut.user_id WHERE u.role LIKE '%team_leader%'`); err == nil {
		type legacyLeader struct {
			userID, teamID int64
		}
		var legacy []legacyLeader
		for rows.Next() {
			var l legacyLeader
			if err := rows.Scan(&l.userID, &l.teamID); err == nil {
				legacy = append(legacy, l)
			}
		}
		rows.Close() //nolint:errcheck
		for _, l := range legacy {
			_, _ = d.core.Exec(dl.rebind(dl.insertOrIgnore("team_leaders", []string{"team_id", "user_id"}, "?, ?")), l.teamID, l.userID)
		}
		if userRows, err := d.core.Query(`SELECT id, role FROM users WHERE role LIKE '%team_leader%'`); err == nil {
			type userRole struct {
				id   int64
				role string
			}
			var uRoles []userRole
			for userRows.Next() {
				var ur userRole
				if err := userRows.Scan(&ur.id, &ur.role); err == nil {
					uRoles = append(uRoles, ur)
				}
			}
			userRows.Close() //nolint:errcheck
			for _, ur := range uRoles {
				var newRoles []string
				for _, r := range strings.Split(ur.role, ",") {
					r = strings.TrimSpace(r)
					if r != "" && r != "team_leader" {
						newRoles = append(newRoles, r)
					}
				}
				newRoleStr := strings.Join(newRoles, ",")
				if newRoleStr == "" {
					newRoleStr = "basic"
				}
				_, _ = d.core.Exec("UPDATE users SET role = ? WHERE id = ?", newRoleStr, ur.id)
			}
		}
	}

	return nil
}

func (d *DB) migratePresence() error {
	dl := d.dialect
	ai := dl.autoincrement()
	bool_ := dl.boolType()
	dt := dl.datetimeType()

	stmts := []string{
		dl.createTableIfNotExists("statuses", fmt.Sprintf(`
id %s,
name %s NOT NULL,
color %s NOT NULL DEFAULT '#3b82f6',
billable %s NOT NULL DEFAULT %s,
on_site %s NOT NULL DEFAULT %s,
sort_order INTEGER NOT NULL DEFAULT 0,
disabled %s NOT NULL DEFAULT %s,
created_at %s DEFAULT CURRENT_TIMESTAMP
`, ai, dl.varcharType(128), dl.varcharType(16), bool_, dl.boolDefault(false), bool_, dl.boolDefault(false), bool_, dl.boolDefault(false), dt)),

		dl.createTableIfNotExists("presences", fmt.Sprintf(`
id %s,
user_id BIGINT NOT NULL,
date %s NOT NULL,
half %s NOT NULL DEFAULT 'full',
status_id BIGINT NOT NULL,
UNIQUE(user_id, date, half),
FOREIGN KEY (status_id) REFERENCES statuses(id)
`, ai, dl.varcharType(10), dl.varcharType(4))),

		dl.createTableIfNotExists("holidays", fmt.Sprintf(`
id %s,
date %s UNIQUE NOT NULL,
name %s NOT NULL,
allow_imputed %s NOT NULL DEFAULT %s
`, ai, dl.varcharType(10), dl.varcharType(128), bool_, dl.boolDefault(false))),

		dl.createTableIfNotExists("presence_logs", fmt.Sprintf(`
id %s,
user_id BIGINT NOT NULL,
actor_id BIGINT NOT NULL,
action %s NOT NULL,
date %s NOT NULL,
half %s NOT NULL DEFAULT 'full',
status_id BIGINT,
created_at %s DEFAULT CURRENT_TIMESTAMP
`, ai, dl.varcharType(16), dl.varcharType(10), dl.varcharType(4), dt)),
	}

	for _, stmt := range stmts {
		if _, err := d.presence.Exec(dl.rebind(stmt)); err != nil {
			return err
		}
	}

	// Additive migrations
	d.presence.Exec(dl.rebind(dl.addColumnIfNotExists("statuses", "on_site", fmt.Sprintf("%s NOT NULL DEFAULT %s", bool_, dl.boolDefault(false))))) //nolint:errcheck
	d.presence.Exec(dl.rebind(dl.addColumnIfNotExists("statuses", "disabled", fmt.Sprintf("%s DEFAULT %s", bool_, dl.boolDefault(false)))))         //nolint:errcheck
	d.presence.Exec(dl.rebind(dl.addColumnIfNotExists("presence_logs", "half", fmt.Sprintf("%s NOT NULL DEFAULT 'full'", dl.varcharType(4)))))      //nolint:errcheck
	d.presence.Exec(dl.rebind(dl.addColumnIfNotExists("holidays", "country_code", dl.varcharType(10)+" NOT NULL DEFAULT ''")))                      //nolint:errcheck

	// Backfill on_site = true for common on-site presence status names if on_site was defaulted to false
	d.presence.Exec(dl.rebind(`UPDATE statuses SET on_site = ? WHERE (LOWER(name) LIKE '%on site%' OR LOWER(name) LIKE '%sur site%' OR LOWER(name) LIKE '%bureau%' OR LOWER(name) LIKE '%présent%' OR LOWER(name) LIKE '%present%' OR LOWER(name) LIKE '%office%') AND (on_site = ? OR on_site IS NULL)`), true, false) //nolint:errcheck

	// SQLite-only migration: recreate presences table if 'half' column is missing
	// (Not needed for network databases which always get the full schema above)
	if dl.isSQLite() {
		var halfColExists int
		d.presence.QueryRow("SELECT COUNT(*) FROM pragma_table_info('presences') WHERE name='half'").Scan(&halfColExists) //nolint:errcheck
		if halfColExists == 0 {
			_, _ = d.presence.Exec(`CREATE TABLE presences_new (
id INTEGER PRIMARY KEY AUTOINCREMENT,
user_id INTEGER NOT NULL,
date TEXT NOT NULL,
half TEXT NOT NULL DEFAULT 'full',
status_id INTEGER NOT NULL,
UNIQUE(user_id, date, half),
FOREIGN KEY (status_id) REFERENCES statuses(id)
)`)
			_, _ = d.presence.Exec(`INSERT INTO presences_new (id, user_id, date, half, status_id) SELECT id, user_id, date, 'full', status_id FROM presences`)
			_, _ = d.presence.Exec(`DROP TABLE presences`)
			_, _ = d.presence.Exec(`ALTER TABLE presences_new RENAME TO presences`)
		}
	}
	return nil
}

func (d *DB) migrateFloorplan() error {
	dl := d.dialect
	ai := dl.autoincrement()
	real_ := dl.realType()
	dt := dl.datetimeType()
	bool_ := dl.boolType()

	stmts := []string{
		dl.createTableIfNotExists("sites", fmt.Sprintf(`
id %s,
name %s NOT NULL,
country_code %s NOT NULL DEFAULT '',
not_corporate_site %s NOT NULL DEFAULT %s,
seats INTEGER NOT NULL DEFAULT 0,
created_at %s DEFAULT CURRENT_TIMESTAMP
`, ai, dl.varcharType(128), dl.varcharType(64), bool_, dl.boolDefault(false), dt)),

		dl.createTableIfNotExists("floorplans", fmt.Sprintf(`
id %s,
name %s NOT NULL,
site_id BIGINT NOT NULL DEFAULT 0,
image_path %s NOT NULL DEFAULT '',
sort_order INTEGER NOT NULL DEFAULT 0
`, ai, dl.varcharType(128), dl.varcharType(255))),

		dl.createTableIfNotExists("seats", fmt.Sprintf(`
id %s,
floorplan_id BIGINT NOT NULL,
label %s NOT NULL,
x_pct %s NOT NULL DEFAULT 0,
y_pct %s NOT NULL DEFAULT 0,
FOREIGN KEY (floorplan_id) REFERENCES floorplans(id) ON DELETE CASCADE
`, ai, dl.varcharType(64), real_, real_)),

		dl.createTableIfNotExists("seat_reservations", fmt.Sprintf(`
id %s,
seat_id BIGINT NOT NULL,
user_id BIGINT NOT NULL,
date %s NOT NULL,
half %s NOT NULL DEFAULT 'full',
created_at %s DEFAULT CURRENT_TIMESTAMP,
UNIQUE(seat_id, date, half),
FOREIGN KEY (seat_id) REFERENCES seats(id) ON DELETE CASCADE
`, ai, dl.varcharType(10), dl.varcharType(4), dt)),

		dl.createTableIfNotExists("floorplan_favorites", `
floorplan_id BIGINT NOT NULL,
user_id      BIGINT NOT NULL,
UNIQUE(floorplan_id, user_id),
FOREIGN KEY (floorplan_id) REFERENCES floorplans(id) ON DELETE CASCADE
`),
	}

	for _, stmt := range stmts {
		if stmt == "" {
			continue
		}
		if _, err := d.floorplan.Exec(dl.rebind(stmt)); err != nil {
			return err
		}
	}

	// Additive migrations
	d.floorplan.Exec(dl.rebind(dl.addColumnIfNotExists("sites", "country_code", dl.varcharType(64)+" NOT NULL DEFAULT ''")))                                    //nolint:errcheck
	d.floorplan.Exec(dl.rebind(dl.addColumnIfNotExists("sites", "not_corporate_site", fmt.Sprintf("%s NOT NULL DEFAULT %s", bool_, dl.boolDefault(false))))) //nolint:errcheck
	d.floorplan.Exec(dl.rebind(dl.addColumnIfNotExists("sites", "seats", "INTEGER NOT NULL DEFAULT 0")))                                                      //nolint:errcheck
	d.floorplan.Exec(dl.rebind("UPDATE sites SET seats = workstations WHERE seats = 0 AND workstations > 0"))                                                 //nolint:errcheck
	d.floorplan.Exec(dl.rebind(dl.addColumnIfNotExists("floorplans", "site_id", "BIGINT NOT NULL DEFAULT 0")))                                              //nolint:errcheck

	return nil
}

func (d *DB) migrateAudit() error {
	dl := d.dialect
	ai := dl.autoincrement()
	dt := dl.datetimeType()

	stmt := dl.createTableIfNotExists("admin_logs", fmt.Sprintf(`
id %s,
actor_id BIGINT NOT NULL,
entity_type %s NOT NULL,
entity_id BIGINT NOT NULL DEFAULT 0,
action %s NOT NULL,
details %s NOT NULL DEFAULT '',
created_at %s DEFAULT CURRENT_TIMESTAMP
`, ai, dl.varcharType(32), dl.varcharType(32), dl.textType(), dt))

	_, err := d.audit.Exec(dl.rebind(stmt))
	return err
}

// migrateLegacy copies all data from a legacy single-file app.db to the 4 domain databases.
func (d *DB) migrateLegacy(legacyPath string) error {
	legacy, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		return fmt.Errorf("open legacy: %w", err)
	}
	defer legacy.Close() //nolint:errcheck
	if err := legacy.Ping(); err != nil {
		return fmt.Errorf("ping legacy: %w", err)
	}

	type tableJob struct {
		dst       *rebindDB
		srcQuery  string
		dstInsert string
	}

	userSrc := "SELECT id, email, name, role, COALESCE(password_hash,''), COALESCE(disabled,0), COALESCE(site_id,0), created_at FROM users"
	userDst := "INSERT OR IGNORE INTO users (id,email,name,role,password_hash,disabled,site_id,created_at) VALUES (?,?,?,?,?,?,?,?)"
	var hasSiteID int
	_ = legacy.QueryRow("SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='site_id'").Scan(&hasSiteID)
	if hasSiteID == 0 {
		userSrc = "SELECT id, email, name, role, COALESCE(password_hash,''), COALESCE(disabled,0), created_at FROM users"
		userDst = "INSERT OR IGNORE INTO users (id,email,name,role,password_hash,disabled,created_at) VALUES (?,?,?,?,?,?,?)"
	}

	jobs := []tableJob{
		{d.core, userSrc, userDst},
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
		if err := copyLegacyRows(legacy, job.dst.DB, job.srcQuery, job.dstInsert); err != nil {
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
	defer rows.Close() //nolint:errcheck

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
		tx.Rollback() //nolint:errcheck
		return err
	}
	defer stmt.Close() //nolint:errcheck

	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			tx.Rollback() //nolint:errcheck
			return err
		}
		stmt.Exec(vals...) //nolint:errcheck // INSERT OR IGNORE: per-row conflicts are expected and OK
	}
	if err := rows.Err(); err != nil {
		tx.Rollback() //nolint:errcheck
		return err
	}
	return tx.Commit()
}

// SeedDefaults creates the admin user and default statuses if they don't exist.
func (d *DB) SeedDefaults(adminUser, adminPass string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(adminPass), d.bcryptCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	hashedPass := string(hash)
	_, err = d.core.Exec(d.dialect.rebind(d.dialect.insertOrIgnore(
		"users",
		[]string{"email", "name", "role", "password_hash"},
		"?, ?, 'global', ?",
	)), adminUser, "Administrator", hashedPass)
	if err != nil {
		return err
	}
	// Only update the hash if the stored value is not already a bcrypt hash,
	// to avoid an expensive rehash on every startup.
	var stored string
	d.core.QueryRow(d.dialect.rebind("SELECT COALESCE(password_hash,'') FROM users WHERE email = ?"), adminUser).Scan(&stored) //nolint:errcheck
	if !strings.HasPrefix(stored, "$2") {
		_, err = d.core.Exec(d.dialect.rebind(`UPDATE users SET role = 'global', password_hash = ? WHERE email = ?`), hashedPass, adminUser)
		if err != nil {
			return err
		}
	} else {
		// Ensure the role is correct even if hash is already bcrypt
		_, err = d.core.Exec(d.dialect.rebind(`UPDATE users SET role = 'global' WHERE email = ?`), adminUser)
		if err != nil {
			return err
		}
	}

	var count int
	d.presence.QueryRow("SELECT COUNT(*) FROM statuses").Scan(&count) //nolint:errcheck
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
				d.dialect.rebind("INSERT INTO statuses (name, color, billable, on_site, sort_order) VALUES (?, ?, ?, ?, ?)"),
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
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	expires := time.Now().Add(24 * time.Hour * 30)
	_, err = d.core.Exec("INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)",
		tokenHash, userID, expires)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (d *DB) GetSessionUser(token string) (*models.User, error) {
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	var u models.User
	err := d.core.QueryRow(d.dialect.rebind(`
SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at
FROM sessions s JOIN users u ON s.user_id = u.id
WHERE s.id = ? AND s.expires_at > `+d.dialect.now()+` AND u.disabled = `+d.dialect.boolDefault(false)+`
`), tokenHash).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsLocal = u.PasswordHash != ""
	return &u, nil
}

func (d *DB) DeleteSession(token string) error {
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	_, err := d.core.Exec("DELETE FROM sessions WHERE id = ?", tokenHash)
	return err
}

func (d *DB) CleanExpiredSessions() {
	d.core.Exec(d.dialect.rebind("DELETE FROM sessions WHERE expires_at < " + d.dialect.now())) //nolint:errcheck
}

// DeleteUserSessions deletes all active sessions for a user.
// Pass exceptTokenRaw="" to delete all sessions (e.g. after a password reset via email).
// Pass the current raw session token to keep the caller's own session alive.
func (d *DB) DeleteUserSessions(userID int64, exceptTokenRaw string) {
	if exceptTokenRaw == "" {
		d.core.Exec("DELETE FROM sessions WHERE user_id = ?", userID) //nolint:errcheck
		return
	}
	sum := sha256.Sum256([]byte(exceptTokenRaw))
	exceptHash := hex.EncodeToString(sum[:])
	d.core.Exec("DELETE FROM sessions WHERE user_id = ? AND id != ?", userID, exceptHash) //nolint:errcheck
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

	id, err := d.core.InsertGetID(
		`INSERT INTO personal_access_tokens (user_id, description, token_hash, token_prefix, expires_at) VALUES (?, ?, ?, ?, ?)`,
		userID, description, tokenHash, prefix, expiresSQL,
	)
	if err != nil {
		return "", nil, err
	}
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
	defer rows.Close() //nolint:errcheck

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

// AdminRevokePAT deletes any token by ID regardless of owner (global admin action).
func (d *DB) AdminRevokePAT(id int64) error {
	res, err := d.core.Exec(`DELETE FROM personal_access_tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("token not found")
	}
	return nil
}

// AdminPAT is a PersonalAccessToken enriched with the owner's display name.
type AdminPAT struct {
	models.PersonalAccessToken
	UserName string `json:"user_name"`
}

// ListAllPATs returns all personal access tokens across all users, joined with user name.
func (d *DB) ListAllPATs() ([]AdminPAT, error) {
	rows, err := d.core.Query(`
SELECT t.id, t.user_id, t.description, t.token_prefix, t.expires_at, t.last_used_at, t.created_at, u.name
FROM personal_access_tokens t
JOIN users u ON t.user_id = u.id
ORDER BY t.created_at DESC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var pats []AdminPAT
	for rows.Next() {
		var p AdminPAT
		var expiresAt, lastUsedAt sql.NullTime
		if err := rows.Scan(&p.ID, &p.UserID, &p.Description, &p.TokenPrefix, &expiresAt, &lastUsedAt, &p.CreatedAt, &p.UserName); err != nil {
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

func (d *DB) GetUserByPAT(token string) (*models.User, error) {
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])

	var u models.User
	err := d.core.QueryRow(d.dialect.rebind(`
SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at
FROM personal_access_tokens t
JOIN users u ON t.user_id = u.id
WHERE t.token_hash = ?
  AND (t.expires_at IS NULL OR t.expires_at > `+d.dialect.now()+`)
  AND u.disabled = `+d.dialect.boolDefault(false)+`
`), tokenHash).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.IsLocal = u.PasswordHash != ""
	go d.core.Exec(d.dialect.rebind(`UPDATE personal_access_tokens SET last_used_at = `+d.dialect.now()+` WHERE token_hash = ?`), tokenHash) //nolint
	return &u, nil
}

// --- Password reset tokens ---

// CreatePasswordResetToken generates a secure one-time token for the given email.
// Returns the raw (unhashed) token to be sent by email.
// Returns ("", nil) silently if no local account with that email exists (don't reveal existence).
func (d *DB) CreatePasswordResetToken(email string) (string, error) {
	u, err := d.GetUserByEmail(email)
	if err != nil || u.PasswordHash == "" {
		// No local account — return empty silently to avoid user enumeration
		return "", nil
	}

	// Delete any existing token for this user
	d.core.Exec(`DELETE FROM password_reset_tokens WHERE user_id = ?`, u.ID) //nolint:errcheck

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])

	expires := time.Now().Add(time.Hour)
	_, err = d.core.Exec(
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at) VALUES (?, ?, ?)`,
		u.ID, hash, expires.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return "", err
	}
	return raw, nil
}

// UsePasswordResetToken validates the token, marks it used (deleted), and returns the target user.
// Returns an error if the token is invalid or expired.
func (d *DB) UsePasswordResetToken(rawToken string) (*models.User, error) {
	sum := sha256.Sum256([]byte(rawToken))
	hash := hex.EncodeToString(sum[:])

	var userID int64
	var expiresAt time.Time
	err := d.core.QueryRow(
		`SELECT user_id, expires_at FROM password_reset_tokens WHERE token_hash = ?`, hash,
	).Scan(&userID, &expiresAt)
	if err != nil {
		return nil, fmt.Errorf("invalid or unknown token")
	}
	if time.Now().After(expiresAt) {
		d.core.Exec(`DELETE FROM password_reset_tokens WHERE token_hash = ?`, hash) //nolint:errcheck
		return nil, fmt.Errorf("token expired")
	}

	// Consume token immediately
	d.core.Exec(`DELETE FROM password_reset_tokens WHERE token_hash = ?`, hash) //nolint:errcheck

	return d.GetUserByID(userID)
}

// CleanExpiredResetTokens removes expired password reset tokens.
func (d *DB) CleanExpiredResetTokens() {
	d.core.Exec(d.dialect.rebind(`DELETE FROM password_reset_tokens WHERE expires_at < ` + d.dialect.now())) //nolint:errcheck
}

// --- User management ---

func (d *DB) GetUserByEmail(email string) (*models.User, error) {
	var u models.User
	err := d.core.QueryRow(
		"SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at, COALESCE(site_id, 0) FROM users WHERE email = ?",
		email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.SiteID)
	if err != nil {
		return nil, err
	}
	u.IsLocal = u.PasswordHash != ""
	if u.SiteID > 0 {
		if s, sErr := d.GetSite(u.SiteID); sErr == nil && s != nil {
			u.SiteName = s.Name
			u.SiteCountryCode = s.CountryCode
		}
	}
	return &u, nil
}

func (d *DB) GetUserByID(id int64) (*models.User, error) {
	var u models.User
	err := d.core.QueryRow(
		"SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at, COALESCE(site_id, 0) FROM users WHERE id = ?",
		id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.SiteID)
	if err != nil {
		return nil, err
	}
	u.IsLocal = u.PasswordHash != ""
	if u.SiteID > 0 {
		if s, sErr := d.GetSite(u.SiteID); sErr == nil && s != nil {
			u.SiteName = s.Name
			u.SiteCountryCode = s.CountryCode
		}
	}
	return &u, nil
}

func (d *DB) UpsertUser(email, name string) (*models.User, error) {
	var stmt string
	switch d.driver {
	case "mysql":
		stmt = `INSERT INTO users (email, name, role) VALUES (?, ?, 'basic') ON DUPLICATE KEY UPDATE name = VALUES(name)`
	case "sqlserver":
		stmt = `MERGE INTO users AS target USING (SELECT ? AS email, ? AS name) AS source ON (target.email = source.email) WHEN MATCHED THEN UPDATE SET target.name = source.name WHEN NOT MATCHED THEN INSERT (email, name, role) VALUES (source.email, source.name, 'basic');`
	default: // sqlite, postgres
		stmt = `INSERT INTO users (email, name, role) VALUES (?, ?, 'basic') ON CONFLICT(email) DO UPDATE SET name = excluded.name`
	}
	_, err := d.core.Exec(d.dialect.rebind(stmt), email, name)
	if err != nil {
		return nil, err
	}
	return d.GetUserByEmail(email)
}

func (d *DB) ListUsers() ([]models.User, error) {
	rows, err := d.core.Query("SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at, COALESCE(site_id, 0) FROM users ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.SiteID); err != nil {
			return nil, err
		}
		u.IsLocal = u.PasswordHash != ""
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d.HydrateUsersSites(users), nil
}

// UpdateUserSite updates a user's assigned site.
func (d *DB) UpdateUserSite(userID, siteID int64) error {
	_, err := d.core.Exec("UPDATE users SET site_id = ? WHERE id = ?", siteID, userID)
	return err
}

// GetUsersBySite returns all users assigned to a site.
func (d *DB) GetUsersBySite(siteID int64) ([]models.User, error) {
	rows, err := d.core.Query(d.dialect.rebind("SELECT id, email, name, role, COALESCE(password_hash,''), disabled, created_at, COALESCE(site_id, 0) FROM users WHERE site_id = ? ORDER BY name"), siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.SiteID); err != nil {
			return nil, err
		}
		u.IsLocal = u.PasswordHash != ""
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d.HydrateUsersSites(users), nil
}

// HydrateUsersSites populates SiteName and SiteCountryCode on the given users using ListSites.
func (d *DB) HydrateUsersSites(users []models.User) []models.User {
	sites, err := d.ListSites()
	if err != nil || len(sites) == 0 {
		return users
	}
	siteMap := make(map[int64]*models.Site, len(sites))
	for _, s := range sites {
		siteMap[s.ID] = s
	}
	for i := range users {
		if s, ok := siteMap[users[i].SiteID]; ok && s != nil {
			users[i].SiteName = s.Name
			users[i].SiteCountryCode = s.CountryCode
		}
	}
	return users
}

// HydrateTeamMembersSites populates SiteName and SiteCountryCode on the given team members using ListSites.
func (d *DB) HydrateTeamMembersSites(members []models.TeamMember) []models.TeamMember {
	sites, err := d.ListSites()
	if err != nil || len(sites) == 0 {
		return members
	}
	siteMap := make(map[int64]*models.Site, len(sites))
	for _, s := range sites {
		siteMap[s.ID] = s
	}
	for i := range members {
		if s, ok := siteMap[members[i].SiteID]; ok && s != nil {
			members[i].SiteName = s.Name
			members[i].SiteCountryCode = s.CountryCode
		}
	}
	return members
}

func (d *DB) UpdateUserRoles(id int64, roles string) error {
	valid := map[string]bool{
		models.RoleBasic: true, models.RoleTeamManager: true,
		models.RoleStatusManager: true,
		models.RoleActivityViewer: true, models.RoleFloorplanManager: true,
		models.RoleProjectsManager: true, models.RoleProjectsViewer: true,
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

func (d *DB) CreateLocalUser(email, name, password string) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), d.bcryptCost)
	if err != nil {
		return 0, fmt.Errorf("hash password: %w", err)
	}
	return d.core.InsertGetID(
		`INSERT INTO users (email, name, role, password_hash) VALUES (?, ?, 'basic', ?)`,
		email, name, string(hash),
	)
}

// CheckPassword compares a submitted plaintext password against a stored hash.
// It supports bcrypt hashes (primary) and plain-text legacy hashes (migration path).
// When a plain-text match is found the password is automatically rehashed with bcrypt.
func (d *DB) CheckPassword(userID int64, storedHash, plainPassword string) bool {
	if storedHash == "" || plainPassword == "" {
		return false
	}
	// Bcrypt hashes always start with $2
	if strings.HasPrefix(storedHash, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(plainPassword)) == nil
	}
	// Legacy plain-text comparison — rehash automatically on successful match
	if storedHash == plainPassword {
		if hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), d.bcryptCost); err == nil {
			d.core.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(hash), userID) //nolint:errcheck
		}
		return true
	}
	return false
}

func (d *DB) UpdateLocalUser(id int64, email, name string) error {
	_, err := d.core.Exec(`UPDATE users SET email = ?, name = ? WHERE id = ?`, email, name, id)
	return err
}

func (d *DB) SetUserPassword(id int64, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), d.bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	_, err = d.core.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), id)
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

// sanitizeCountryCodes normalizes a comma-separated list of country codes (e.g. "fr, ma , US" -> "FR,MA,US").
func sanitizeCountryCodes(codes string) string {
	if strings.TrimSpace(codes) == "" {
		return ""
	}
	var res []string
	seen := make(map[string]bool)
	for _, c := range strings.Split(codes, ",") {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c != "" && !seen[c] {
			seen[c] = true
			res = append(res, c)
		}
	}
	return strings.Join(res, ",")
}

// --- Team management ---

func (d *DB) ListTeams() ([]models.Team, error) {
	rows, err := d.core.Query("SELECT id, name, COALESCE(jira_space_key,''), timesheets_managed_manually, COALESCE(require_activity_comment, false), domain_id, COALESCE(country_codes, ''), created_at FROM teams ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var teams []models.Team
	for rows.Next() {
		var t models.Team
		if err := rows.Scan(&t.ID, &t.Name, &t.JiraSpaceKey, &t.TimesheetsManagedManually, &t.RequireActivityComment, &t.DomainID, &t.CountryCodes, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// GetTeam returns a team by its ID.
func (d *DB) GetTeam(id int64) (*models.Team, error) {
	var t models.Team
	err := d.core.QueryRow(
		"SELECT id, name, COALESCE(jira_space_key,''), timesheets_managed_manually, COALESCE(require_activity_comment, false), domain_id, COALESCE(country_codes, ''), created_at FROM teams WHERE id = ?",
		id,
	).Scan(&t.ID, &t.Name, &t.JiraSpaceKey, &t.TimesheetsManagedManually, &t.RequireActivityComment, &t.DomainID, &t.CountryCodes, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTeam creates a new team with just a name (jira_space_key empty, manual timesheets off, require comment off).
func (d *DB) CreateTeam(name string) (int64, error) {
	return d.CreateTeamWithDetails(name, "", false, false)
}

// CreateTeamWithDetails creates a new team with all its properties including optional country codes.
func (d *DB) CreateTeamWithDetails(name, jiraSpaceKey string, timesheetsManagedManually, requireActivityComment bool, countryCodes ...string) (int64, error) {
	cc := ""
	if len(countryCodes) > 0 {
		cc = sanitizeCountryCodes(countryCodes[0])
	}
	return d.core.InsertGetID("INSERT INTO teams (name, jira_space_key, timesheets_managed_manually, require_activity_comment, country_codes) VALUES (?, ?, ?, ?, ?)", name, jiraSpaceKey, timesheetsManagedManually, requireActivityComment, cc)
}

// UpdateTeam renames a team, leaving its other properties unchanged.
func (d *DB) UpdateTeam(id int64, name string) error {
	_, err := d.core.Exec("UPDATE teams SET name = ? WHERE id = ?", name, id)
	return err
}

// UpdateTeamDetails updates a team's name, extra properties, and optional country codes.
func (d *DB) UpdateTeamDetails(id int64, name, jiraSpaceKey string, timesheetsManagedManually, requireActivityComment bool, countryCodes ...string) error {
	if len(countryCodes) > 0 {
		cc := sanitizeCountryCodes(countryCodes[0])
		_, err := d.core.Exec("UPDATE teams SET name = ?, jira_space_key = ?, timesheets_managed_manually = ?, require_activity_comment = ?, country_codes = ? WHERE id = ?", name, jiraSpaceKey, timesheetsManagedManually, requireActivityComment, cc, id)
		return err
	}
	_, err := d.core.Exec("UPDATE teams SET name = ?, jira_space_key = ?, timesheets_managed_manually = ?, require_activity_comment = ? WHERE id = ?", name, jiraSpaceKey, timesheetsManagedManually, requireActivityComment, id)
	return err
}

// UpdateTeamDomain sets (or clears, with domainID=0) the domain a team is attached to.
func (d *DB) UpdateTeamDomain(teamID, domainID int64) error {
	_, err := d.core.Exec("UPDATE teams SET domain_id = ? WHERE id = ?", domainID, teamID)
	return err
}

func (d *DB) DeleteTeam(id int64) error {
	_, err := d.core.Exec("DELETE FROM teams WHERE id = ?", id)
	return err
}

// GetTeamMembers returns currently active members of a team (left_at IS NULL).
func (d *DB) GetTeamMembers(teamID int64) ([]models.User, error) {
	rows, err := d.core.Query(`
SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at, COALESCE(u.site_id, 0)
FROM users u
JOIN user_teams ut ON u.id = ut.user_id
WHERE ut.team_id = ? AND ut.left_at IS NULL
ORDER BY u.name
`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.SiteID); err != nil {
			return nil, err
		}
		u.IsLocal = u.PasswordHash != ""
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d.HydrateUsersSites(users), nil
}

// GetAllTeamMembers returns all members of a team (active and departed), active first.
func (d *DB) GetAllTeamMembers(teamID int64) ([]models.TeamMember, error) {
	rows, err := d.core.Query(`
SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at, COALESCE(u.site_id, 0), ut.left_at
FROM users u
JOIN user_teams ut ON u.id = ut.user_id
WHERE ut.team_id = ?
ORDER BY CASE WHEN ut.left_at IS NULL THEN 0 ELSE 1 END, u.name
`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var members []models.TeamMember
	for rows.Next() {
		var m models.TeamMember
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.Roles, &m.PasswordHash, &m.Disabled, &m.CreatedAt, &m.SiteID, &m.LeftAt); err != nil {
			return nil, err
		}
		m.IsLocal = m.PasswordHash != ""
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d.HydrateTeamMembersSites(members), nil
}

// GetTeamMembersAt returns members who were active at any point from startDate onwards
// (left_at IS NULL, meaning still active, or left_at >= startDate, meaning they left during or after the period).
func (d *DB) GetTeamMembersAt(teamID int64, startDate string) ([]models.User, error) {
	rows, err := d.core.Query(`
SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at, COALESCE(u.site_id, 0)
FROM users u
JOIN user_teams ut ON u.id = ut.user_id
WHERE ut.team_id = ? AND (ut.left_at IS NULL OR ut.left_at >= ?)
ORDER BY u.name
`, teamID, startDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.SiteID); err != nil {
			return nil, err
		}
		u.IsLocal = u.PasswordHash != ""
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d.HydrateUsersSites(users), nil
}

// SetTeamMemberLeftAt sets or clears the departure date for a team member.
// Pass nil to reinstate (clear left_at).
func (d *DB) SetTeamMemberLeftAt(teamID, userID int64, leftAt *string) error {
	_, err := d.core.Exec("UPDATE user_teams SET left_at = ? WHERE team_id = ? AND user_id = ?", leftAt, teamID, userID)
	return err
}

func (d *DB) AddTeamMember(teamID, userID int64) error {
	d.core.Exec(d.dialect.rebind(d.dialect.insertOrIgnore( //nolint:errcheck
		"user_teams",
		[]string{"team_id", "user_id"},
		"?, ?",
	)), teamID, userID)
	// Reset departure date in case the member is being re-added after leaving.
	_, err := d.core.Exec("UPDATE user_teams SET left_at = NULL WHERE team_id = ? AND user_id = ?", teamID, userID)
	return err
}

func (d *DB) RemoveTeamMember(teamID, userID int64) error {
	_, err := d.core.Exec("DELETE FROM user_teams WHERE team_id = ? AND user_id = ?", teamID, userID)
	return err
}

func (d *DB) GetUserTeams(userID int64) ([]models.Team, error) {
	rows, err := d.core.Query(`
SELECT t.id, t.name, COALESCE(t.jira_space_key,''), t.timesheets_managed_manually, COALESCE(t.require_activity_comment, false), t.domain_id, COALESCE(t.country_codes, ''), t.created_at
FROM teams t
JOIN user_teams ut ON t.id = ut.team_id
WHERE ut.user_id = ? AND ut.left_at IS NULL
ORDER BY t.name
`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var teams []models.Team
	for rows.Next() {
		var t models.Team
		if err := rows.Scan(&t.ID, &t.Name, &t.JiraSpaceKey, &t.TimesheetsManagedManually, &t.RequireActivityComment, &t.DomainID, &t.CountryCodes, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// ─── Team leaders ─────────────────────────────────────────────────────────────

// GetTeamLeaderIDs returns the user IDs explicitly designated as leaders of a team.
func (d *DB) GetTeamLeaderIDs(teamID int64) ([]int64, error) {
	rows, err := d.core.Query(`SELECT user_id FROM team_leaders WHERE team_id = ? ORDER BY user_id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListTeamLeaders returns the users designated as leaders of a team.
func (d *DB) ListTeamLeaders(teamID int64) ([]models.User, error) {
	rows, err := d.core.Query(`
SELECT u.id, u.email, u.name, u.role, COALESCE(u.password_hash,''), u.disabled, u.created_at, COALESCE(u.site_id, 0)
FROM users u
JOIN team_leaders tl ON u.id = tl.user_id
WHERE tl.team_id = ?
ORDER BY u.name
`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Roles, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.SiteID); err != nil {
			return nil, err
		}
		u.IsLocal = u.PasswordHash != ""
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d.HydrateUsersSites(users), nil
}

// SetTeamLeaders atomically replaces the full leader list of a team.
func (d *DB) SetTeamLeaders(teamID int64, userIDs []int64) error {
	tx, err := d.core.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM team_leaders WHERE team_id = ?`, teamID); err != nil {
		return err
	}
	for _, uid := range userIDs {
		if _, err := tx.Exec(d.dialect.rebind(d.dialect.insertOrIgnore(
			"team_leaders",
			[]string{"team_id", "user_id"},
			"?, ?",
		)), teamID, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IsTeamLeader reports whether the user is designated as a leader of at least one team.
func (d *DB) IsTeamLeader(userID int64) (bool, error) {
	var count int
	err := d.core.QueryRow(`SELECT COUNT(*) FROM team_leaders WHERE user_id = ?`, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// IsLeaderOfTeam reports whether userID is designated as a leader of teamID.
func (d *DB) IsLeaderOfTeam(userID, teamID int64) (bool, error) {
	var count int
	err := d.core.QueryRow(`SELECT COUNT(*) FROM team_leaders WHERE team_id = ? AND user_id = ?`, teamID, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetLedTeamIDs returns the IDs of all teams where userID is a designated leader.
func (d *DB) GetLedTeamIDs(userID int64) ([]int64, error) {
	rows, err := d.core.Query(`SELECT team_id FROM team_leaders WHERE user_id = ? ORDER BY team_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetLedTeams returns the full Team objects for all teams where userID is a designated leader.
func (d *DB) GetLedTeams(userID int64) ([]models.Team, error) {
	rows, err := d.core.Query(`
SELECT t.id, t.name, COALESCE(t.jira_space_key,''), t.timesheets_managed_manually, COALESCE(t.require_activity_comment, false), t.domain_id, COALESCE(t.country_codes, ''), t.created_at
FROM teams t
JOIN team_leaders tl ON t.id = tl.team_id
WHERE tl.user_id = ?
ORDER BY t.name
`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var teams []models.Team
	for rows.Next() {
		var t models.Team
		if err := rows.Scan(&t.ID, &t.Name, &t.JiraSpaceKey, &t.TimesheetsManagedManually, &t.RequireActivityComment, &t.DomainID, &t.CountryCodes, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// IsTeamLeaderOf returns true if leaderID is a designated leader of any team that targetID is currently an active member of.
func (d *DB) IsTeamLeaderOf(leaderID, targetID int64) bool {
	var count int
	err := d.core.QueryRow(`
SELECT COUNT(*)
FROM team_leaders tl
JOIN user_teams ut ON tl.team_id = ut.team_id
WHERE tl.user_id = ? AND ut.user_id = ? AND ut.left_at IS NULL
`, leaderID, targetID).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// --- Status management ---

func (d *DB) ListStatuses() ([]models.Status, error) {
	falseVal := d.dialect.boolDefault(false)
	rows, err := d.presence.Query(fmt.Sprintf(
		"SELECT id, name, color, billable, on_site, sort_order, COALESCE(disabled, %s) FROM statuses ORDER BY sort_order, id",
		falseVal,
	))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var statuses []models.Status
	for rows.Next() {
		var s models.Status
		var disabled sql.NullBool
		if err := rows.Scan(&s.ID, &s.Name, &s.Color, &s.Billable, &s.OnSite, &s.SortOrder, &disabled); err != nil {
			return nil, err
		}
		s.Disabled = disabled.Valid && disabled.Bool
		statuses = append(statuses, s)
	}
	return statuses, rows.Err()
}

// ListActiveStatuses returns only statuses that are not disabled (used for the presence picker).
func (d *DB) ListActiveStatuses() ([]models.Status, error) {
	falseVal := d.dialect.boolDefault(false)
	rows, err := d.presence.Query(fmt.Sprintf(
		"SELECT id, name, color, billable, on_site, sort_order, COALESCE(disabled, %s) FROM statuses WHERE COALESCE(disabled, %s) = %s ORDER BY sort_order, id",
		falseVal, falseVal, falseVal,
	))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var statuses []models.Status
	for rows.Next() {
		var s models.Status
		var disabled sql.NullBool
		if err := rows.Scan(&s.ID, &s.Name, &s.Color, &s.Billable, &s.OnSite, &s.SortOrder, &disabled); err != nil {
			return nil, err
		}
		s.Disabled = disabled.Valid && disabled.Bool
		statuses = append(statuses, s)
	}
	return statuses, rows.Err()
}

// SetStatusDisabled enables or disables a status.
func (d *DB) SetStatusDisabled(id int64, disabled bool) error {
	_, err := d.presence.Exec("UPDATE statuses SET disabled = ? WHERE id = ?", disabled, id)
	return err
}

func (d *DB) CreateStatus(s models.Status) (int64, error) {
	return d.presence.InsertGetID(
		"INSERT INTO statuses (name, color, billable, on_site, sort_order) VALUES (?, ?, ?, ?, ?)",
		s.Name, s.Color, s.Billable, s.OnSite, s.SortOrder,
	)
}

func (d *DB) UpdateStatus(s models.Status) error {
	_, err := d.presence.Exec(
		"UPDATE statuses SET name = ?, color = ?, billable = ?, on_site = ?, sort_order = ? WHERE id = ?",
		s.Name, s.Color, s.Billable, s.OnSite, s.SortOrder, s.ID,
	)
	return err
}

func (d *DB) DeleteStatus(id int64) error {
	var count int
	if err := d.presence.QueryRow("SELECT COUNT(*) FROM presences WHERE status_id = ?", id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("status_in_use")
	}
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
	defer rows.Close() //nolint:errcheck

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
	defer tx.Rollback() //nolint:errcheck

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
		if _, err := tx.Exec(d.dialect.rebind(d.dialect.upsertOnConflict(
			"presences",
			[]string{"user_id", "date", "half", "status_id"},
			"?, ?, ?, ?",
			"user_id, date, half",
			"status_id = excluded.status_id",
		)), userID, date, half, statusID); err != nil {
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
	members, err := d.GetTeamMembersAt(teamID, startDate)
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
	rows, err := d.presence.Query("SELECT id, date, name, allow_imputed, COALESCE(country_code, '') FROM holidays ORDER BY date")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var holidays []models.Holiday
	for rows.Next() {
		var h models.Holiday
		if err := rows.Scan(&h.ID, &h.Date, &h.Name, &h.AllowImputed, &h.CountryCode); err != nil {
			return nil, err
		}
		holidays = append(holidays, h)
	}
	return holidays, rows.Err()
}

func (d *DB) GetHolidayMap(startDate, endDate string) (map[string]models.Holiday, error) {
	rows, err := d.presence.Query(
		"SELECT id, date, name, allow_imputed, COALESCE(country_code, '') FROM holidays WHERE date >= ? AND date <= ?",
		startDate, endDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	result := make(map[string]models.Holiday)
	for rows.Next() {
		var h models.Holiday
		if err := rows.Scan(&h.ID, &h.Date, &h.Name, &h.AllowImputed, &h.CountryCode); err != nil {
			return nil, err
		}
		result[h.Date] = h
	}
	return result, rows.Err()
}

// GetUserHolidayMap returns the map of holidays applicable to a given user based on their assigned site's country.
// Global holidays (CountryCode == "") apply to all users.
// A country-specific holiday applies if the user is assigned to a site located in one of the holiday's countries.
func (d *DB) GetUserHolidayMap(userID int64, startDate, endDate string) (map[string]models.Holiday, error) {
	user, err := d.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	var userCountry string
	if user.SiteID > 0 {
		if site, err := d.GetSite(user.SiteID); err == nil && site != nil {
			userCountry = strings.TrimSpace(site.CountryCode)
		}
	}

	rows, err := d.presence.Query(
		"SELECT id, date, name, allow_imputed, COALESCE(country_code, '') FROM holidays WHERE date >= ? AND date <= ? ORDER BY date",
		startDate, endDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	result := make(map[string]models.Holiday)
	for rows.Next() {
		var h models.Holiday
		if err := rows.Scan(&h.ID, &h.Date, &h.Name, &h.AllowImputed, &h.CountryCode); err != nil {
			return nil, err
		}
		if models.UserMatchesHoliday(userCountry, h) {
			if _, ok := result[h.Date]; ok {
				if !h.AllowImputed {
					result[h.Date] = h
				}
			} else {
				result[h.Date] = h
			}
		}
	}
	return result, rows.Err()
}

// GetTeamHolidayMap returns the map of holidays applicable to a team based on the assigned site countries of its members.
func (d *DB) GetTeamHolidayMap(teamID int64, startDate, endDate string) (map[string]models.Holiday, error) {
	members, err := d.GetTeamMembersAt(teamID, startDate)
	if err != nil {
		return d.GetHolidayMap(startDate, endDate)
	}

	countryMap := make(map[string]bool)
	for _, m := range members {
		if m.SiteID > 0 {
			if site, err := d.GetSite(m.SiteID); err == nil && site != nil && site.CountryCode != "" {
				countryMap[site.CountryCode] = true
			}
		}
	}
	var countries []string
	for c := range countryMap {
		countries = append(countries, c)
	}

	rows, err := d.presence.Query(
		"SELECT id, date, name, allow_imputed, COALESCE(country_code, '') FROM holidays WHERE date >= ? AND date <= ? ORDER BY date",
		startDate, endDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	result := make(map[string]models.Holiday)
	for rows.Next() {
		var h models.Holiday
		if err := rows.Scan(&h.ID, &h.Date, &h.Name, &h.AllowImputed, &h.CountryCode); err != nil {
			return nil, err
		}
		if models.CountriesMatchHoliday(countries, h) {
			if _, ok := result[h.Date]; ok {
				if !h.AllowImputed {
					result[h.Date] = h
				}
			} else {
				result[h.Date] = h
			}
		}
	}
	return result, rows.Err()
}

func (d *DB) CreateHoliday(date, name string, allowImputed bool, countryCode ...string) (int64, error) {
	cc := ""
	if len(countryCode) > 0 {
		cc = sanitizeCountryCodes(countryCode[0])
	}
	return d.presence.InsertGetID(
		"INSERT INTO holidays (date, name, allow_imputed, country_code) VALUES (?, ?, ?, ?)",
		date, name, allowImputed, cc,
	)
}

func (d *DB) UpdateHoliday(id int64, date, name string, allowImputed bool, countryCode ...string) error {
	cc := ""
	if len(countryCode) > 0 {
		cc = sanitizeCountryCodes(countryCode[0])
	}
	_, err := d.presence.Exec(
		"UPDATE holidays SET date = ?, name = ?, allow_imputed = ?, country_code = ? WHERE id = ?",
		date, name, allowImputed, cc, id,
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
	defer tx.Rollback() //nolint:errcheck

	if action == "set" {
		s, err := tx.Prepare(
			"INSERT INTO presence_logs (user_id, actor_id, action, date, status_id, half) VALUES (?, ?, ?, ?, ?, ?)",
		)
		if err != nil {
			return err
		}
		defer s.Close() //nolint:errcheck
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
		defer s.Close() //nolint:errcheck
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
	defer rows.Close() //nolint:errcheck

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

// GetPresenceOverrides returns the latest third-party overrides (where actor_id != user_id)
// for each user and date within [startDate, endDate].
// If a user subsequently self-declared on that date, it is not considered an override.
func (d *DB) GetPresenceOverrides(userIDs []int64, startDate, endDate string) (map[int64]map[string]models.PresenceOverride, error) {
	result := make(map[int64]map[string]models.PresenceOverride)
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

	// Fetch logs in ascending chronological order so that the last entry per (user_id, date) is the final state.
	query := fmt.Sprintf(
		"SELECT id, user_id, actor_id, action, date, half, created_at FROM presence_logs WHERE user_id IN (%s) AND date >= ? AND date <= ? ORDER BY created_at ASC, id ASC",
		strings.Join(placeholders, ","),
	)

	rows, err := d.presence.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	type logEntry struct {
		actorID int64
		action  string
		half    string
	}
	latestPerUserDate := make(map[int64]map[string]logEntry)
	actorIDs := make(map[int64]struct{})

	for rows.Next() {
		var id, userID, actorID int64
		var action, date, half string
		var createdAt time.Time
		if err := rows.Scan(&id, &userID, &actorID, &action, &date, &half, &createdAt); err != nil {
			return nil, err
		}
		if latestPerUserDate[userID] == nil {
			latestPerUserDate[userID] = make(map[string]logEntry)
		}
		latestPerUserDate[userID][date] = logEntry{
			actorID: actorID,
			action:  action,
			half:    half,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for userID, dateMap := range latestPerUserDate {
		for _, entry := range dateMap {
			if entry.actorID != userID && entry.actorID > 0 {
				actorIDs[entry.actorID] = struct{}{}
			}
		}
	}

	names := d.fetchUserNames(actorIDs)

	for userID, dateMap := range latestPerUserDate {
		for date, entry := range dateMap {
			if entry.actorID != userID && entry.actorID > 0 {
				if result[userID] == nil {
					result[userID] = make(map[string]models.PresenceOverride)
				}
				actorName := names[entry.actorID]
				if actorName == "" {
					actorName = fmt.Sprintf("User #%d", entry.actorID)
				}
				result[userID][date] = models.PresenceOverride{
					ActorID:   entry.actorID,
					ActorName: actorName,
					Action:    entry.action,
					Half:      entry.half,
				}
			}
		}
	}

	return result, nil
}

// --- Name lookup helpers ---

func (d *DB) GetTeamName(id int64) string {
	var name string
	d.core.QueryRow("SELECT name FROM teams WHERE id = ?", id).Scan(&name) //nolint:errcheck
	return name
}

func (d *DB) GetStatusName(id int64) string {
	var name string
	d.presence.QueryRow("SELECT name FROM statuses WHERE id = ?", id).Scan(&name) //nolint:errcheck
	return name
}

func (d *DB) GetHolidayName(id int64) string {
	var name string
	d.presence.QueryRow("SELECT name FROM holidays WHERE id = ?", id).Scan(&name) //nolint:errcheck
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
	defer rows.Close() //nolint:errcheck
	names := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		rows.Scan(&id, &name) //nolint:errcheck
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
	defer rows.Close() //nolint:errcheck
	names := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		rows.Scan(&id, &name) //nolint:errcheck
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
	defer rows.Close() //nolint:errcheck
	names := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		rows.Scan(&id, &name) //nolint:errcheck
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
	defer rows.Close() //nolint:errcheck
	names := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		rows.Scan(&id, &name) //nolint:errcheck
		names[id] = name
	}
	return names
}

// --- Admin logging ---

func (d *DB) LogAdminAction(actorID int64, entityType string, entityID int64, action, details string) {
	d.audit.Exec( //nolint:errcheck
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
	defer rows.Close() //nolint:errcheck

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
		collectAdminLogEntityID(l, teamIDs, statusIDs, holidayIDs, userEntityIDs)
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
		logs[i].EntityName = resolveAdminLogEntityName(l, teamNames, statusNames, holidayNames, userNames)
	}
	return logs, nil
}

// collectAdminLogEntityID routes the log entry's EntityID into the appropriate
// ID-set based on EntityType, for later batch name resolution.
func collectAdminLogEntityID(l models.AdminLog, teamIDs, statusIDs, holidayIDs, userEntityIDs map[int64]struct{}) {
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

// resolveAdminLogEntityName returns the human-readable name for the entity
// referenced by the log entry.
func resolveAdminLogEntityName(l models.AdminLog, teamNames, statusNames, holidayNames, userNames map[int64]string) string {
	switch l.EntityType {
	case "team":
		return teamNames[l.EntityID]
	case "status":
		return statusNames[l.EntityID]
	case "holiday":
		return holidayNames[l.EntityID]
	case "user":
		if l.EntityID > 0 {
			return userNames[l.EntityID]
		}
	}
	return ""
}

// --- Site management ---

func (d *DB) CreateSite(s models.Site) (int64, error) {
	id, err := d.floorplan.InsertGetID(
		"INSERT INTO sites (name, country_code, not_corporate_site, seats) VALUES (?, ?, ?, ?)",
		s.Name, s.CountryCode, s.NotCorporateSite, s.Seats,
	)
	if err != nil {
		return 0, err
	}
	if len(s.FloorplanIDs) > 0 {
		_ = d.SetSiteFloorplans(id, s.FloorplanIDs)
	}
	return id, nil
}

func (d *DB) GetSite(id int64) (*models.Site, error) {
	var s models.Site
	err := d.floorplan.QueryRow(
		"SELECT id, name, country_code, not_corporate_site, seats FROM sites WHERE id = ?", id,
	).Scan(&s.ID, &s.Name, &s.CountryCode, &s.NotCorporateSite, &s.Seats)
	if err != nil {
		return nil, err
	}
	fps, err := d.GetFloorplansBySite(id)
	if err == nil {
		s.Floorplans = fps
		for _, fp := range fps {
			s.FloorplanIDs = append(s.FloorplanIDs, fp.ID)
		}
	}
	return &s, nil
}

func (d *DB) ListSites() ([]*models.Site, error) {
	rows, err := d.floorplan.Query("SELECT id, name, country_code, not_corporate_site, seats FROM sites ORDER BY name, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var sites []*models.Site
	siteMap := make(map[int64]*models.Site)
	for rows.Next() {
		var s models.Site
		if err := rows.Scan(&s.ID, &s.Name, &s.CountryCode, &s.NotCorporateSite, &s.Seats); err != nil {
			return nil, err
		}
		s.Floorplans = []*models.Floorplan{}
		s.FloorplanIDs = []int64{}
		sites = append(sites, &s)
		siteMap[s.ID] = &s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fetch all floorplans and populate their sites
	allFps, err := d.ListFloorplans()
	if err == nil {
		for i := range allFps {
			fp := &allFps[i]
			if fp.SiteID > 0 {
				if s, ok := siteMap[fp.SiteID]; ok {
					s.Floorplans = append(s.Floorplans, fp)
					s.FloorplanIDs = append(s.FloorplanIDs, fp.ID)
				}
			}
		}
	}

	return sites, nil
}

func (d *DB) UpdateSite(s models.Site) error {
	_, err := d.floorplan.Exec(
		"UPDATE sites SET name = ?, country_code = ?, not_corporate_site = ?, seats = ? WHERE id = ?",
		s.Name, s.CountryCode, s.NotCorporateSite, s.Seats, s.ID,
	)
	if err != nil {
		return err
	}
	if s.FloorplanIDs != nil {
		return d.SetSiteFloorplans(s.ID, s.FloorplanIDs)
	}
	return nil
}

// GetActiveUsersBySite returns a map of site_id -> list of active (non-disabled) users attached to that site.
func (d *DB) GetActiveUsersBySite() (map[int64][]models.User, error) {
	users, err := d.ListUsers()
	if err != nil {
		return nil, err
	}
	res := make(map[int64][]models.User)
	for _, u := range users {
		if !u.Disabled && u.SiteID > 0 {
			res[u.SiteID] = append(res[u.SiteID], u)
		}
	}
	return res, nil
}

// SitePresenceEntry represents a user's on-site presence record.
type SitePresenceEntry struct {
	UserID int64
	Date   string
	Half   string
}

// GetMonthlyOnSitePresences returns all presences where status has on_site=1 between startDate and endDate.
func (d *DB) GetMonthlyOnSitePresences(startDate, endDate string) ([]SitePresenceEntry, error) {
	rows, err := d.presence.Query(d.dialect.rebind(`
SELECT p.user_id, p.date, p.half
FROM presences p
JOIN statuses s ON p.status_id = s.id
WHERE p.date >= ? AND p.date <= ? AND s.on_site = ?
`), startDate, endDate, true)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var list []SitePresenceEntry
	for rows.Next() {
		var e SitePresenceEntry
		if err := rows.Scan(&e.UserID, &e.Date, &e.Half); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// SiteReservationEntry represents a seat reservation joined with its site.
type SiteReservationEntry struct {
	SiteID int64
	Date   string
	Half   string
	SeatID int64
	UserID int64
}

// GetMonthlySiteReservations returns all seat reservations between startDate and endDate for floorplans attached to a site.
func (d *DB) GetMonthlySiteReservations(startDate, endDate string) ([]SiteReservationEntry, error) {
	rows, err := d.floorplan.Query(d.dialect.rebind(`
SELECT f.site_id, sr.date, sr.half, sr.seat_id, sr.user_id
FROM seat_reservations sr
JOIN seats s ON sr.seat_id = s.id
JOIN floorplans f ON s.floorplan_id = f.id
WHERE f.site_id > 0 AND sr.date >= ? AND sr.date <= ?
`), startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var list []SiteReservationEntry
	for rows.Next() {
		var e SiteReservationEntry
		if err := rows.Scan(&e.SiteID, &e.Date, &e.Half, &e.SeatID, &e.UserID); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// GetSiteReservableSeats returns a map of site_id -> count of reservable seats on floorplans attached to that site.
func (d *DB) GetSiteReservableSeats() (map[int64]int, error) {
	rows, err := d.floorplan.Query(`
SELECT f.site_id, COUNT(s.id)
FROM seats s
JOIN floorplans f ON s.floorplan_id = f.id
WHERE f.site_id > 0
GROUP BY f.site_id
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	result := make(map[int64]int)
	for rows.Next() {
		var siteID int64
		var count int
		if err := rows.Scan(&siteID, &count); err != nil {
			return nil, err
		}
		result[siteID] = count
	}
	return result, rows.Err()
}

func (d *DB) DeleteSite(id int64) error {
	// Detach floorplans and users first
	_, _ = d.core.Exec("UPDATE users SET site_id = 0 WHERE site_id = ?", id)
	_, _ = d.floorplan.Exec("UPDATE floorplans SET site_id = 0 WHERE site_id = ?", id)
	_, err := d.floorplan.Exec("DELETE FROM sites WHERE id = ?", id)
	return err
}

func (d *DB) SetSiteFloorplans(siteID int64, floorplanIDs []int64) error {
	// Reset any floorplans currently attached to this site
	if _, err := d.floorplan.Exec("UPDATE floorplans SET site_id = 0 WHERE site_id = ?", siteID); err != nil {
		return err
	}
	if len(floorplanIDs) == 0 {
		return nil
	}
	for _, fpID := range floorplanIDs {
		if _, err := d.floorplan.Exec("UPDATE floorplans SET site_id = ? WHERE id = ?", siteID, fpID); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) GetFloorplansBySite(siteID int64) ([]*models.Floorplan, error) {
	rows, err := d.floorplan.Query(
		`SELECT f.id, f.name, f.image_path, f.sort_order, f.site_id, COALESCE(s.name, '') AS site_name
		FROM floorplans f
		LEFT JOIN sites s ON f.site_id = s.id
		WHERE f.site_id = ?
		ORDER BY f.sort_order, f.id`, siteID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var fps []*models.Floorplan
	for rows.Next() {
		var f models.Floorplan
		if err := rows.Scan(&f.ID, &f.Name, &f.ImagePath, &f.SortOrder, &f.SiteID, &f.SiteName); err != nil {
			return nil, err
		}
		fps = append(fps, &f)
	}
	return fps, rows.Err()
}

// --- Floorplan management ---

func (d *DB) ListFloorplans() ([]models.Floorplan, error) {
	rows, err := d.floorplan.Query(`
		SELECT f.id, f.name, f.image_path, f.sort_order, f.site_id, COALESCE(s.name, '') AS site_name
		FROM floorplans f
		LEFT JOIN sites s ON f.site_id = s.id
		ORDER BY f.sort_order, f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var fps []models.Floorplan
	for rows.Next() {
		var f models.Floorplan
		if err := rows.Scan(&f.ID, &f.Name, &f.ImagePath, &f.SortOrder, &f.SiteID, &f.SiteName); err != nil {
			return nil, err
		}
		fps = append(fps, f)
	}
	return fps, rows.Err()
}

func (d *DB) GetFloorplan(id int64) (*models.Floorplan, error) {
	var f models.Floorplan
	err := d.floorplan.QueryRow(`
		SELECT f.id, f.name, f.image_path, f.sort_order, f.site_id, COALESCE(s.name, '') AS site_name
		FROM floorplans f
		LEFT JOIN sites s ON f.site_id = s.id
		WHERE f.id = ?`, id).
		Scan(&f.ID, &f.Name, &f.ImagePath, &f.SortOrder, &f.SiteID, &f.SiteName)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (d *DB) CreateFloorplan(name string, sortOrder int) (int64, error) {
	return d.CreateFloorplanWithSite(name, 0, sortOrder)
}

func (d *DB) CreateFloorplanWithSite(name string, siteID int64, sortOrder int) (int64, error) {
	return d.floorplan.InsertGetID("INSERT INTO floorplans (name, site_id, sort_order) VALUES (?, ?, ?)", name, siteID, sortOrder)
}

func (d *DB) UpdateFloorplan(id int64, name string, sortOrder int) error {
	_, err := d.floorplan.Exec("UPDATE floorplans SET name = ?, sort_order = ? WHERE id = ?", name, sortOrder, id)
	return err
}

func (d *DB) UpdateFloorplanWithSite(id int64, name string, siteID int64, sortOrder int) error {
	_, err := d.floorplan.Exec("UPDATE floorplans SET name = ?, site_id = ?, sort_order = ? WHERE id = ?", name, siteID, sortOrder, id)
	return err
}

func (d *DB) UpdateFloorplanSite(id int64, siteID int64) error {
	_, err := d.floorplan.Exec("UPDATE floorplans SET site_id = ? WHERE id = ?", siteID, id)
	return err
}

func (d *DB) SetFloorplanImage(id int64, imagePath string) error {
	_, err := d.floorplan.Exec("UPDATE floorplans SET image_path = ? WHERE id = ?", imagePath, id)
	return err
}

func (d *DB) DeleteFloorplan(id int64) error {
	_, _ = d.floorplan.Exec("DELETE FROM floorplan_favorites WHERE floorplan_id = ?", id)
	_, err := d.floorplan.Exec("DELETE FROM floorplans WHERE id = ?", id)
	return err
}

// GetUserFavoriteFloorplanIDs returns all floorplan IDs favorited by the given user.
func (d *DB) GetUserFavoriteFloorplanIDs(userID int64) ([]int64, error) {
	rows, err := d.floorplan.Query(
		`SELECT floorplan_id FROM floorplan_favorites WHERE user_id = ? ORDER BY floorplan_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ToggleFloorplanFavorite flips the favorite state for (userID, floorplanID).
// Returns true when the floorplan is now a favorite, false when it was removed.
func (d *DB) ToggleFloorplanFavorite(userID, floorplanID int64) (bool, error) {
	res, err := d.floorplan.Exec(
		`DELETE FROM floorplan_favorites WHERE user_id = ? AND floorplan_id = ?`, userID, floorplanID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return false, nil
	}
	_, err = d.floorplan.Exec(
		`INSERT INTO floorplan_favorites (user_id, floorplan_id) VALUES (?, ?)`, userID, floorplanID)
	return err == nil, err
}

func (d *DB) ListSeats(floorplanID int64) ([]models.Seat, error) {
	rows, err := d.floorplan.Query("SELECT id, floorplan_id, label, x_pct, y_pct FROM seats WHERE floorplan_id = ? ORDER BY id", floorplanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
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
	return d.floorplan.InsertGetID("INSERT INTO seats (floorplan_id, label, x_pct, y_pct) VALUES (?, ?, ?, ?)", floorplanID, label, xPct, yPct)
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
	defer rows.Close() //nolint:errcheck

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

// seatFlags tracks reservation ownership for one seat across the requested dates.
type seatFlags struct{ isMine, isTaken bool }

// buildSeatResults constructs the SeatWithStatus slice from seats and their status flags.
// A nil statusMap treats every seat as free.
func buildSeatResults(seats []models.Seat, statusMap map[int64]*seatFlags) []models.SeatWithStatus {
	result := make([]models.SeatWithStatus, len(seats))
	for i, s := range seats {
		st := "free"
		if sf := statusMap[s.ID]; sf != nil {
			if sf.isMine {
				st = "mine"
			} else if sf.isTaken {
				st = "taken"
			}
		}
		result[i] = models.SeatWithStatus{Seat: s, Status: st}
	}
	return result
}

// GetSeatsWithStatusForDates returns seats for a floorplan with occupancy status
// computed across multiple dates. A seat is "taken" if reserved by someone else
// on any of the dates, "mine" if reserved by userID on any date, "free" otherwise.
// halfOverlaps reports whether a reservation with half h overlaps the requested half.
func halfOverlaps(h, half string) bool {
	return h == "full" || half == "full" || h == half
}

func (d *DB) GetSeatsWithStatusForDates(floorplanID, userID int64, dates []string, half string) ([]models.SeatWithStatus, error) {
	seats, err := d.ListSeats(floorplanID)
	if err != nil {
		return nil, err
	}
	if len(dates) == 0 {
		return buildSeatResults(seats, nil), nil
	}

	placeholders := make([]string, len(dates))
	args := []interface{}{floorplanID}
	for i, d := range dates {
		placeholders[i] = "?"
		args = append(args, d)
	}
	rows, err := d.floorplan.Query(`
SELECT sr.seat_id, sr.user_id, sr.half
FROM seat_reservations sr
JOIN seats s ON sr.seat_id = s.id
WHERE s.floorplan_id = ? AND sr.date IN (`+strings.Join(placeholders, ",")+`)
`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	statusMap := make(map[int64]*seatFlags)
	for rows.Next() {
		var seatID, uid int64
		var h string
		if err := rows.Scan(&seatID, &uid, &h); err != nil {
			return nil, err
		}
		if !halfOverlaps(h, half) {
			continue
		}
		if statusMap[seatID] == nil {
			statusMap[seatID] = &seatFlags{}
		}
		if uid == userID {
			statusMap[seatID].isMine = true
		} else {
			statusMap[seatID].isTaken = true
		}
	}
	return buildSeatResults(seats, statusMap), nil
}

func (d *DB) ReserveSeat(seatID, userID int64, date, half string) error {
	if half == "" {
		half = "full"
	}
	var count int
	d.floorplan.QueryRow(`
SELECT COUNT(*) FROM seat_reservations
WHERE seat_id = ? AND date = ? AND (half = ? OR half = 'full' OR ? = 'full')
`, seatID, date, half, half).Scan(&count) //nolint:errcheck
	if count > 0 {
		return fmt.Errorf("ce siège est déjà réservé pour cette période")
	}
	var userCount int
	d.floorplan.QueryRow(`
SELECT COUNT(*) FROM seat_reservations
WHERE user_id = ? AND date = ? AND (half = ? OR half = 'full' OR ? = 'full')
`, userID, date, half, half).Scan(&userCount) //nolint:errcheck
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
WHERE p.user_id = ? AND p.date = ? AND s.on_site = ?
`, userID, date, true).Scan(&count)
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
	defer rows.Close() //nolint:errcheck
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
