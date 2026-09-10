package handlers

import (
	"bytes"
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/matoy/mypresence/internal/i18n"
	"github.com/matoy/mypresence/internal/models"
)

func TestProjectsTemplate_ManualMode_HideFutureFilter(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	baseDir := filepath.Dir(thisFile)
	layoutPath := filepath.Clean(filepath.Join(baseDir, "../../web/templates/layout.html"))
	projectsPath := filepath.Clean(filepath.Join(baseDir, "../../web/templates/projects.html"))

	layoutBytes, err := os.ReadFile(layoutPath)
	if err != nil {
		t.Fatalf("read layout template: %v", err)
	}
	projectsBytes, err := os.ReadFile(projectsPath)
	if err != nil {
		t.Fatalf("read projects template: %v", err)
	}

	funcMap := template.FuncMap{
		"json": func(v interface{}) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
		"safeNewsContent": func(s string) template.HTML {
			return template.HTML(template.HTMLEscapeString(s))
		},
		"newsBgColor": func(hex string, opacity int) template.CSS {
			return template.CSS(hex)
		},
	}

	for _, lang := range []string{"en", "fr"} {
		t.Run("Lang_"+lang, func(t *testing.T) {
			tmpl, err := template.New("base").Funcs(funcMap).Parse(string(layoutBytes))
			if err != nil {
				t.Fatalf("parse layout: %v", err)
			}
			tmpl, err = tmpl.Parse(string(projectsBytes))
			if err != nil {
				t.Fatalf("parse projects: %v", err)
			}

			translations := i18n.T(lang)
			data := models.PageData{
				Config: map[string]interface{}{"AppName": "myPresence"},
				User:   &models.User{ID: 1, Name: "TestUser", Roles: models.RoleBasic},
				Page:   "projects",
				T:      translations,
				Lang:   lang,
				Data: map[string]interface{}{
					"ManualMode":       true,
					"ManualDates":      []string{"2026-09-30", "2026-09-10", "2026-09-01"},
					"ManualWeights":    map[string]float64{"2026-09-30": 1, "2026-09-10": 1, "2026-09-01": 1},
					"ManualActivities": map[string][]models.ProjectActivity{},
					"JiraEnabled":      false,
					"RequireComment":   false,
					"BillableDays":     3.0,
					"TotalDeclared":    0.0,
					"Certified":        false,
					"Year":             2026,
					"Month":            9,
					"PrevYear":         2026,
					"PrevMonth":        8,
					"NextYear":         2026,
					"NextMonth":        10,
				},
			}

			var out bytes.Buffer
			if err := tmpl.ExecuteTemplate(&out, "layout", data); err != nil {
				t.Fatalf("execute projects template: %v", err)
			}
			html := out.String()

			// Check that hideFuture filter toggle button is present
			if !strings.Contains(html, "hideFuture = !hideFuture") {
				t.Error("expected hideFuture toggle button in HTML")
			}

			// Check that the condition is present in the day loop
			if !strings.Contains(html, "!hideFuture || !isFuture(d)") {
				t.Error("expected (!hideFuture || !isFuture(d)) in day loop x-show")
			}

			// Check that Alpine component has isFuture definition
			if !strings.Contains(html, "isFuture(d)") {
				t.Error("expected isFuture method in Alpine component")
			}

			// Check language-specific button text
			expectedLabel := translations["projects.manual.filter_hide_future"]
			if expectedLabel == "" {
				t.Fatalf("missing translation for projects.manual.filter_hide_future in %s", lang)
			}
			if !strings.Contains(html, expectedLabel) {
				t.Errorf("expected button to contain %q in lang %s", expectedLabel, lang)
			}
		})
	}
}
