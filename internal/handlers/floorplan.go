package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"presence-app/internal/db"
	"presence-app/internal/middleware"
	"presence-app/internal/models"
)

// FloorplanHandler handles both the user-facing floorplan page and the admin management.
type FloorplanHandler struct {
	DB      *db.DB
	DataDir string
	Render  func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// ----------------------------------------------------------------
// User-facing page: /floorplan
// ----------------------------------------------------------------

// FloorplanPage renders the seat-booking page for regular users.
func (h *FloorplanHandler) FloorplanPage(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Parse query params
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		dateStr = time.Now().Format("2006-01-02")
	}
	half := r.URL.Query().Get("half")
	if half == "" {
		half = "full"
	}
	fpIDStr := r.URL.Query().Get("floorplan")
	var fpID int64
	if fpIDStr != "" {
		fpID, _ = strconv.ParseInt(fpIDStr, 10, 64)
	}

	floorplans, _ := h.DB.ListFloorplans()

	// Default to first floorplan if none selected
	if fpID == 0 && len(floorplans) > 0 {
		fpID = floorplans[0].ID
	}

	var currentFP *models.Floorplan
	var seats []models.SeatWithStatus
	var isOnSite bool

	if fpID > 0 {
		currentFP, _ = h.DB.GetFloorplan(fpID)
		if currentFP != nil {
			isOnSite, _ = h.DB.GetUserOnSiteStatus(user.ID, dateStr)
			if isOnSite {
				seats, _ = h.DB.GetSeatsWithStatus(fpID, user.ID, dateStr, half)
			} else {
				// Still show seat layout but read-only (no reservations)
				rawSeats, _ := h.DB.ListSeats(fpID)
				for _, s := range rawSeats {
					seats = append(seats, models.SeatWithStatus{Seat: s, Status: "free"})
				}
			}
		}
	}

	h.Render(w, r, "floorplan", map[string]interface{}{
		"Floorplans": floorplans,
		"CurrentFP":  currentFP,
		"Seats":      seats,
		"Date":       dateStr,
		"Half":       half,
		"IsOnSite":   isOnSite,
	})
}

// SeatsAPI returns seats with status as JSON (for Alpine.js updates without full reload).
func (h *FloorplanHandler) SeatsAPI(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	fpID, _ := strconv.ParseInt(r.URL.Query().Get("floorplan_id"), 10, 64)
	date := r.URL.Query().Get("date")
	half := r.URL.Query().Get("half")
	if half == "" {
		half = "full"
	}
	if fpID == 0 || date == "" {
		jsonError(w, "Paramètres manquants", http.StatusBadRequest)
		return
	}

	isOnSite, _ := h.DB.GetUserOnSiteStatus(user.ID, date)
	if !isOnSite {
		jsonOK(w, map[string]interface{}{"seats": []interface{}{}, "on_site": false})
		return
	}

	seats, err := h.DB.GetSeatsWithStatus(fpID, user.ID, date, half)
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{"seats": seats, "on_site": true})
}

// ReserveSeat handles POST /api/reservations.
func (h *FloorplanHandler) ReserveSeat(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	var req struct {
		SeatID int64  `json:"seat_id"`
		Date   string `json:"date"`
		Half   string `json:"half"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}
	if req.SeatID == 0 || req.Date == "" {
		jsonError(w, "Paramètres manquants", http.StatusBadRequest)
		return
	}
	if req.Half == "" {
		req.Half = "full"
	}

	// Verify on-site presence
	isOnSite, _ := h.DB.GetUserOnSiteStatus(user.ID, req.Date)
	if !isOnSite {
		jsonError(w, "Vous devez être déclaré sur site pour réserver un siège", http.StatusForbidden)
		return
	}

	if err := h.DB.ReserveSeat(req.SeatID, user.ID, req.Date, req.Half); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// CancelReservation handles DELETE /api/reservations/{id}.
func (h *FloorplanHandler) CancelReservation(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := h.DB.CancelReservation(id, user.ID); err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ----------------------------------------------------------------
// Admin pages: /admin/floorplans
// ----------------------------------------------------------------

// AdminFloorplansPage renders the floorplan administration page.
func (h *FloorplanHandler) AdminFloorplansPage(w http.ResponseWriter, r *http.Request) {
	floorplans, _ := h.DB.ListFloorplans()

	fpIDStr := r.URL.Query().Get("fp")
	var currentFP *models.Floorplan
	var seats []models.Seat
	if fpIDStr != "" {
		fpID, _ := strconv.ParseInt(fpIDStr, 10, 64)
		currentFP, _ = h.DB.GetFloorplan(fpID)
		if currentFP != nil {
			seats, _ = h.DB.ListSeats(fpID)
		}
	} else if len(floorplans) > 0 {
		currentFP = &floorplans[0]
		seats, _ = h.DB.ListSeats(currentFP.ID)
	}

	h.Render(w, r, "admin_floorplans", map[string]interface{}{
		"Floorplans": floorplans,
		"CurrentFP":  currentFP,
		"Seats":      seats,
	})
}

// CreateFloorplan handles POST /admin/floorplans.
func (h *FloorplanHandler) CreateFloorplan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jsonError(w, "Name is required", http.StatusBadRequest)
		return
	}
	fps, _ := h.DB.ListFloorplans()
	id, err := h.DB.CreateFloorplan(req.Name, len(fps))
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{"id": id, "name": req.Name})
}

// AdminGetFloorplan handles GET /api/admin/floorplans/{id}.
func (h *FloorplanHandler) AdminGetFloorplan(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	fp, err := h.DB.GetFloorplan(id)
	if err != nil {
		jsonError(w, "Not found", http.StatusNotFound)
		return
	}
	jsonOK(w, fp)
}

// AdminListSeats handles GET /api/admin/seats?floorplan_id=.
func (h *FloorplanHandler) AdminListSeats(w http.ResponseWriter, r *http.Request) {
	fpID, _ := strconv.ParseInt(r.URL.Query().Get("floorplan_id"), 10, 64)
	if fpID == 0 {
		jsonError(w, "floorplan_id required", http.StatusBadRequest)
		return
	}
	seats, err := h.DB.ListSeats(fpID)
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	if seats == nil {
		seats = []models.Seat{}
	}
	jsonOK(w, seats)
}

// UpdateFloorplan handles PUT /admin/floorplans/{id}.
func (h *FloorplanHandler) UpdateFloorplan(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jsonError(w, "Name is required", http.StatusBadRequest)
		return
	}
	if err := h.DB.UpdateFloorplan(id, req.Name, req.SortOrder); err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// DeleteFloorplan handles DELETE /admin/floorplans/{id}.
func (h *FloorplanHandler) DeleteFloorplan(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	// Delete image file if exists
	fp, err := h.DB.GetFloorplan(id)
	if err == nil && fp.ImagePath != "" {
		os.Remove(filepath.Join(h.DataDir, fp.ImagePath))
	}
	h.DB.DeleteFloorplan(id) //nolint:errcheck
	jsonOK(w, map[string]string{"status": "ok"})
}

// UploadFloorplanImage handles POST /admin/floorplans/{id}/image.
func (h *FloorplanHandler) UploadFloorplanImage(w http.ResponseWriter, r *http.Request) {
	const maxUploadBytes = 10 << 20 // 10 MB

	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)

	r.ParseMultipartForm(maxUploadBytes) //nolint:errcheck
	file, header, err := r.FormFile("image")
	if err != nil {
		jsonError(w, "Fichier manquant", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}
	if !allowedExts[ext] {
		jsonError(w, "Format non supporté (PNG, JPG, GIF, WEBP)", http.StatusBadRequest)
		return
	}

	// Read up to 512 bytes to detect real content type (defeats extension spoofing)
	sniff := make([]byte, 512)
	n, _ := file.Read(sniff)
	detectedType := http.DetectContentType(sniff[:n])
	allowedTypes := map[string]bool{
		"image/png":  true,
		"image/jpeg": true,
		"image/gif":  true,
		"image/webp": true,
	}
	// http.DetectContentType may return "image/png", "image/jpeg", "image/gif",
	// "image/webp" or "application/octet-stream" for unknown binary data.
	base := strings.SplitN(detectedType, ";", 2)[0]
	if !allowedTypes[base] {
		jsonError(w, "Contenu de fichier invalide (image uniquement)", http.StatusBadRequest)
		return
	}

	// Delete old image
	if fp, err := h.DB.GetFloorplan(id); err == nil && fp.ImagePath != "" {
		os.Remove(filepath.Join(h.DataDir, fp.ImagePath)) //nolint:errcheck
	}

	filename := fmt.Sprintf("floorplan_%d%s", id, ext)
	dst, err := os.Create(filepath.Join(h.DataDir, filename))
	if err != nil {
		jsonError(w, "Erreur création fichier", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Reconstruct full reader: prepend the already-read sniff bytes, then limit total size
	fullReader := io.LimitReader(io.MultiReader(bytes.NewReader(sniff[:n]), file), maxUploadBytes)
	if _, err := io.Copy(dst, fullReader); err != nil {
		os.Remove(filepath.Join(h.DataDir, filename)) //nolint:errcheck
		jsonError(w, "Erreur lors de l'écriture du fichier", http.StatusInternalServerError)
		return
	}

	h.DB.SetFloorplanImage(id, filename) //nolint:errcheck
	jsonOK(w, map[string]string{"status": "ok", "image_path": filename})
}

// CreateSeat handles POST /admin/floorplans/{id}/seats.
func (h *FloorplanHandler) CreateSeat(w http.ResponseWriter, r *http.Request) {
	fpID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Label string  `json:"label"`
		XPct  float64 `json:"x_pct"`
		YPct  float64 `json:"y_pct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		req.Label = "?"
	}
	seatID, err := h.DB.CreateSeat(fpID, req.Label, req.XPct, req.YPct)
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{"id": seatID, "label": req.Label, "x_pct": req.XPct, "y_pct": req.YPct})
}

// UpdateSeat handles PUT /admin/seats/{id}.
func (h *FloorplanHandler) UpdateSeat(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Label string  `json:"label"`
		XPct  float64 `json:"x_pct"`
		YPct  float64 `json:"y_pct"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		req.Label = "?"
	}
	if err := h.DB.UpdateSeat(id, req.Label, req.XPct, req.YPct); err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// DeleteSeat handles DELETE /admin/seats/{id}.
func (h *FloorplanHandler) DeleteSeat(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	h.DB.DeleteSeat(id) //nolint:errcheck
	jsonOK(w, map[string]string{"status": "ok"})
}

// ----------------------------------------------------------------
// User-accessible floorplan/seat listing APIs
// ----------------------------------------------------------------

// ListFloorplansAPI returns all floorplans as JSON (no auth beyond login required).
func (h *FloorplanHandler) ListFloorplansAPI(w http.ResponseWriter, r *http.Request) {
	floorplans, err := h.DB.ListFloorplans()
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	if floorplans == nil {
		floorplans = []models.Floorplan{}
	}
	jsonOK(w, floorplans)
}

// ListSeatsForFloorplanAPI returns the seats for a floorplan without booking status
// (user-accessible, used by the calendar seat-picker modal).
func (h *FloorplanHandler) ListSeatsForFloorplanAPI(w http.ResponseWriter, r *http.Request) {
	fpID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if fpID == 0 {
		jsonError(w, "ID manquant", http.StatusBadRequest)
		return
	}
	seats, err := h.DB.ListSeats(fpID)
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	if seats == nil {
		seats = []models.Seat{}
	}
	jsonOK(w, seats)
}

// BulkReserveSeats handles POST /api/reservations/bulk.
// It attempts to book the given seat for each date in the list,
// silently skipping dates where the user is not on-site or the seat is taken.
func (h *FloorplanHandler) BulkReserveSeats(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	var req struct {
		SeatID int64    `json:"seat_id"`
		Dates  []string `json:"dates"`
		Half   string   `json:"half"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}
	if req.SeatID == 0 || len(req.Dates) == 0 {
		jsonError(w, "Paramètres manquants", http.StatusBadRequest)
		return
	}
	for _, d := range req.Dates {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			jsonError(w, "Date invalide: "+d, http.StatusBadRequest)
			return
		}
	}
	count := h.DB.BulkReserveSeat(req.SeatID, user.ID, req.Dates, req.Half)
	jsonOK(w, map[string]interface{}{"booked": count})
}

// CancelReservationsByDates handles DELETE /api/reservations/bulk.
// It removes all seat reservations for the requesting user on the given list of dates.
func (h *FloorplanHandler) CancelReservationsByDates(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	var req struct {
		Dates []string `json:"dates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}
	if len(req.Dates) == 0 {
		jsonError(w, "Paramètres manquants", http.StatusBadRequest)
		return
	}
	for _, d := range req.Dates {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			jsonError(w, "Date invalide: "+d, http.StatusBadRequest)
			return
		}
	}
	if err := h.DB.CancelUserReservationsForDates(user.ID, req.Dates); err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}
