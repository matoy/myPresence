// Presence App - JavaScript
// Calendar drag-to-select + Admin AJAX helpers

// ============================================================
// Calendar Component (Alpine.js)
// ============================================================
function calendarApp(statuses, currentUserId, isAdmin) {
    return {
        statuses: statuses || [],
        currentUserId: currentUserId,
        isAdmin: isAdmin,
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
        },

        // Open right-click context menu for half-day selection
        openContextMenu(userId, date, event) {
            if (!this.isAdmin && userId !== this.currentUserId) return;
            if (this.isCellBlocked(userId, date)) return;
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
            this.selectedUserId = this.contextMenuUserId;
            this.selectedDates = [this.contextMenuDate];
            this.pickerX = this.contextMenuX;
            this.pickerY = this.contextMenuY;
            this.showPicker = true;
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
                if (this.selecting && this.selectedDates.length > 0) {
                    this.showPicker = true;
                    
                    const touch = e.changedTouches[0];
                    this.pickerX = Math.min(touch.clientX + 10, window.innerWidth - 280);
                    this.pickerY = Math.min(touch.clientY - 200, window.innerHeight - 400);
                    if (this.pickerY < 10) this.pickerY = 10;
                    
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
