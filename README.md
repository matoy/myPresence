<<<<<<< HEAD
# Presence App

<!-- Replace matoy/myPresence and matoy with actual values -->
[![CI](https://github.com/matoy/myPresence/actions/workflows/ci.yml/badge.svg)](https://github.com/matoy/myPresence/actions/workflows/ci.yml)
[![Release](https://github.com/matoy/myPresence/actions/workflows/release.yml/badge.svg)](https://github.com/matoy/myPresence/actions/workflows/release.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/matoy/mypresence)](https://hub.docker.com/r/matoy/mypresence)
[![Docker Image Size](https://img.shields.io/docker/image-size/matoy/mypresence/latest)](https://hub.docker.com/r/matoy/mypresence)
[![Go Version](https://img.shields.io/badge/go-1.23-00ADD8?logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

A web application for managing employee presence and absences, built with Go and SQLite. Reactive UI using Alpine.js + Tailwind CSS, deployed via Docker.

---

## Features

- **Personal monthly calendar**: each user enters their own presence/absences using click or drag-to-select
- **Customizable statuses**: color, label, billable flag (€)
- **Public holidays**: displayed in grey on the calendar, with an optional imputation flag
- **Team management**: assign users to teams
- **Statistics**: view by team and user over a selected period
- **Activity Report (CRA)**: summary of billable days per team
- **Role management**: granular per-user permissions
- **SAML 2.0 SSO**: Microsoft Entra ID (Azure AD) integration with automatic user provisioning

---

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and Docker Compose

### Start

```bash
docker compose up -d
```

The application is available at **http://localhost:8080**

Default credentials: `admin` / `admin`

### Stop

```bash
docker compose down
```

Data is persisted in the `presence-data` Docker volume (SQLite database at `/data/app.db`).

---

## Configuration

All options are set via environment variables in `docker-compose.yml`.

### General

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listening port |
| `DATA_DIR` | `/data` | Directory for the SQLite database and uploaded files |
| `SECRET_KEY` | `change-me-...` | Session cookie signing key (32 characters recommended) |

### Branding

| Variable | Default | Description |
|----------|---------|-------------|
| `APP_NAME` | `Presence` | Application name (header, browser tab) |
| `PRIMARY_COLOR` | `#3b82f6` | Primary UI color (buttons, links) |
| `SECONDARY_COLOR` | `#1e40af` | Secondary color (header gradient) |
| `ACCENT_COLOR` | `#f59e0b` | Accent color (badges) |
| `LOGO_PATH` | *(empty)* | Path to a logo file (`logo.png`, `logo.svg`, or `logo.jpg` inside `DATA_DIR`) |

### Local Authentication (Admin)

| Variable | Default | Description |
|----------|---------|-------------|
| `ADMIN_USER` | `admin` | Local admin username |
| `ADMIN_PASSWORD` | `admin` | Local admin password |

> The local admin account always has the `global` role. Change these values in production.

### SAML 2.0 SSO (Microsoft Entra ID)

| Variable | Required | Description |
|----------|----------|-------------|
| `SAML_IDP_METADATA_URL` | Yes | IdP metadata URL from Entra ID |
| `SAML_ENTITY_ID` | Yes | Service Provider Entity ID (must match the Entra ID app config) |
| `SAML_ROOT_URL` | Yes | Public application URL (e.g. `https://presence.example.com`) |
| `SAML_SP_CERT_FILE` | No | Path to the SP certificate (auto-generated if not provided) |
| `SAML_SP_KEY_FILE` | No | Path to the SP private key |

SSO is enabled when both `SAML_IDP_METADATA_URL` and `SAML_ENTITY_ID` are set. New SAML users are automatically provisioned with the `basic` role.

**SAML SP endpoints**:
- Metadata: `GET /saml/metadata`
- Initiation: `GET /saml/login`
- ACS (Assertion Consumer Service): `POST /saml/acs`

---

## Roles & Permissions

Roles are cumulative (stored as a comma-separated string per user). The `global` role grants all permissions.

| Role | Access |
|------|--------|
| `basic` | Personal calendar (own presences only) |
| `team_manager` | Team management + edit any user's presences |
| `status_manager` | Create / edit / delete presence statuses |
| `stats_viewer` | View statistics by team |
| `cra_viewer` | View Activity Report (billable days) by team |
| `global` | Full access — includes role management and public holidays |

Roles are assigned from the **🔑 Roles** page (accessible to `global` role only).

---

## Pages

| URL | Required role | Description |
|-----|---------------|-------------|
| `/` | Any logged-in user | Personal monthly calendar |
| `/admin/teams` | `team_manager` | Manage teams and members |
| `/admin/statuses` | `status_manager` | Manage presence statuses |
| `/admin/activity` | `activity_viewer` | Activity report by team and period |
| `/admin/cra` | `cra_viewer` | Activity Report — billable days by team |
| `/admin/roles` | `global` | Assign roles to users |
| `/admin/holidays` | `global` | Manage public holidays |

---

## Calendar

- Month navigation (← →)
- Days selected by click or drag (range selection)
- **Weekends** are greyed out and cannot be selected
- **Public holidays** are greyed out and non-selectable by default
  - *Allow imputed* option: the holiday remains visually grey but can receive a status
- After selection, a colour-coded status picker appears to apply or clear a presence
- Hovering over a cell shows a tooltip with the status name or holiday name

---

## Default Presence Statuses

Automatically seeded on first startup:

| Name | Color | Billable |
|------|-------|----------|
| On-site | 🟢 green | Yes |
| Remote (télétravail) | 🟣 purple | Yes |
| Business trip | 🔵 blue | Yes |
| Leave | 🟠 orange | No |
| Sick leave | 🔴 red | No |
| Training | 🟡 yellow | No |
| Absence | ⚫ grey | No |

All statuses are fully editable from `/admin/statuses`.

---

## Technical Architecture

```
my-super-app/
├── Dockerfile              # Multi-stage build: Go → Alpine runtime
├── docker-compose.yml
├── main.go                 # Router, middleware wiring, template rendering
├── internal/
│   ├── config/             # Configuration loader (env vars)
│   ├── db/                 # Database layer (migrations, CRUD)
│   ├── handlers/           # HTTP handlers (calendar, admin, auth, cra, holidays)
│   ├── middleware/         # Auth session, RequireRole() factory
│   └── models/             # Data structs and role constants
└── web/
    ├── static/
    │   ├── css/app.css
    │   └── js/app.js       # Alpine.js — drag-select calendar, admin AJAX
    └── templates/          # Go HTML templates (layout + pages)
```

**Stack**:
- **Backend**: Go 1.23, `modernc.org/sqlite` (CGO-free), `crewjam/saml`
- **Frontend**: Alpine.js, Tailwind CSS (CDN)
- **Database**: SQLite (single file at `/data/app.db`)
- **Deployment**: Docker multi-stage build, static binary, Alpine 3.19 runtime

### Database Schema

| Table | Description |
|-------|-------------|
| `users` | Users (email, name, roles, password hash) |
| `teams` | Teams |
| `user_teams` | User ↔ team many-to-many mapping |
| `statuses` | Presence statuses (name, color, billable, sort order) |
| `presences` | Recorded presences (user_id, date YYYY-MM-DD, status_id) |
| `presence_logs` | Audit log of all set/clear presence actions (actor, target user, date, status) |
| `sessions` | Active sessions (token, user_id, 30-day expiry) |
| `holidays` | Public holidays (date, name, allow_imputed) |

---

## Rebuilding After Changes

Templates and static files are embedded (`//go:embed`) into the binary at build time. Any change requires a rebuild:

```bash
docker compose down && docker compose up -d --build
```
=======
# myPresence
>>>>>>> 14065b196b4edee9e6b2e85dfccf405f02a5f0f4
