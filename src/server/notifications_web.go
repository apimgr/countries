package server

import (
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/config"
)

type announcementView struct {
	ID, Type, Title, Message string
	Role, Icon               string
	Dismissible              bool
}

var announcementTemplate = template.Must(template.New("announcements").Parse(`{{range .Announcements}}<div class="site-banner site-banner-{{.Type}}" role="{{.Role}}" data-announcement-id="{{.ID}}"><span class="site-banner-icon" aria-hidden="true">{{.Icon}}</span><span class="site-banner-text"><strong>{{.Title}}</strong> {{.Message}}</span>{{if .Dismissible}}<form method="post" action="{{$.Action}}" class="site-banner-dismiss"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><input type="hidden" name="return_to" value="{{$.ReturnTo}}"><button type="submit" class="site-banner-close" aria-label="{{$.DismissLabel}}">×</button></form>{{end}}</div>{{end}}`))

type announcementTemplateData struct {
	Announcements []announcementView
	Action        string
	CSRF          string
	ReturnTo      string
	DismissLabel  string
}

func (s *Server) publicNotificationsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isFrontendPage(r) {
			next.ServeHTTP(w, r)
			return
		}
		buffer := newHTMLResponseBuffer(w)
		next.ServeHTTP(buffer, r)
		body := buffer.body.Bytes()
		if buffer.status < 200 || buffer.status >= 300 || !strings.Contains(strings.ToLower(buffer.header.Get("Content-Type")), "text/html") || !bytes.Contains(body, []byte("</body>")) {
			buffer.flush()
			return
		}
		prefix := s.requestURLPrefix(r)
		lang := s.languageForRequest(buffer, r)
		if !strings.Contains(string(body), "/static/js/app.js") {
			script := []byte(`<script src="` + template.HTMLEscapeString(prefix+"/static/js/app.js") + `" defer></script>`)
			body = bytes.Replace(body, []byte("</body>"), append(script, []byte("</body>")...), 1)
		}
		if container := s.toastContainer(lang); container != "" {
			body = bytes.Replace(body, []byte("</body>"), []byte(container+"</body>"), 1)
		}
		announcements := s.activeAnnouncements(r)
		if len(announcements) > 0 {
			csrf := csrfToken(buffer, r, strings.HasPrefix(s.reportOrigin(r), "https://"))
			data := announcementTemplateData{Announcements: announcements, Action: prefix + "/announcements/dismiss", CSRF: csrf, ReturnTo: r.URL.RequestURI(), DismissLabel: i18n.Translate(lang, "announcements.dismiss")}
			var rendered bytes.Buffer
			if announcementTemplate.Execute(&rendered, data) == nil {
				body = bytes.Replace(body, []byte(`<template id="announcement-slot"></template>`), rendered.Bytes(), 1)
			}
		}
		if r.URL.Query().Get("saved") == "1" {
			flash := `<div class="notification-flash notification-flash-success" role="status">` + template.HTMLEscapeString(i18n.Translate(lang, "preferences.saved")) + `</div>`
			body = bytes.Replace(body, []byte("<main"), []byte(flash+"<main"), 1)
		}
		buffer.body.Reset()
		_, _ = buffer.body.Write(body)
		buffer.header.Del("Content-Length")
		buffer.flush()
	})
}

func isFrontendPage(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	if path == "/" || path == "/search" || path == "/coordinates" {
		return true
	}
	return strings.HasPrefix(path, "/server/") && path != "/server/tracking.js"
}

func (s *Server) activeAnnouncements(r *http.Request) []announcementView {
	config := s.config.Web.Announcements
	if !config.Enabled || len(config.Messages) == 0 {
		return nil
	}
	dismissed := dismissedAnnouncementIDs(r)
	now := time.Now().UTC()
	views := make([]announcementView, 0, len(config.Messages))
	for _, announcement := range config.Messages {
		if dismissed[announcement.ID] || !announcementIsActive(announcement, now) {
			continue
		}
		kind := announcement.Type
		if kind == "" {
			kind = "info"
		}
		role, icon := "status", "ℹ"
		switch kind {
		case "warning":
			role, icon = "alert", "⚠"
		case "error":
			role, icon = "alert", "✖"
		case "success":
			icon = "✓"
		}
		views = append(views, announcementView{ID: announcement.ID, Type: kind, Title: announcement.Title, Message: announcement.Message, Role: role, Icon: icon, Dismissible: announcement.Dismissible && r.URL.Path != "/server/preferences"})
	}
	return views
}

func (s *Server) toastContainer(lang string) string {
	webui := s.config.Server.Notifications.WebUI
	label := template.HTMLEscapeString(i18n.Translate(lang, "notifications.label"))
	dismiss := template.HTMLEscapeString(i18n.Translate(lang, "notifications.dismiss"))
	copySuccess := template.HTMLEscapeString(i18n.Translate(lang, "notifications.copy_success"))
	copyFailed := template.HTMLEscapeString(i18n.Translate(lang, "notifications.copy_failed"))
	updateAvailable := template.HTMLEscapeString(i18n.Translate(lang, "pwa.update_available"))
	updateNow := template.HTMLEscapeString(i18n.Translate(lang, "pwa.update_now"))
	updateLater := template.HTMLEscapeString(i18n.Translate(lang, "pwa.update_later"))
	installLabel := template.HTMLEscapeString(i18n.Translate(lang, "pwa.install"))
	installTitle := template.HTMLEscapeString(i18n.Translate(lang, "pwa.ios_install_title"))
	installStep1 := template.HTMLEscapeString(i18n.Translate(lang, "pwa.ios_install_step1"))
	installStep2 := template.HTMLEscapeString(i18n.Translate(lang, "pwa.ios_install_step2"))
	installStep3 := template.HTMLEscapeString(i18n.Translate(lang, "pwa.ios_install_step3"))
	installClose := template.HTMLEscapeString(i18n.Translate(lang, "pwa.ios_install_close"))
	return `<div id="toast-container" aria-label="` + label + `" data-position="` + template.HTMLEscapeString(webui.Position) + `" data-dismiss-label="` + dismiss + `" data-copy-success="` + copySuccess + `" data-copy-failed="` + copyFailed + `" data-duration="` + strconv.Itoa(webui.Duration) + `" data-warning-duration="` + strconv.Itoa(webui.WarningDuration) + `" data-update-available="` + updateAvailable + `" data-update-now="` + updateNow + `" data-update-later="` + updateLater + `" data-install-label="` + installLabel + `" data-ios-install-title="` + installTitle + `" data-ios-install-step1="` + installStep1 + `" data-ios-install-step2="` + installStep2 + `" data-ios-install-step3="` + installStep3 + `" data-ios-install-close="` + installClose + `"></div>`
}

func (s *Server) dismissAnnouncement(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || !validCSRF(r) {
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	id := r.FormValue("id")
	var configured *config.AnnouncementConfig
	for index := range s.config.Web.Announcements.Messages {
		announcement := &s.config.Web.Announcements.Messages[index]
		if announcement.ID == id {
			configured = announcement
			break
		}
	}
	if configured == nil || !configured.Dismissible || !announcementIsActive(*configured, time.Now().UTC()) {
		s.errorResponse(w, r, "Announcement cannot be dismissed", http.StatusBadRequest)
		return
	}
	ids := dismissedAnnouncementIDs(r)
	if !ids[id] {
		ids[id] = true
	}
	values := make([]string, 0, len(ids))
	for key := range ids {
		values = append(values, key)
	}
	secure := strings.HasPrefix(s.reportOrigin(r), "https://")
	http.SetCookie(w, &http.Cookie{Name: "dismissed_announcements", Value: url.QueryEscape(strings.Join(values, ",")), Path: "/", MaxAge: 365 * 24 * 60 * 60, Secure: secure, SameSite: http.SameSiteLaxMode})
	returnTo := r.FormValue("return_to")
	if !isLocalReturnPath(returnTo) {
		returnTo = "/"
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func announcementIsActive(announcement config.AnnouncementConfig, now time.Time) bool {
	if announcement.Start != "" {
		start, err := time.Parse(time.RFC3339, announcement.Start)
		if err != nil || now.Before(start) {
			return false
		}
	}
	if announcement.End != "" {
		end, err := time.Parse(time.RFC3339, announcement.End)
		if err != nil || !now.Before(end) {
			return false
		}
	}
	return true
}

func dismissedAnnouncementIDs(r *http.Request) map[string]bool {
	ids := make(map[string]bool)
	cookie, err := r.Cookie("dismissed_announcements")
	if err != nil {
		return ids
	}
	value, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return ids
	}
	for _, id := range strings.Split(value, ",") {
		if id != "" {
			ids[id] = true
		}
	}
	return ids
}

type htmlResponseBuffer struct {
	original http.ResponseWriter
	header   http.Header
	body     bytes.Buffer
	status   int
}

func newHTMLResponseBuffer(original http.ResponseWriter) *htmlResponseBuffer {
	return &htmlResponseBuffer{original: original, header: make(http.Header)}
}

func (w *htmlResponseBuffer) Header() http.Header { return w.header }

func (w *htmlResponseBuffer) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *htmlResponseBuffer) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(value)
}

func (w *htmlResponseBuffer) flush() {
	w.header.Del("Content-Length")
	for key, values := range w.header {
		for _, value := range values {
			w.original.Header().Add(key, value)
		}
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.original.WriteHeader(status)
	if w.body.Len() > 0 {
		_, _ = w.original.Write(w.body.Bytes())
	}
}
