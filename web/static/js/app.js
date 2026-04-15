// Presence App - JavaScript
// Calendar drag-to-select + Admin AJAX helpers

// ============================================================
// Calendar Component (Alpine.js)
// ============================================================
function calendarApp(statuses, currentUserId, isAdmin, presences) {
    return {
        statuses: statuses || [],
        currentUserId: currentUserId,
        isAdmin: isAdmin,
        presences: presences || {},
        selecting: false,
        selectedUserId: null,
        selectedDates: [],
        startDate: null,
        showPicker: false,
        pickerX: 0,
        pickerY: 0,
        // Half-day context menu state
        showContextMenu: false,
        contextMenuX: 0,
        contextMenuY: 0,
        contextMenuDate: null,
        contextMenuUserId: null,
        pendingHalf: 'full',
        longPressTimer: null,
        contextMenuForSelection: false,

        // Seat reservation modal state
        showSeatModal: false,
        seatFloorplans: [],
        seatFloorplanID: 0,
        seatModalSeats: [],
        seatModalLoading: false,
        selectedSeatID: null,

        // Check if a cell is blocked (weekend or non-imputable holiday)
        isCellBlocked(userId, date) {
            const cell = document.querySelector(`[data-user-id="${userId}"][data-date="${date}"]`);
            if (!cell) return false;
            if (cell.dataset.weekend === "true") return true;
            if (cell.dataset.holiday === "true" && cell.dataset.holidayAllowImputed !== "true") return true;
            return false;
        },

        // Start selection on mousedown/touchstart
        startSelect(userId, date) {
            // Only allow editing own presences (admin/manager can edit anyone)
            if (!this.isAdmin && userId !== this.currentUserId) return;
            if (this.isCellBlocked(userId, date)) return;

            // Long-press (600ms) opens the context menu on mobile
            if (this.longPressTimer) clearTimeout(this.longPressTimer);
            this.longPressTimer = setTimeout(() => {
                this.longPressTimer = null;
                this.selecting = false;
                this.selectedDates = [];
                if (navigator.vibrate) navigator.vibrate(30);
                const cell = document.querySelector(`[data-user-id="${userId}"][data-date="${date}"]`);
                const rect = cell ? cell.getBoundingClientRect() : { left: 16, bottom: 120, width: 36 };
                this.openContextMenu(userId, date, {
                    clientX: Math.min(rect.left, window.innerWidth - 220),
                    clientY: Math.min(rect.bottom + 4, window.innerHeight - 230)
                });
            }, 600);

            this.selecting = true;
            this.selectedUserId = userId;
            this.selectedDates = [date];
            this.startDate = date;
            this.showPicker = false;
        },

        // Extend selection on mousemove/touchmove
        extendSelect(userId, date) {
            if (!this.selecting || userId !== this.selectedUserId) return;
            if (this.isCellBlocked(userId, date)) return;

            // Build date range between startDate and current date
            const start = new Date(this.startDate);
            const end = new Date(date);
            const minDate = start < end ? start : end;
            const maxDate = start < end ? end : start;

            this.selectedDates = [];
            const current = new Date(minDate);
            while (current <= maxDate) {
                const d = current.toISOString().split('T')[0];
                if (!this.isCellBlocked(userId, d)) {
                    this.selectedDates.push(d);
                }
                current.setDate(current.getDate() + 1);
            }
        },

        // Handle touch move for mobile
        handleTouchMove(event) {
            // Cancel long-press if the finger moves
            if (this.longPressTimer) {
                clearTimeout(this.longPressTimer);
                this.longPressTimer = null;
            }
            if (!this.selecting) return;
            const touch = event.touches[0];
            const element = document.elementFromPoint(touch.clientX, touch.clientY);
            if (element) {
                const cell = element.closest('[data-user-id][data-date]');
                if (cell) {
                    const userId = parseInt(cell.dataset.userId);
                    const date = cell.dataset.date;
                    this.extendSelect(userId, date);
                }
            }
        },

        // Check if a cell is in the current selection
        isSelected(userId, date) {
            return this.selecting && 
                   this.selectedUserId === userId && 
                   this.selectedDates.includes(date);
        },

        // Apply a status to selected dates
        async applyStatus(statusId) {
            if (!this.selectedDates.length || !this.selectedUserId) return;

            try {
                const resp = await fetch('/api/presences', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        user_id: this.selectedUserId,
                        dates: this.selectedDates,
                        status_id: statusId,
                        half: this.pendingHalf
                    })
                });
                if (resp.ok) {
                    window.location.reload();
                } else {
                    const data = await resp.json();
                    alert(data.error || 'Erreur');
                }
            } catch (e) {
                alert('Erreur de connexion');
            }
            this.pendingHalf = 'full';
            this.cancelSelect();
        },

        // Clear presences for selected dates
        async clearStatus() {
            if (!this.selectedDates.length || !this.selectedUserId) return;

            try {
                const resp = await fetch('/api/presences/clear', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        user_id: this.selectedUserId,
                        dates: this.selectedDates,
                        half: ''
                    })
                });
                if (resp.ok) {
                    window.location.reload();
                }
            } catch (e) {
                alert('Erreur de connexion');
            }
            this.pendingHalf = 'full';
            this.cancelSelect();
        },

        cancelSelect() {
            this.selecting = false;
            this.selectedDates = [];
            this.selectedUserId = null;
            this.showPicker = false;
            this.showContextMenu = false;
            this.pendingHalf = 'full';
            this.contextMenuForSelection = false;
        },

        // Open right-click context menu for half-day selection
        openContextMenu(userId, date, event) {
            if (!this.isAdmin && userId !== this.currentUserId) return;
            if (this.isCellBlocked(userId, date)) return;
            this.contextMenuForSelection = false;
            this.showContextMenu = true;
            this.showPicker = false;
            this.contextMenuDate = date;
            this.contextMenuUserId = userId;
            this.contextMenuX = Math.min(event.clientX + 5, window.innerWidth - 220);
            this.contextMenuY = Math.min(event.clientY + 5, window.innerHeight - 210);
        },

        // Select half (AM / full / PM) and open status picker
        selectHalf(half) {
            this.pendingHalf = half;
            this.showContextMenu = false;
            if (this.contextMenuForSelection && this.selectedDates.length > 0) {
                // Preserve multi-cell drag selection
                this.selectedUserId = this.contextMenuUserId;
            } else {
                this.selectedUserId = this.contextMenuUserId;
                this.selectedDates = [this.contextMenuDate];
            }
            this.contextMenuForSelection = false;
            this.pickerX = this.contextMenuX;
            this.pickerY = this.contextMenuY;
            this.showPicker = true;
        },

        // Check whether a date has at least one presence declared
        hasPresence(date) {
            if (!date) return false;
            const halves = this.presences[date];
            if (!halves) return false;
            return !!(halves['full'] || halves['AM'] || halves['PM']);
        },

        // Return the primary statusId for a date (full > AM > PM)
        getDateStatusId(date) {
            const halves = this.presences[date];
            if (!halves) return null;
            return halves['full'] || halves['AM'] || halves['PM'] || null;
        },

        // Generate and download an .ics file for the given date
        addToCalendar(date) {
            this.showContextMenu = false;
            const statusId = this.getDateStatusId(date);
            if (!statusId) return;
            const status = this.statuses.find(s => s.id === statusId);
            if (!status) return;

            let busyStatus, transp;
            if (status.billable && status.on_site) {
                busyStatus = 'FREE';
                transp = 'TRANSPARENT';
            } else if (status.billable && !status.on_site) {
                busyStatus = 'WORKINGELSEWHERE';
                transp = 'OPAQUE';
            } else {
                busyStatus = 'OOF';
                transp = 'OPAQUE';
            }

            const dtstart = date.replace(/-/g, '');
            const nextDay = new Date(date + 'T00:00:00');
            nextDay.setDate(nextDay.getDate() + 1);
            const dtend = nextDay.toISOString().split('T')[0].replace(/-/g, '');
            const uid = (typeof crypto !== 'undefined' && crypto.randomUUID)
                ? crypto.randomUUID()
                : (Date.now() + Math.random()).toString(36);

            const ics = [
                'BEGIN:VCALENDAR',
                'VERSION:2.0',
                'CALSCALE:GREGORIAN',
                'PRODID:-//Presence App//FR',
                'BEGIN:VEVENT',
                'UID:' + uid + '@presence-app',
                'DTSTART;VALUE=DATE:' + dtstart,
                'DTEND;VALUE=DATE:' + dtend,
                'SUMMARY:' + status.name,
                'TRANSP:' + transp,
                'X-MICROSOFT-CDO-BUSYSTATUS:' + busyStatus,
                'X-MICROSOFT-CDO-ALLDAYEVENT:TRUE',
                'X-MICROSOFT-CDO-REMINDER-SET:FALSE',
                'END:VEVENT',
                'END:VCALENDAR'
            ].join('\r\n');

            const blob = new Blob([ics], { type: 'text/calendar;charset=utf-8' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'presence-' + date + '.ics';
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
        },

        // Returns the dates to use for bulk seat booking:
        // the active drag-selection if any, otherwise just the right-clicked date.
        getSeatBookingDates() {
            if (this.selectedDates.length > 0) {
                return this.selectedDates;
            }
            return this.contextMenuDate ? [this.contextMenuDate] : [];
        },

        // Open the seat picker modal, lazy-loading floorplans only.
        // Seats are loaded when the user picks a plan from the dropdown.
        async openSeatModal() {
            this.showContextMenu = false;
            this.showSeatModal = true;
            this.seatModalLoading = true;
            this.selectedSeatID = null;
            this.seatModalSeats = [];
            this.seatFloorplanID = 0;
            try {
                const resp = await fetch('/api/floorplans');
                if (resp.ok) {
                    this.seatFloorplans = await resp.json();
                    // Auto-select the first plan and load its seats
                    if (this.seatFloorplans.length > 0) {
                        this.seatFloorplanID = this.seatFloorplans[0].id;
                        await this.loadSeatModalSeats();
                    }
                }
            } catch (e) {
                // ignore - modal will show empty state
            } finally {
                this.seatModalLoading = false;
            }
        },

        // Fetch seats for the currently selected floorplan in the modal.
        async loadSeatModalSeats() {
            if (!this.seatFloorplanID) return;
            this.seatModalLoading = true;
            this.selectedSeatID = null;
            try {
                const resp = await fetch(`/api/floorplans/${this.seatFloorplanID}/seats`);
                if (resp.ok) {
                    this.seatModalSeats = await resp.json();
                }
            } finally {
                this.seatModalLoading = false;
            }
        },

        // Submit bulk seat reservation for the active selection.
        async bookSeatsForSelection() {
            if (!this.selectedSeatID) return;
            const dates = this.getSeatBookingDates();
            if (!dates.length) return;
            this.showSeatModal = false;
            try {
                await fetch('/api/reservations/bulk', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        seat_id: this.selectedSeatID,
                        dates: dates,
                        half: this.pendingHalf
                    })
                });
            } catch (e) { /* ignore */ }
            window.location.reload();
        },

        // Cancel all seat reservations for the active selection.
        async cancelSeatsForSelection() {
            this.showContextMenu = false;
            const dates = this.getSeatBookingDates();
            if (!dates.length) return;
            try {
                await fetch('/api/reservations/bulk', {
                    method: 'DELETE',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ dates })
                });
            } catch (e) { /* ignore */ }
            window.location.reload();
        },

        // Clear all halves for the context menu target date
        async clearDay() {
            this.showContextMenu = false;
            if (!this.contextMenuUserId || !this.contextMenuDate) return;
            try {
                const resp = await fetch('/api/presences/clear', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        user_id: this.contextMenuUserId,
                        dates: [this.contextMenuDate],
                        half: ''
                    })
                });
                if (resp.ok) {
                    window.location.reload();
                } else {
                    const d = await resp.json();
                    alert(d.error || 'Erreur');
                }
            } catch (e) {
                alert('Erreur de connexion');
            }
        },

        // Initialize event listeners
        init() {
            // End selection on mouseup
            document.addEventListener('mouseup', (e) => {
                if (this.longPressTimer) {
                    clearTimeout(this.longPressTimer);
                    this.longPressTimer = null;
                }
                if (this.selecting && this.selectedDates.length > 0) {
                    // Show status picker
                    this.showPicker = true;
                    
                    // Position picker near the mouse/touch
                    const rect = document.body.getBoundingClientRect();
                    this.pickerX = Math.min(e.clientX + 10, window.innerWidth - 280);
                    this.pickerY = Math.min(e.clientY + 10, window.innerHeight - 400);
                    
                    this.selecting = false;
                }
            });

            // End selection on touchend
            document.addEventListener('touchend', (e) => {
                if (this.longPressTimer) {
                    clearTimeout(this.longPressTimer);
                    this.longPressTimer = null;
                }
                if (this.selecting && this.selectedDates.length > 0) {
                    const touch = e.changedTouches[0];
                    const x = Math.min(touch.clientX + 10, window.innerWidth - 220);
                    const y = Math.max(10, Math.min(touch.clientY - 10, window.innerHeight - 300));

                    if (this.selectedDates.length > 1) {
                        // Multi-cell drag: show context menu to choose AM/PM/full first
                        this.contextMenuForSelection = true;
                        this.contextMenuUserId = this.selectedUserId;
                        this.contextMenuDate = this.selectedDates[this.selectedDates.length - 1];
                        this.contextMenuX = x;
                        this.contextMenuY = y;
                        this.showContextMenu = true;
                        this.showPicker = false;
                    } else {
                        // Single cell: show picker directly
                        this.showPicker = true;
                        this.pickerX = Math.min(touch.clientX + 10, window.innerWidth - 280);
                        this.pickerY = Math.min(touch.clientY - 200, window.innerHeight - 400);
                        if (this.pickerY < 10) this.pickerY = 10;
                    }

                    this.selecting = false;
                }
            });

            // Close picker on Escape
            document.addEventListener('keydown', (e) => {
                if (e.key === 'Escape') {
                    this.cancelSelect();
                }
            });
        }
    };
}

// ============================================================
// Admin: Teams management
// ============================================================
function teamsAdmin() {
    return {
        newTeamName: '',

        async createTeam() {
            if (!this.newTeamName.trim()) return;
            const r = await fetch('/admin/teams', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: this.newTeamName.trim() })
            });
            if (r.ok) window.location.reload();
        },

        async renameTeam(id, name) {
            await fetch(`/admin/teams/${id}`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name })
            });
            window.location.reload();
        },

        async deleteTeam(id) {
            await fetch(`/admin/teams/${id}`, { method: 'DELETE' });
            window.location.reload();
        },

        async addMember(teamId, userId) {
            await fetch(`/admin/teams/${teamId}/members`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ user_id: parseInt(userId) })
            });
            window.location.reload();
        },

        async removeMember(teamId, userId) {
            if (!confirm('Remove this member from the team?')) return;
            await fetch(`/admin/teams/${teamId}/members/${userId}`, { method: 'DELETE' });
            window.location.reload();
        }
    };
}

// ============================================================
// Admin: Status management
// ============================================================
function statusAdmin() {
    return {
        newName: '',
        newColor: '#3b82f6',
        newOrder: 0,
        newBillable: false,
        newOnSite: false,
        createError: '',

        async createStatus() {
            this.createError = '';
            if (!this.newName || !this.newColor) { this.createError = 'Name and color are required'; return; }
            const resp = await fetch('/admin/statuses', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: this.newName, color: this.newColor, sort_order: this.newOrder, billable: this.newBillable, on_site: this.newOnSite })
            });
            if (resp.ok) { window.location.reload(); }
            else { const d = await resp.json(); this.createError = d.error || 'Error'; }
        },

        async updateStatus(id, name, color, billable, onSite, sortOrder) {
            await fetch(`/admin/statuses/${id}`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name, color, billable, on_site: onSite, sort_order: sortOrder })
            });
            window.location.reload();
        },

        async deleteStatus(id) {
            await fetch(`/admin/statuses/${id}`, { method: 'DELETE' });
            window.location.reload();
        }
    };
}
