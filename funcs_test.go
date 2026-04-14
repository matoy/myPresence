package main

import "testing"

// ---- tmplFmtF ----

func TestTmplFmtF(t *testing.T) {
	tests := []struct {
		f    float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{-3, "-3"},
		{0.5, "0.5"},
		{1.5, "1.5"},
		{2.5, "2.5"},
		{3.14159, "3.1"}, // truncated to 1 decimal
		{-0.5, "-0.5"},
	}
	for _, tt := range tests {
		if got := tmplFmtF(tt.f); got != tt.want {
			t.Errorf("tmplFmtF(%v) = %q, want %q", tt.f, got, tt.want)
		}
	}
}

// ---- tmplPercentF ----

func TestTmplPercentF(t *testing.T) {
	tests := []struct {
		a, b float64
		want int
	}{
		{0, 20, 0},
		{20, 20, 100},
		{10, 20, 50},
		{1, 3, 33}, // floor
		{0.5, 1, 50},
		{0, 0, 0}, // division-by-zero guard
		{5, 0, 0}, // division-by-zero guard with non-zero numerator
	}
	for _, tt := range tests {
		if got := tmplPercentF(tt.a, tt.b); got != tt.want {
			t.Errorf("tmplPercentF(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// ---- tmplPercent ----

func TestTmplPercent(t *testing.T) {
	tests := []struct {
		a, b int
		want int
	}{
		{0, 20, 0},
		{20, 20, 100},
		{10, 20, 50},
		{1, 3, 33},
		{0, 0, 0},
		{5, 0, 0},
	}
	for _, tt := range tests {
		if got := tmplPercent(tt.a, tt.b); got != tt.want {
			t.Errorf("tmplPercent(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// ---- tmplI2F ----

func TestTmplI2F(t *testing.T) {
	tests := []struct {
		i    int
		want float64
	}{
		{0, 0.0},
		{1, 1.0},
		{-5, -5.0},
		{22, 22.0},
	}
	for _, tt := range tests {
		if got := tmplI2F(tt.i); got != tt.want {
			t.Errorf("tmplI2F(%d) = %v, want %v", tt.i, got, tt.want)
		}
	}
}

// ---- tmplSubF ----

func TestTmplSubF(t *testing.T) {
	tests := []struct {
		a, b float64
		want float64
	}{
		{5, 2, 3},
		{0, 0, 0},
		{1.5, 0.5, 1.0},
		{10, 10.5, -0.5},
	}
	for _, tt := range tests {
		if got := tmplSubF(tt.a, tt.b); got != tt.want {
			t.Errorf("tmplSubF(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// ---- tmplSumMapF ----

func TestTmplSumMapF(t *testing.T) {
	tests := []struct {
		m    map[int64]float64
		want float64
	}{
		{nil, 0},
		{map[int64]float64{}, 0},
		{map[int64]float64{1: 1.0}, 1.0},
		{map[int64]float64{1: 0.5, 2: 0.5}, 1.0},
		{map[int64]float64{1: 2.5, 2: 1.5, 3: 1.0}, 5.0},
	}
	for _, tt := range tests {
		if got := tmplSumMapF(tt.m); got != tt.want {
			t.Errorf("tmplSumMapF(%v) = %v, want %v", tt.m, got, tt.want)
		}
	}
}

// ---- tmplGetCountF ----

func TestTmplGetCountF(t *testing.T) {
	m := map[int64]float64{1: 2.5, 2: 0.5}
	if got := tmplGetCountF(m, 1); got != 2.5 {
		t.Errorf("tmplGetCountF: got %v, want 2.5", got)
	}
	if got := tmplGetCountF(m, 2); got != 0.5 {
		t.Errorf("tmplGetCountF: got %v, want 0.5", got)
	}
	// missing key returns zero
	if got := tmplGetCountF(m, 99); got != 0 {
		t.Errorf("tmplGetCountF(missing): got %v, want 0", got)
	}
}

// ---- tmplGetStrCountF ----

func TestTmplGetStrCountF(t *testing.T) {
	m := map[string]float64{"onsite": 3.5, "remote": 1.0}
	if got := tmplGetStrCountF(m, "onsite"); got != 3.5 {
		t.Errorf("tmplGetStrCountF: got %v, want 3.5", got)
	}
	// missing key returns zero
	if got := tmplGetStrCountF(m, "absent"); got != 0 {
		t.Errorf("tmplGetStrCountF(missing): got %v, want 0", got)
	}
}

// ---- tmplPresenceHalf ----

func TestTmplPresenceHalf(t *testing.T) {
	m := map[string]map[string]int64{
		"2026-04-14": {"full": 2, "AM": 3},
		"2026-04-15": {"PM": 1},
	}

	tests := []struct {
		date, half string
		want       int64
	}{
		{"2026-04-14", "full", 2},
		{"2026-04-14", "AM", 3},
		{"2026-04-14", "PM", 0},   // key missing in sub-map
		{"2026-04-15", "PM", 1},
		{"2026-04-15", "full", 0}, // key missing in sub-map
		{"2026-04-16", "full", 0}, // date missing entirely
	}
	for _, tt := range tests {
		if got := tmplPresenceHalf(m, tt.date, tt.half); got != tt.want {
			t.Errorf("tmplPresenceHalf(%q, %q) = %d, want %d", tt.date, tt.half, got, tt.want)
		}
	}
	// nil map
	if got := tmplPresenceHalf(nil, "2026-04-14", "full"); got != 0 {
		t.Errorf("tmplPresenceHalf(nil): got %d, want 0", got)
	}
}

// ---- tmplHasDatePresence ----

func TestTmplHasDatePresence(t *testing.T) {
	m := map[string]map[string]int64{
		"2026-04-14": {"full": 2},
		"2026-04-15": {},
	}

	if !tmplHasDatePresence(m, "2026-04-14") {
		t.Error("expected true for date with entries")
	}
	if tmplHasDatePresence(m, "2026-04-15") {
		t.Error("expected false for date with empty sub-map")
	}
	if tmplHasDatePresence(m, "2026-04-16") {
		t.Error("expected false for missing date")
	}
	if tmplHasDatePresence(nil, "2026-04-14") {
		t.Error("expected false for nil map")
	}
}
