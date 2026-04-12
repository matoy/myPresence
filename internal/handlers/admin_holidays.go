package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"presence-app/internal/db"
)

// HolidaysHandler manages the public holidays admin page.
type HolidaysHandler struct {
	DB     *db.DB
	Render func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// HolidaysPage renders the list of public holidays.
func (h *HolidaysHandler) HolidaysPage(w http.ResponseWriter, r *http.Request) {
	holidays, err := h.DB.ListHolidays()
	if err != nil {
		http.Error(w, "Error loading holidays", http.StatusInternalServerError)
		return
	}

	h.Render(w, r, "admin_holidays", map[string]interface{}{
		"Holidays": holidays,
		"Error":    r.URL.Query().Get("error"),
	})
}

// CreateHoliday handles POST /admin/holidays.
func (h *HolidaysHandler) CreateHoliday(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/holidays?error=Requête+invalide", http.StatusSeeOther)
		return
	}

	date := r.FormValue("date")
	name := r.FormValue("name")
	allowImputed := r.FormValue("allow_imputed") == "on"

	if date == "" || name == "" {
		http.Redirect(w, r, "/admin/holidays?error=Date+et+nom+requis", http.StatusSeeOther)
		return
	}

	if err := h.DB.CreateHoliday(date, name, allowImputed); err != nil {
		http.Redirect(w, r, "/admin/holidays?error=Date+déjà+existante+ou+erreur+serveur", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin/holidays", http.StatusSeeOther)
}

// UpdateHoliday handles PUT /admin/holidays/{id}.
func (h *HolidaysHandler) UpdateHoliday(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, "ID invalide", http.StatusBadRequest)
		return
	}

	var req struct {
		Date         string `json:"date"`
		Name         string `json:"name"`
		AllowImputed bool   `json:"allow_imputed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}

	if req.Date == "" || req.Name == "" {
		jsonError(w, "Date et nom requis", http.StatusBadRequest)
		return
	}

	if err := h.DB.UpdateHoliday(id, req.Date, req.Name, req.AllowImputed); err != nil {
		jsonError(w, "Erreur serveur", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]string{"status": "ok"})
}

// DeleteHoliday handles DELETE /admin/holidays/{id}.
func (h *HolidaysHandler) DeleteHoliday(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, "ID invalide", http.StatusBadRequest)
		return
	}

	if err := h.DB.DeleteHoliday(id); err != nil {
		jsonError(w, "Erreur suppression", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]string{"status": "ok"})
}
