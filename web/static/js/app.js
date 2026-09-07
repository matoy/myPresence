// Presence App - JavaScript
// Calendar drag-to-select + Admin AJAX helpers

// ============================================================
// Dark Mode Toggle (Alpine.js component)
// ============================================================
function darkModeToggle() {
    return {
        mode: localStorage.getItem('darkMode') || 'system',
        get icon() {
            if (this.mode === 'light') return '☀️';
            if (this.mode === 'dark') return '🌙';
            return '🖥️';
        },
        get label() {
            if (this.mode === 'light') return (_t['nav.theme.light']  || 'Light');
            if (this.mode === 'dark')  return (_t['nav.theme.dark']   || 'Dark');
            return (_t['nav.theme.system'] || 'System');
        },
        cycle() {
            const modes = ['system', 'light', 'dark'];
            const idx = modes.indexOf(this.mode);
            this.mode = modes[(idx + 1) % modes.length];
            localStorage.setItem('darkMode', this.mode);
            this.apply();
        },
        apply() {
            const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
            if (this.mode === 'dark' || (this.mode === 'system' && prefersDark)) {
                document.documentElement.classList.add('dark');
            } else {
                document.documentElement.classList.remove('dark');
            }
        },
        init() {
            // Listen for system preference changes when in 'system' mode
            window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
                if (this.mode === 'system') this.apply();
            });
        }
    };
}

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
        _selectButton: 0,          // mouse button that started the current selection
        _contextMenuViaMouseup: false, // flag to skip @contextmenu when mouseup already showed it
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
        seatFloorplanImage: '',
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
        startSelect(userId, date, event) {
            // Only allow editing own presences (admin/manager can edit anyone)
            if (!this.isAdmin && userId !== this.currentUserId) return;
            if (this.isCellBlocked(userId, date)) return;

            // Remember which button initiated the drag
            this._selectButton = event ? event.button : 0;
            const isTouch = event && event.type === 'touchstart';

            // Long-press (600ms) opens the context menu — touch only.
            // Mouse interactions use mouseup instead, so the timer must not fire.
            if (this.longPressTimer) clearTimeout(this.longPressTimer);
            this.longPressTimer = isTouch ? setTimeout(() => {
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
            }, 600) : null;

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

            // Cancel long-press as soon as the user drags to a new cell
            if (date !== this.startDate && this.longPressTimer) {
                clearTimeout(this.longPressTimer);
                this.longPressTimer = null;
            }

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
                    const data = await resp.json().catch(() => ({}));
                    if (resp.status === 423) {
                        alert((typeof _t !== 'undefined' && _t['cert.locked_warning']) || data.error || 'Déclaration certifiée : modification impossible pour ce mois.');
                    } else {
                        alert(data.error || (typeof _t !== 'undefined' && _t['fp.error']) || 'Erreur');
                    }
                }
            } catch (e) {
                alert((typeof _t !== 'undefined' && _t['fp.conn_error']) || 'Erreur de connexion');
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
                } else {
                    const data = await resp.json().catch(() => ({}));
                    if (resp.status === 423) {
                        alert((typeof _t !== 'undefined' && _t['cert.locked_warning']) || data.error || 'Déclaration certifiée : modification impossible pour ce mois.');
                    } else {
                        alert(data.error || (typeof _t !== 'undefined' && _t['fp.error']) || 'Erreur');
                    }
                }
            } catch (e) {
                alert((typeof _t !== 'undefined' && _t['fp.conn_error']) || 'Erreur de connexion');
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
            // Ignore contextmenu events fired during an active selection (e.g. the
            // browser fires contextmenu on right-mousedown, before the button is
            // released). The mouseup handler will show the menu at the right time.
            if (this.selecting) return;
            // mouseup already set up the context menu for right-click release
            if (this._contextMenuViaMouseup) {
                this._contextMenuViaMouseup = false;
                return;
            }
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
            this.seatFloorplanImage = '';
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
            this.seatFloorplanImage = '';
            try {
                const fp = this.seatFloorplans.find(f => f.id === this.seatFloorplanID);
                if (fp) this.seatFloorplanImage = fp.image_path || '';
                const dates = this.getSeatBookingDates();
                const params = new URLSearchParams({ half: this.pendingHalf || 'full' });
                if (dates.length > 0) params.set('dates', dates.join(','));
                const resp = await fetch(`/api/floorplans/${this.seatFloorplanID}/seats/status?${params}`);
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
                    const d = await resp.json().catch(() => ({}));
                    if (resp.status === 423) {
                        alert((typeof _t !== 'undefined' && _t['cert.locked_warning']) || d.error || 'Déclaration certifiée : modification impossible pour ce mois.');
                    } else {
                        alert(d.error || (typeof _t !== 'undefined' && _t['fp.error']) || 'Erreur');
                    }
                }
            } catch (e) {
                alert((typeof _t !== 'undefined' && _t['fp.conn_error']) || 'Erreur de connexion');
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
                    this.selecting = false;

                    if (e.button === 2) {
                        // Right-click release: show AM/PM context menu for the selection
                        this._contextMenuViaMouseup = true;
                        this.contextMenuForSelection = this.selectedDates.length > 1;
                        this.contextMenuUserId = this.selectedUserId;
                        this.contextMenuDate = this.selectedDates[this.selectedDates.length - 1];
                        this.contextMenuX = Math.min(e.clientX + 5, window.innerWidth - 220);
                        this.contextMenuY = Math.min(e.clientY + 5, window.innerHeight - 210);
                        this.showContextMenu = true;
                        this.showPicker = false;
                    } else {
                        // Left-click release: show status picker directly
                        this.showPicker = true;
                        this.pickerX = Math.min(e.clientX + 10, window.innerWidth - 280);
                        this.pickerY = Math.min(e.clientY + 10, window.innerHeight - 400);
                    }
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
// Team Calendar Component (Alpine.js)
// Handles a multi-row presence table for team members.
// canEdit=true for team leaders / managers / global admins.
// allPresences: { userId: { date: { half: statusId } } }
// ============================================================
function teamCalendarApp(statuses, currentUserId, canEdit, allPresences) {
    return {
        statuses: statuses || [],
        currentUserId,
        canEdit,
        allPresences: allPresences || {},

        selecting: false,
        selectedUserId: null,
        selectedDates: [],
        startDate: null,
        showPicker: false,
        pickerX: 0,
        pickerY: 0,
        _selectButton: 0,
        _contextMenuViaMouseup: false,
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
        seatFloorplanImage: '',
        seatModalSeats: [],
        seatModalLoading: false,
        selectedSeatID: null,

        isCellBlocked(userId, date) {
            // Scope query to this Alpine root element to avoid cross-table conflicts.
            const root = this.$el || document;
            const cell = root.querySelector(`[data-user-id="${userId}"][data-date="${date}"]`);
            if (!cell) return false;
            if (cell.dataset.weekend === 'true') return true;
            if (cell.dataset.holiday === 'true' && cell.dataset.holidayAllowImputed !== 'true') return true;
            return false;
        },

        startSelect(userId, date, event) {
            if (!this.canEdit) return;
            if (this.isCellBlocked(userId, date)) return;

            this._selectButton = event ? event.button : 0;
            const isTouch = event && event.type === 'touchstart';

            if (this.longPressTimer) clearTimeout(this.longPressTimer);
            this.longPressTimer = isTouch ? setTimeout(() => {
                this.longPressTimer = null;
                this.selecting = false;
                this.selectedDates = [];
                if (navigator.vibrate) navigator.vibrate(30);
                const root = this.$el || document;
                const cell = root.querySelector(`[data-user-id="${userId}"][data-date="${date}"]`);
                const rect = cell ? cell.getBoundingClientRect() : { left: 16, bottom: 120 };
                this.openContextMenu(userId, date, {
                    clientX: Math.min(rect.left, window.innerWidth - 220),
                    clientY: Math.min(rect.bottom + 4, window.innerHeight - 230)
                });
            }, 600) : null;

            this.selecting = true;
            this.selectedUserId = userId;
            this.selectedDates = [date];
            this.startDate = date;
            this.showPicker = false;
        },

        extendSelect(userId, date) {
            if (!this.selecting || userId !== this.selectedUserId) return;
            if (this.isCellBlocked(userId, date)) return;
            if (date !== this.startDate && this.longPressTimer) {
                clearTimeout(this.longPressTimer);
                this.longPressTimer = null;
            }
            const start = new Date(this.startDate);
            const end = new Date(date);
            const minDate = start < end ? start : end;
            const maxDate = start < end ? end : start;
            this.selectedDates = [];
            const current = new Date(minDate);
            while (current <= maxDate) {
                const d = current.toISOString().split('T')[0];
                if (!this.isCellBlocked(userId, d)) this.selectedDates.push(d);
                current.setDate(current.getDate() + 1);
            }
        },

        handleTouchMove(event) {
            if (this.longPressTimer) { clearTimeout(this.longPressTimer); this.longPressTimer = null; }
            if (!this.selecting) return;
            const touch = event.touches[0];
            const element = document.elementFromPoint(touch.clientX, touch.clientY);
            if (element) {
                const cell = element.closest('[data-user-id][data-date]');
                if (cell) this.extendSelect(parseInt(cell.dataset.userId), cell.dataset.date);
            }
        },

        isSelected(userId, date) {
            return this.selecting && this.selectedUserId === userId && this.selectedDates.includes(date);
        },

        async applyStatus(statusId) {
            if (!this.selectedDates.length || !this.selectedUserId) return;
            try {
                const resp = await fetch('/api/presences', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ user_id: this.selectedUserId, dates: this.selectedDates, status_id: statusId, half: this.pendingHalf })
                });
                if (resp.ok) { window.location.reload(); }
                else {
                    const d = await resp.json().catch(() => ({}));
                    if (resp.status === 423) {
                        alert((typeof _t !== 'undefined' && _t['cert.locked_warning']) || d.error || 'Déclaration certifiée : modification impossible pour ce mois.');
                    } else {
                        alert(d.error || (typeof _t !== 'undefined' && _t['fp.error']) || 'Erreur');
                    }
                }
            } catch (e) { alert((typeof _t !== 'undefined' && _t['fp.conn_error']) || 'Erreur de connexion'); }
            this.pendingHalf = 'full';
            this.cancelSelect();
        },

        async clearStatus() {
            if (!this.selectedDates.length || !this.selectedUserId) return;
            try {
                const resp = await fetch('/api/presences/clear', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ user_id: this.selectedUserId, dates: this.selectedDates, half: '' })
                });
                if (resp.ok) { window.location.reload(); }
                else {
                    const d = await resp.json().catch(() => ({}));
                    if (resp.status === 423) {
                        alert((typeof _t !== 'undefined' && _t['cert.locked_warning']) || d.error || 'Déclaration certifiée : modification impossible pour ce mois.');
                    } else {
                        alert(d.error || (typeof _t !== 'undefined' && _t['fp.error']) || 'Erreur');
                    }
                }
            } catch (e) { alert((typeof _t !== 'undefined' && _t['fp.conn_error']) || 'Erreur de connexion'); }
            this.pendingHalf = 'full';
            this.cancelSelect();
        },

        // Returns dates for seat booking: active selection or just the right-clicked date.
        getSeatBookingDates() {
            if (this.selectedDates.length > 0) return this.selectedDates;
            return this.contextMenuDate ? [this.contextMenuDate] : [];
        },

        // Returns the target user ID for seat operations.
        getSeatBookingUserId() {
            return this.contextMenuUserId || this.selectedUserId;
        },

        async openSeatModal() {
            this.showContextMenu = false;
            this.showSeatModal = true;
            this.seatModalLoading = true;
            this.selectedSeatID = null;
            this.seatModalSeats = [];
            this.seatFloorplanID = 0;
            this.seatFloorplanImage = '';
            try {
                const resp = await fetch('/api/floorplans');
                if (resp.ok) {
                    this.seatFloorplans = await resp.json();
                    if (this.seatFloorplans.length > 0) {
                        this.seatFloorplanID = this.seatFloorplans[0].id;
                        await this.loadSeatModalSeats();
                    }
                }
            } catch (e) { /* ignore */ } finally {
                this.seatModalLoading = false;
            }
        },

        async loadSeatModalSeats() {
            if (!this.seatFloorplanID) return;
            this.seatModalLoading = true;
            this.selectedSeatID = null;
            this.seatFloorplanImage = '';
            try {
                const fp = this.seatFloorplans.find(f => f.id === this.seatFloorplanID);
                if (fp) this.seatFloorplanImage = fp.image_path || '';
                const dates = this.getSeatBookingDates();
                const params = new URLSearchParams({ half: this.pendingHalf || 'full' });
                if (dates.length > 0) params.set('dates', dates.join(','));
                const resp = await fetch(`/api/floorplans/${this.seatFloorplanID}/seats/status?${params}`);
                if (resp.ok) this.seatModalSeats = await resp.json();
            } finally {
                this.seatModalLoading = false;
            }
        },

        async bookSeatsForSelection() {
            if (!this.selectedSeatID) return;
            const dates = this.getSeatBookingDates();
            const userId = this.getSeatBookingUserId();
            if (!dates.length || !userId) return;
            this.showSeatModal = false;
            try {
                await fetch('/api/reservations/bulk', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ seat_id: this.selectedSeatID, dates, half: this.pendingHalf, user_id: userId })
                });
            } catch (e) { /* ignore */ }
            window.location.reload();
        },

        async cancelSeatsForSelection() {
            this.showContextMenu = false;
            const dates = this.getSeatBookingDates();
            const userId = this.getSeatBookingUserId();
            if (!dates.length || !userId) return;
            try {
                await fetch('/api/reservations/bulk', {
                    method: 'DELETE',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ dates, user_id: userId })
                });
            } catch (e) { /* ignore */ }
            window.location.reload();
        },

        async clearDay() {
            this.showContextMenu = false;
            if (!this.contextMenuUserId || !this.contextMenuDate) return;
            try {
                const resp = await fetch('/api/presences/clear', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ user_id: this.contextMenuUserId, dates: [this.contextMenuDate], half: '' })
                });
                if (resp.ok) { window.location.reload(); }
                else { const d = await resp.json(); alert(d.error || 'Erreur'); }
            } catch (e) { alert('Erreur de connexion'); }
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

        openContextMenu(userId, date, event) {
            if (!this.canEdit) return;
            if (this.isCellBlocked(userId, date)) return;
            if (this.selecting) return;
            if (this._contextMenuViaMouseup) { this._contextMenuViaMouseup = false; return; }
            this.contextMenuForSelection = false;
            this.showContextMenu = true;
            this.showPicker = false;
            this.contextMenuDate = date;
            this.contextMenuUserId = userId;
            this.contextMenuX = Math.min(event.clientX + 5, window.innerWidth - 220);
            this.contextMenuY = Math.min(event.clientY + 5, window.innerHeight - 210);
        },

        selectHalf(half) {
            this.pendingHalf = half;
            this.showContextMenu = false;
            this.selectedUserId = this.contextMenuUserId;
            if (!this.contextMenuForSelection || this.selectedDates.length === 0) {
                this.selectedDates = [this.contextMenuDate];
            }
            this.contextMenuForSelection = false;
            this.pickerX = this.contextMenuX;
            this.pickerY = this.contextMenuY;
            this.showPicker = true;
        },

        init() {
            document.addEventListener('mouseup', (e) => {
                if (this.longPressTimer) { clearTimeout(this.longPressTimer); this.longPressTimer = null; }
                if (this.selecting && this.selectedDates.length > 0) {
                    this.selecting = false;
                    if (e.button === 2) {
                        this._contextMenuViaMouseup = true;
                        this.contextMenuForSelection = this.selectedDates.length > 1;
                        this.contextMenuUserId = this.selectedUserId;
                        this.contextMenuDate = this.selectedDates[this.selectedDates.length - 1];
                        this.contextMenuX = Math.min(e.clientX + 5, window.innerWidth - 220);
                        this.contextMenuY = Math.min(e.clientY + 5, window.innerHeight - 210);
                        this.showContextMenu = true;
                        this.showPicker = false;
                    } else {
                        this.showPicker = true;
                        this.pickerX = Math.min(e.clientX + 10, window.innerWidth - 280);
                        this.pickerY = Math.min(e.clientY + 10, window.innerHeight - 400);
                    }
                }
            });
            document.addEventListener('touchend', (e) => {
                if (this.longPressTimer) { clearTimeout(this.longPressTimer); this.longPressTimer = null; }
                if (this.selecting && this.selectedDates.length > 0) {
                    const touch = e.changedTouches[0];
                    const x = Math.min(touch.clientX + 10, window.innerWidth - 220);
                    const y = Math.max(10, Math.min(touch.clientY - 10, window.innerHeight - 300));
                    if (this.selectedDates.length > 1) {
                        this.contextMenuForSelection = true;
                        this.contextMenuUserId = this.selectedUserId;
                        this.contextMenuDate = this.selectedDates[this.selectedDates.length - 1];
                        this.contextMenuX = x; this.contextMenuY = y;
                        this.showContextMenu = true; this.showPicker = false;
                    } else {
                        this.showPicker = true;
                        this.pickerX = Math.min(touch.clientX + 10, window.innerWidth - 280);
                        this.pickerY = Math.min(touch.clientY - 200, window.innerHeight - 400);
                        if (this.pickerY < 10) this.pickerY = 10;
                    }
                    this.selecting = false;
                }
            });
            document.addEventListener('keydown', (e) => {
                if (e.key === 'Escape') this.cancelSelect();
            });
        }
    };
}

// ============================================================
// Admin: Teams management
// ============================================================
// Admin: Teams management
// ============================================================
function teamsAdmin(initialTeams, countriesCatalog, allUsers, initialSites) {
    let teams = initialTeams;
    let countries = countriesCatalog;
    let users = allUsers;
    let sites = initialSites;
    if (!teams) {
        const el = document.getElementById('teams-json-data');
        if (el && el.textContent) {
            try { teams = JSON.parse(el.textContent); } catch (e) { teams = []; }
        }
    }
    if (!sites) {
        const el = document.getElementById('sites-json-data');
        if (el && el.textContent) {
            try { sites = JSON.parse(el.textContent); } catch (e) { sites = []; }
        }
    }
    if (!countries) {
        const el = document.getElementById('countries-json-data');
        if (el && el.textContent) {
            try { countries = JSON.parse(el.textContent); } catch (e) { countries = []; }
        }
    }
    if (!users) {
        const el = document.getElementById('users-json-data');
        if (el && el.textContent) {
            try { users = JSON.parse(el.textContent); } catch (e) { users = []; }
        }
    }
    return {
        teams: teams || [],
        sites: sites || [],
        countriesCatalog: countries || [],
        allUsers: users || [],
        newTeamName: '',
        newTeamJiraKey: '',
        newTeamManual: false,
        newTeamRequireComment: false,
        newTeamDomainId: 0,
        newTeamLeaderSearch: '',
        newTeamLeaderIds: [],
        newTeamRemindPresence: false,
        newTeamPresenceDays: 3,
        newTeamRemindActivity: false,
        newTeamActivityDays: 3,
        createError: '',
        showCreateModal: false,
        filterText: '',
        filterMembers: 'all',

        get totalCount() {
            return (this.teams || []).length;
        },

        get filteredCount() {
            return (this.teams || []).filter(t => {
                const members = t.Members || t.members || [];
                const activeMemberCount = members.filter(m => !m.left_at).length;
                const teamName = (t.Team && (t.Team.name || t.Team.Name)) || (t.team && (t.team.name || t.team.Name)) || '';
                return this.matchesTeam(teamName, activeMemberCount);
            }).length;
        },

        resetFilters() {
            this.filterText = '';
            this.filterMembers = 'all';
        },

        matchesTeam(name, memberCount) {
            const q = (this.filterText || '').trim().toLowerCase();
            if (q && !(name || '').toLowerCase().includes(q)) return false;
            if (this.filterMembers === 'with' && memberCount <= 0) return false;
            if (this.filterMembers === 'empty' && memberCount > 0) return false;
            return true;
        },

        getGroupedSites(searchText) {
            const q = (searchText || '').trim().toLowerCase();
            const filtered = (this.sites || []).filter(s => {
                if (!q) return true;
                const name = (s.name || '').toLowerCase();
                const code = (s.country_code || '').toLowerCase();
                const country = (this.countriesCatalog || []).find(c => c.code.toLowerCase() === code);
                const countryName = country ? (country.name || '').toLowerCase() : '';
                return name.includes(q) || code.includes(q) || countryName.includes(q);
            });

            const groups = {};
            for (const s of filtered) {
                const code = (s.country_code || '').toUpperCase().trim();
                if (!groups[code]) {
                    const country = (this.countriesCatalog || []).find(c => c.code.toUpperCase() === code);
                    groups[code] = {
                        country: code,
                        countryName: country ? country.name : (code || 'Autres'),
                        flag: country ? country.flag : '',
                        sites: []
                    };
                }
                groups[code].sites.push(s);
            }

            return Object.values(groups).sort((a, b) => {
                if (!a.country) return 1;
                if (!b.country) return -1;
                return (a.countryName || a.country).localeCompare(b.countryName || b.country);
            });
        },

        getCountryFlag(code) {
            if (!code) return '';
            const c = (this.countriesCatalog || []).find(item => item.code.toUpperCase() === code.toUpperCase());
            if (c && c.flag) return c.flag;
            if (code.length === 2) {
                try {
                    return String.fromCodePoint(...[...code.toUpperCase()].map(ch => 0x1F1E6 + ch.charCodeAt(0) - 65));
                } catch (e) {
                    return '';
                }
            }
            return '';
        },

        async setMemberSite(teamId, userId, siteId) {
            try {
                const r = await fetch(`/admin/teams/${teamId}/members/${userId}/site`, {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ site_id: parseInt(siteId) || 0 })
                });
                if (r.ok) {
                    const data = await r.json().catch(() => ({}));
                    const updatedSiteId = data.site_id || 0;
                    const updatedSiteName = data.site_name || '';
                    const updatedCountry = data.site_country_code || '';

                    if (this.allUsers) {
                        const u = this.allUsers.find(user => user.id === userId);
                        if (u) {
                            u.site_id = updatedSiteId;
                            u.site_name = updatedSiteName;
                            u.site_country_code = updatedCountry;
                        }
                    }

                    window.dispatchEvent(new CustomEvent('user-site-updated', {
                        detail: {
                            userId: userId,
                            siteId: updatedSiteId,
                            siteName: updatedSiteName,
                            siteCountryCode: updatedCountry
                        }
                    }));
                    return data;
                } else {
                    const d = await r.json().catch(() => ({}));
                    alert(d.error || 'Erreur lors de la mise à jour du site');
                    window.dispatchEvent(new CustomEvent('user-site-error', { detail: { userId: userId } }));
                    return null;
                }
            } catch (err) {
                alert('Erreur réseau lors de la mise à jour du site');
                window.dispatchEvent(new CustomEvent('user-site-error', { detail: { userId: userId } }));
                return null;
            }
        },

        async createTeam() {
            this.createError = '';
            if (!this.newTeamName.trim()) {
                this.createError = _t['teams.error_name_required'] || 'Name is required';
                return;
            }
            const r = await fetch('/admin/teams', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    name: this.newTeamName.trim(),
                    jira_space_key: this.newTeamJiraKey.trim(),
                    timesheets_managed_manually: this.newTeamManual,
                    require_activity_comment: this.newTeamRequireComment,
                    domain_id: parseInt(this.newTeamDomainId) || 0,
                    remind_presence: this.newTeamRemindPresence,
                    presence_reminder_days: parseInt(this.newTeamPresenceDays) || 3,
                    remind_activity: this.newTeamRemindActivity,
                    activity_reminder_days: parseInt(this.newTeamActivityDays) || 3
                })
            });
            if (r.ok) {
                const data = await r.json().catch(() => ({}));
                if (data.id && this.newTeamLeaderIds.length > 0) {
                    await fetch(`/api/admin/teams/${data.id}/leaders`, {
                        method: 'PUT',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ user_ids: this.newTeamLeaderIds })
                    });
                }
                this.showCreateModal = false;
                window.location.reload();
                return;
            }
            try {
                const d = await r.json();
                this.createError = _t[d.error] || d.error || 'Error';
            } catch (e) {
                this.createError = 'Error';
            }
        },

        async saveTeamDetails(id, name, jiraSpaceKey, timesheetsManagedManually, requireActivityComment, domainId, leaderIds, remindPresence, presenceDays, remindActivity, activityDays) {
            await fetch(`/admin/teams/${id}`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    name,
                    jira_space_key: (jiraSpaceKey || '').trim(),
                    timesheets_managed_manually: !!timesheetsManagedManually,
                    require_activity_comment: !!requireActivityComment,
                    domain_id: parseInt(domainId) || 0,
                    remind_presence: !!remindPresence,
                    presence_reminder_days: parseInt(presenceDays) || 0,
                    remind_activity: !!remindActivity,
                    activity_reminder_days: parseInt(activityDays) || 0
                })
            });
            if (Array.isArray(leaderIds)) {
                await fetch(`/api/admin/teams/${id}/leaders`, {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ user_ids: leaderIds })
                });
            }
            window.location.reload();
        },

        async triggerTeamReminders(teamId, teamName) {
            const confirmMsg = (_t['teams.trigger_reminders_confirm'] || 'Trigger reminder notifications for this team now?');
            if (!confirm(confirmMsg)) return;
            try {
                const r = await fetch(`/api/admin/teams/${teamId}/trigger-reminders`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' }
                });
                if (r.ok) {
                    const data = await r.json();
                    const pCount = data.presence_sent || 0;
                    const aCount = data.activity_sent || 0;
                    const tpl = _t['teams.trigger_reminders_result'] || 'Reminders sent: %d presence, %d activity.';
                    const msg = tpl.replace('%d', pCount).replace('%d', aCount);
                    alert(msg);
                } else {
                    const errData = await r.json().catch(() => ({}));
                    alert(errData.error || 'Error triggering reminders');
                }
            } catch (e) {
                alert('Network error while triggering reminders');
            }
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
            if (!confirm(_t['teams.remove_member_confirm'] || 'Remove this member from the team?')) return;
            await fetch(`/admin/teams/${teamId}/members/${userId}`, { method: 'DELETE' });
            window.location.reload();
        },

        async setLeftAt(teamId, userId, leftAt) {
            await fetch(`/admin/teams/${teamId}/members/${userId}/left-at`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ left_at: leftAt })
            });
            window.location.reload();
        },

        async clearLeftAt(teamId, userId) {
            await fetch(`/admin/teams/${teamId}/members/${userId}/left-at`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ left_at: null })
            });
            window.location.reload();
        }
    };
}

// ============================================================
// Admin: Sites management
// ============================================================
function sitesAdmin(initialSites) {
    let sites = initialSites;
    if (!sites) {
        const el = document.getElementById('sites-json-data');
        if (el && el.textContent) {
            try { sites = JSON.parse(el.textContent); } catch (e) { sites = []; }
        }
    }
    let allFloorplans = [];
    const fpEl = document.getElementById('floorplans-json-data');
    if (fpEl && fpEl.textContent) {
        try { allFloorplans = JSON.parse(fpEl.textContent); } catch (e) { allFloorplans = []; }
    }
    let countriesCatalog = [];
    const cEl = document.getElementById('countries-json-data');
    if (cEl && cEl.textContent) {
        try { countriesCatalog = JSON.parse(cEl.textContent); } catch (e) { countriesCatalog = []; }
    }

    return {
        sites: sites || [],
        allFloorplans: allFloorplans || [],
        countriesCatalog: countriesCatalog || [],
        filterText: '',
        filterCorporate: 'all',
        filterFloors: 'all',
        showCreateModal: false,
        createError: '',
        newSiteName: '',
        newSiteCountryCode: '',
        newSiteNotCorporate: false,
        newSiteSeats: 0,
        newSiteFloorplanSearch: '',
        newSiteFloorplanIds: [],

        get totalCount() {
            return (this.sites || []).length;
        },

        get filteredCount() {
            return (this.sites || []).filter(s => {
                const count = (s.floorplan_ids || s.floorplans || []).length;
                return this.matchesSite(s.name, s.country_code, s.not_corporate_site, count);
            }).length;
        },

        resetFilters() {
            this.filterText = '';
            this.filterCorporate = 'all';
            this.filterFloors = 'all';
        },

        matchesSite(name, countryCode, notCorporate, floorCount) {
            if (this.filterText) {
                const q = this.filterText.toLowerCase();
                const n = (name || '').toLowerCase();
                const c = (countryCode || '').toLowerCase();
                if (!n.includes(q) && !c.includes(q)) return false;
            }
            if (this.filterCorporate === 'corporate' && notCorporate) return false;
            if (this.filterCorporate === 'non_corporate' && !notCorporate) return false;
            if (this.filterFloors === 'with' && floorCount <= 0) return false;
            if (this.filterFloors === 'empty' && floorCount > 0) return false;
            return true;
        },

        isCountryInList(currentStr, code) {
            if (!currentStr || !code) return false;
            return currentStr.trim().toUpperCase() === code.trim().toUpperCase();
        },

        toggleCountryInList(currentStr, code) {
            if (!code) return '';
            const target = code.trim().toUpperCase();
            return (currentStr || '').trim().toUpperCase() === target ? '' : target;
        },

        async createSite() {
            this.createError = '';
            if (!this.newSiteName.trim()) {
                this.createError = _t['sites.error_name_required'] || 'Name is required';
                return;
            }
            const r = await fetch('/admin/sites', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    name: this.newSiteName.trim(),
                    country_code: (this.newSiteCountryCode || '').trim().toUpperCase(),
                    not_corporate_site: !!this.newSiteNotCorporate,
                    seats: parseInt(this.newSiteSeats || 0, 10),
                    floorplan_ids: this.newSiteFloorplanIds
                })
            });
            if (r.ok) {
                this.showCreateModal = false;
                window.location.reload();
                return;
            }
            try {
                const d = await r.json();
                this.createError = _t[d.error] || d.error || 'Error';
            } catch (e) {
                this.createError = 'Error';
            }
        },

        async saveSiteDetails(id, name, countryCode, notCorporate, seats, floorplanIds) {
            await fetch(`/admin/sites/${id}`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    name: (name || '').trim(),
                    country_code: (countryCode || '').trim(),
                    not_corporate_site: !!notCorporate,
                    seats: parseInt(seats || 0, 10),
                    floorplan_ids: floorplanIds || []
                })
            });
            window.location.reload();
        },

        async deleteSite(id) {
            await fetch(`/admin/sites/${id}`, { method: 'DELETE' });
            window.location.reload();
        }
    };
}

// ============================================================
// Admin: Domains management
// ============================================================
function domainsAdmin(initialDomains) {
    let domains = initialDomains;
    if (!domains) {
        const el = document.getElementById('admin-domains-data');
        if (el && el.textContent) {
            try { domains = JSON.parse(el.textContent); } catch (e) { domains = []; }
        }
    }
    let allUsers = window.allUsersForDomains || [];
    const usersEl = document.getElementById('admin-domains-users-data');
    if (usersEl && usersEl.textContent) {
        try { allUsers = JSON.parse(usersEl.textContent); } catch (e) {}
    }
    let allTeams = window.allTeamsForDomains || [];
    const teamsEl = document.getElementById('admin-domains-teams-data');
    if (teamsEl && teamsEl.textContent) {
        try { allTeams = JSON.parse(teamsEl.textContent); } catch (e) {}
    }
    return {
        domains: domains || [],
        showModal: false,
        editId: null,
        form: { name: '', manager_ids: [], team_ids: [] },
        managerSearch: '',
        teamSearch: '',
        formError: '',

        userName(id) {
            const u = (allUsers || []).find(u => u.id === id);
            return u ? u.name : id;
        },

        teamName(id) {
            const t = (allTeams || []).find(t => t.id === id);
            return t ? t.name : id;
        },

        get filteredManagerCandidates() {
            const q = this.managerSearch.trim().toLowerCase();
            return (allUsers || []).filter(u =>
                !this.form.manager_ids.includes(u.id) &&
                (q === '' || u.name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q))
            );
        },

        get filteredTeamCandidates() {
            const q = this.teamSearch.trim().toLowerCase();
            return (allTeams || []).filter(t =>
                !this.form.team_ids.includes(t.id) &&
                (q === '' || t.name.toLowerCase().includes(q))
            );
        },

        openCreateModal() {
            this.editId = null;
            this.form = { name: '', manager_ids: [], team_ids: [] };
            this.managerSearch = '';
            this.teamSearch = '';
            this.formError = '';
            this.showModal = true;
        },

        openEditModal(d) {
            this.editId = d.Domain.id;
            this.form = {
                name: d.Domain.name,
                manager_ids: (d.Managers || []).map(u => u.id),
                team_ids: (d.Teams || []).map(t => t.id)
            };
            this.managerSearch = '';
            this.teamSearch = '';
            this.formError = '';
            this.showModal = true;
        },

        async save() {
            this.formError = '';
            if (!this.form.name.trim()) {
                this.formError = 'Name is required';
                return;
            }
            const url = this.editId ? `/admin/domains/${this.editId}` : '/admin/domains';
            const method = this.editId ? 'PUT' : 'POST';
            const r = await fetch(url, {
                method,
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    name: this.form.name.trim(),
                    manager_ids: this.form.manager_ids,
                    team_ids: this.form.team_ids
                })
            });
            if (r.ok) {
                this.showModal = false;
                window.location.reload();
                return;
            }
            try {
                const d = await r.json();
                this.formError = d.error || 'Error';
            } catch (e) {
                this.formError = 'Error';
            }
        },

        async deleteDomain(id) {
            await fetch(`/admin/domains/${id}`, { method: 'DELETE' });
            window.location.reload();
        }
    };
}

// ============================================================
// Admin: General settings — live environment variable editing
// ============================================================
function envVarsAdmin(initialVars) {
    return {
        vars: initialVars || [],
        editingKey: null,
        editValue: '',
        envError: '',

        isSensitive(key) {
            const u = (key || '').toUpperCase();
            return u.includes('PASSWORD') || u.includes('SECRET') || u.includes('TOKEN') || u.includes('KEY') || u.includes('CREDENTIAL') || u.includes('AUTH') || u === 'SMTP_URL';
        },

        startEdit(v) {
            this.editingKey = v.Key;
            this.editValue = this.isSensitive(v.Key) ? '' : v.Value;
            this.envError = '';
        },

        cancelEdit() {
            this.editingKey = null;
            this.envError = '';
        },

        async saveEdit(v) {
            this.envError = '';
            const r = await fetch('/admin/settings/env', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ key: v.Key, value: this.editValue })
            });
            if (r.ok) {
                if (this.isSensitive(v.Key) && this.editValue !== '') {
                    const last3 = this.editValue.length <= 3 ? this.editValue : this.editValue.slice(-3);
                    v.Value = '••••••••' + last3;
                } else {
                    v.Value = this.editValue;
                }
                this.editingKey = null;
                return;
            }
            try {
                const d = await r.json();
                this.envError = d.error || 'Error';
            } catch (e) {
                this.envError = 'Error';
            }
        }
    };
}

// ============================================================
// Admin: Status management
// ============================================================
function statusAdmin(initialStatuses) {
    return {
        statuses: initialStatuses || [],
        newName: '',
        newColor: '#3b82f6',
        newOrder: 0,
        newBillable: false,
        newOnSite: false,
        createError: '',
        showCreateModal: false,
        filterText: '',
        filterBillable: 'all',
        filterOnSite: 'all',
        filterDisabled: 'all',

        get totalCount() {
            return this.statuses.length;
        },

        get filteredCount() {
            return this.statuses.filter(s => this.matchesStatus(s.name, s.billable, s.on_site, s.disabled)).length;
        },

        resetFilters() {
            this.filterText = '';
            this.filterBillable = 'all';
            this.filterOnSite = 'all';
            this.filterDisabled = 'all';
        },

        openCreateModal() {
            this.newName = '';
            this.newColor = '#3b82f6';
            this.newOrder = 0;
            this.newBillable = false;
            this.newOnSite = false;
            this.createError = '';
            this.showCreateModal = true;
        },

        matchesStatus(name, billable, onSite, disabled) {
            const q = this.filterText.trim().toLowerCase();
            if (q && !(name || '').toLowerCase().includes(q)) return false;
            if (this.filterBillable === '1' && !billable) return false;
            if (this.filterBillable === '0' && billable) return false;
            if (this.filterOnSite === '1' && !onSite) return false;
            if (this.filterOnSite === '0' && onSite) return false;
            if (this.filterDisabled === '0' && disabled) return false;
            if (this.filterDisabled === '1' && !disabled) return false;
            return true;
        },

        async createStatus() {
            this.createError = '';
            if (!this.newName || !this.newColor) { this.createError = 'Name and color are required'; return; }
            const resp = await fetch('/admin/statuses', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: this.newName, color: this.newColor, sort_order: this.newOrder, billable: this.newBillable, on_site: this.newOnSite })
            });
            if (resp.ok) { this.showCreateModal = false; window.location.reload(); }
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
            const resp = await fetch(`/admin/statuses/${id}`, { method: 'DELETE' });
            if (!resp.ok) {
                const d = await resp.json();
                const msg = _t[d.error] || d.error || 'Error';
                alert(msg);
                return;
            }
            window.location.reload();
        },

        async toggleDisabled(id, disabled) {
            await fetch(`/admin/statuses/${id}/disabled`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ disabled })
            });
            window.location.reload();
        }
    };
}

// ============================================================
// Notification Toasts (Alpine.js component)
// ============================================================
function notificationToasts(initialNotifs) {
    let notifs = initialNotifs;
    if (!notifs) {
        const el = document.getElementById('notifications-json-data');
        if (el && el.textContent) {
            try { notifs = JSON.parse(el.textContent); } catch (e) { notifs = []; }
        }
    }
    return {
        notifs: notifs || [],
        formatDate(dtStr) {
            if (!dtStr) return '';
            try {
                const d = new Date(dtStr);
                return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
            } catch (e) {
                return dtStr;
            }
        },
        getTitle(notif) {
            if (!notif) return '';
            if (notif.type === 'team_added' && typeof _t !== 'undefined' && _t['notifications.team_added_title']) {
                return _t['notifications.team_added_title'];
            }
            if (notif.type === 'reminder_presence' && typeof _t !== 'undefined' && _t['notifications.reminder_presence_title']) {
                return _t['notifications.reminder_presence_title'];
            }
            if (notif.type === 'reminder_activity' && typeof _t !== 'undefined' && _t['notifications.reminder_activity_title']) {
                return _t['notifications.reminder_activity_title'];
            }
            return notif.title || '';
        },
        getMessage(notif) {
            if (!notif) return '';
            if (notif.type === 'team_added' && typeof _t !== 'undefined' && _t['notifications.team_added_msg']) {
                const tpl = _t['notifications.team_added_msg'];
                const team = notif.link || '';
                const actor = notif.actor_name || 'Admin';
                if (team) {
                    const parts = tpl.split('%s');
                    if (parts.length === 3) {
                        return parts[0] + team + parts[1] + actor + parts[2];
                    }
                }
            }
            if (notif.type === 'reminder_presence' && typeof _t !== 'undefined' && _t['notifications.reminder_presence_msg']) {
                const match = notif.message ? notif.message.match(/\d+/) : null;
                const days = match ? match[0] : '1';
                return _t['notifications.reminder_presence_msg'].replace('%d', days);
            }
            if (notif.type === 'reminder_activity' && typeof _t !== 'undefined' && _t['notifications.reminder_activity_msg']) {
                const match = notif.message ? notif.message.match(/\d+/) : null;
                const days = match ? match[0] : '1';
                return _t['notifications.reminder_activity_msg'].replace('%d', days);
            }
            return notif.message || '';
        },
        async ack(id) {
            try {
                const resp = await fetch('/api/notifications/' + id + '/ack', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' }
                });
                if (resp.ok) {
                    const idx = this.notifs.findIndex(n => n.id === id);
                    if (idx !== -1) {
                        this.notifs[idx].acknowledged = true;
                    }
                }
            } catch (err) {
                console.error('Error acknowledging notification:', err);
            }
        }
    };
}

