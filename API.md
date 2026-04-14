# myPresence — API Reference

Version: see `/health` endpoint.

## Authentication

All endpoints (except `/health` and `/api/docs`) require authentication.
Two methods are supported:

### 1. Session cookie (browser)
Obtained by logging in via `POST /login`. The cookie `session` is set automatically.

### 2. Personal Access Token (PAT)
Add the following header to every request:

```
Authorization: Bearer <your-token>
```

Tokens use the prefix `mpa_` and are 68 characters long.
A PAT inherits **exactly** the rights of the user who created it — no more, no less.

Manage your tokens at `/settings/tokens` or via the token API below.

---

## Error format

All API errors return JSON:

```json
{ "error": "description of the problem" }
```

---

## Endpoints

### Health

#### `GET /health`
Public. Returns application and database health.

**Response 200**
```json
{
  "status": "ok",
  "uptime": "3h14m",
  "checks": { "database": "ok" },
  "time": "2026-04-14T10:00:00Z"
}
```

---

### Personal Access Tokens

#### `GET /api/tokens`
List the caller's tokens (hash never returned).

**Response 200**
```json
[
  {
    "id": 1,
    "user_id": 42,
    "description": "Script reporting",
    "token_prefix": "mpa_6f3a8b",
    "expires_at": "2026-07-14T10:00:00Z",
    "last_used_at": null,
    "created_at": "2026-04-14T10:00:00Z"
  }
]
```

#### `POST /api/tokens`
Create a new PAT. The raw token is returned **once only**.

**Request**
```json
{
  "description": "Script reporting mensuel",
  "expires_in": 90
}
```
`expires_in` is the validity in days. Use `0` for no expiry (max 3650).

**Response 200**
```json
{
  "id": 1,
  "token": "mpa_6f3a8b1c2d3e...",
  "description": "Script reporting mensuel",
  "token_prefix": "mpa_6f3a8b",
  "expires_at": "2026-07-14T10:00:00Z",
  "created_at": "2026-04-14T10:00:00Z"
}
```

#### `DELETE /api/tokens/{id}`
Revoke a token owned by the caller.

**Response 200**
```json
{ "status": "ok" }
```

---

### Presences

#### `GET /api/presences?team_id=&year=&month=`
Returns presences for all members of a team for the given month.
Requires `activity_viewer` or `team_leader` role (team leaders can only query their own teams).

| Parameter | Type | Description |
|-----------|------|-------------|
| `team_id` | int  | Required |
| `year`    | int  | Required |
| `month`   | int  | Required (1–12) |

**Response 200**
```json
{
  "users": [
    {
      "id": 5,
      "name": "Alice Dupont",
      "presences": {
        "2026-04-07": { "full": 3 },
        "2026-04-08": { "AM": 2, "PM": 4 }
      }
    }
  ],
  "statuses": [
    { "id": 3, "name": "Présent sur site", "color": "#22c55e", "billable": true, "on_site": true }
  ]
}
```

#### `POST /api/presences`
Set presence(s) for one or more dates.
Users can only modify their own presences unless they hold `global` or `team_manager`.

**Request**
```json
{
  "user_id": 5,
  "dates": ["2026-04-14", "2026-04-15"],
  "status_id": 3,
  "half": "full"
}
```
`half` accepts `"full"`, `"AM"`, or `"PM"`.

**Response 200**
```json
{ "status": "ok" }
```

#### `POST /api/presences/clear`
Clear presence(s) for one or more dates.

**Request**
```json
{
  "user_id": 5,
  "dates": ["2026-04-14"],
  "half": "full"
}
```

**Response 200**
```json
{ "status": "ok" }
```

---

### Floor Plans _(disabled if `DISABLE_FLOORPLANS=true`)_

#### `GET /api/floorplans`
List all floor plans.

**Response 200**
```json
[
  { "id": 1, "name": "Étage 3", "image_path": "floorplan_1.png", "sort_order": 0 }
]
```

#### `GET /api/floorplans/{id}/seats`
List seats for a floor plan (without booking status).

**Response 200**
```json
[
  { "id": 12, "floorplan_id": 1, "label": "A3", "x_pct": 45.5, "y_pct": 30.2 }
]
```

#### `GET /api/seats?floorplan_id=&date=&half=`
List seats with booking status for the caller on a given date.

| Parameter | Type | Description |
|-----------|------|-------------|
| `floorplan_id` | int | Required |
| `date` | string | YYYY-MM-DD (default: today) |
| `half` | string | `full`, `AM`, or `PM` (default: `full`) |

**Response 200**
```json
{
  "seats": [
    {
      "id": 12, "floorplan_id": 1, "label": "A3",
      "x_pct": 45.5, "y_pct": 30.2,
      "status": "free",
      "reservation_id": 0
    },
    {
      "id": 13, "floorplan_id": 1, "label": "A4",
      "x_pct": 50.0, "y_pct": 30.2,
      "status": "mine",
      "reservation_id": 7
    }
  ],
  "on_site": true
}
```
`status` values: `free`, `mine` (booked by caller), `taken` (booked by someone else).

#### `POST /api/reservations`
Reserve a single seat for a date and half.

**Request**
```json
{
  "seat_id": 12,
  "date": "2026-04-14",
  "half": "full"
}
```

**Response 200**
```json
{ "id": 7, "status": "ok" }
```

#### `POST /api/reservations/bulk`
Reserve the same seat across multiple dates.
Dates where the caller has no on-site presence are silently skipped.

**Request**
```json
{
  "seat_id": 12,
  "dates": ["2026-04-14", "2026-04-15", "2026-04-16"],
  "half": "full"
}
```

**Response 200**
```json
{ "reserved": 2 }
```

#### `DELETE /api/reservations/bulk`
Cancel seat reservations for the caller across multiple dates.

**Request**
```json
{
  "dates": ["2026-04-14", "2026-04-15"]
}
```

**Response 200**
```json
{ "status": "ok" }
```

#### `DELETE /api/reservations/{id}`
Cancel a specific seat reservation owned by the caller.

**Response 200**
```json
{ "status": "ok" }
```

---

### Users _(requires `global` role)_

#### `GET /api/users`
List all users.

**Response 200**
```json
[
  {
    "id": 5,
    "email": "alice@example.com",
    "name": "Alice Dupont",
    "roles": "basic",
    "is_local": true,
    "disabled": false,
    "created_at": "2025-01-01T00:00:00Z"
  }
]
```

#### `PUT /api/users/{id}/roles`
Update roles for a user. Roles is a comma-separated string.

Valid roles: `basic`, `team_manager`, `team_leader`, `status_manager`, `activity_viewer`, `floorplan_manager`, `global`.

**Request**
```json
{ "roles": "basic,activity_viewer" }
```

**Response 200**
```json
{ "status": "ok" }
```

---

### Activity Report _(requires `activity_viewer` or `team_leader`)_

#### `GET /api/activity?team_id=&year=&month=`
Returns presence statistics for a team over a month.

| Parameter | Type | Description |
|-----------|------|-------------|
| `team_id` | int  | Required |
| `year`    | int  | Required |
| `month`   | int  | Required (1–12) |

**Response 200**
```json
{
  "stats": [
    {
      "user": { "id": 5, "name": "Alice Dupont" },
      "status_counts": { "3": 12.5, "4": 2.0 },
      "billable_days": 12.5,
      "on_site_days": 8.0
    }
  ],
  "statuses": [...],
  "working_days": 22
}
```

---

### Floor Plan Admin _(requires `floorplan_manager` role)_

#### `GET /api/admin/seats?floorplan_id=`
List all seats for a floor plan (admin view, used by the editor).

**Response 200**: array of `Seat` objects (same as `/api/floorplans/{id}/seats`).

---

## Rate limits

No rate limiting is enforced. Implement your own throttling if needed.

## Versioning

The API is not versioned. Breaking changes will be documented in the project changelog.
