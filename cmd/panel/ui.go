package main

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/JIRAYUTRPY/MineCManager/internal/accounts"
)

//go:embed web/*.html web/*.js web/*.css
var webFiles embed.FS
var pagesHTML = template.Must(template.ParseFS(webFiles, "web/*.html"))

type panelPage struct{ ID, Path, Title, Group, Scopes, Fields string }

var pages = []panelPage{
	{"overview", "/", "ภาพรวม", "Panel", "", ""},
	{"hosts", "/fleet", "เครื่อง VM", "Fleet", "admin:fleet", "host"},
	{"minecraft", "/fleet/minecraft", "ติดตั้ง Minecraft", "Fleet", "admin:fleet", "host serverID platform service"},
	{"files", "/fleet/files", "ไฟล์และคำสั่ง OS", "Fleet", "admin:fleet", "host"},
	{"cloud", "/fleet/cloud", "Cloudflare", "Fleet", "admin:fleet", "dnsType"},
	{"backup", "/fleet/backups", "สำรองและกู้คืน VM", "Fleet", "admin:fleet", "host serverID"},
	{"jobs", "/fleet/jobs", "งานล่าสุด", "Fleet", "admin:fleet", "jobStatus"},
	{"updates", "/fleet/updates", "อัปเดตระบบ", "Fleet", "admin:fleet", "updateTarget"},
	{"nodes", "/nodes", "สถานะเซิร์ฟเวอร์", "Minecraft", "nodes:read nodes:control", "node"},
	{"plugins", "/nodes/plugins", "ปลั๊กอิน", "Minecraft", "plugins:read plugins:write", "node"},
	{"config", "/nodes/config", "ไฟล์ Config", "Minecraft", "configs:read configs:write", "node"},
	{"snapshots", "/nodes/backups", "สำเนาเซิร์ฟเวอร์", "Minecraft", "backups:read backups:write backups:restore", "node"},
	{"players", "/nodes/players", "ผู้เล่นและ Inventory", "Minecraft", "players:read inventory:read inventory:write resources:read", "node playerUUID"},
	{"users", "/admin/users", "ผู้ใช้", "ผู้ดูแล", "users:manage", "roleFilter"},
	{"roles", "/admin/roles", "บทบาทและสิทธิ์", "ผู้ดูแล", "users:manage", "selectedRole"},
	{"keys", "/admin/keys", "API keys", "ผู้ดูแล", "admin:keys", ""},
	{"registrations", "/admin/nodes", "ทะเบียน Agent", "ผู้ดูแล", "admin:nodes", ""},
	{"audit", "/admin/audit", "บันทึก Audit", "ผู้ดูแล", "audit:read", ""},
	{"account", "/account", "บัญชีของฉัน", "บัญชี", "", ""},
}

func pageByID(id string) *panelPage {
	for i := range pages {
		if pages[i].ID == id {
			return &pages[i]
		}
	}
	return nil
}
func pageByPath(path string) *panelPage {
	for i := range pages {
		if pages[i].Path == path {
			return &pages[i]
		}
	}
	return nil
}
func (p panelPage) allowed(u *accounts.User) bool {
	if u == nil {
		return false
	}
	if p.Scopes == "" {
		return true
	}
	for _, scope := range strings.Fields(p.Scopes) {
		if u.Has(scope) {
			return true
		}
	}
	return false
}

var serviceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,119}\.service$`)
var playerID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (p panelPage) validPreferences(values map[string]string) bool {
	fields := strings.Fields(p.Fields)
	if len(values) == 0 || len(values) > len(fields) {
		return false
	}
	for k, v := range values {
		if !slices.Contains(fields, k) {
			return false
		}
		if v == "" {
			continue
		}
		switch k {
		case "platform":
			if !slices.Contains([]string{"paper", "purpur", "fabric", "vanilla"}, v) {
				return false
			}
		case "dnsType":
			if !slices.Contains([]string{"A", "AAAA", "CNAME", "SRV"}, v) {
				return false
			}
		case "jobStatus":
			if !slices.Contains([]string{"queued", "running", "succeeded", "failed", "interrupted"}, v) {
				return false
			}
		case "service":
			if !serviceName.MatchString(v) {
				return false
			}
		case "playerUUID":
			if !playerID.MatchString(v) {
				return false
			}
		default:
			if !validID.MatchString(v) {
				return false
			}
		}
	}
	return true
}

type navGroup struct {
	Title string
	Pages []panelPage
}
type screen struct {
	Page        panelPage
	User        *accounts.User
	Groups      []navGroup
	Body        template.HTML
	Next        string
	Preferences map[string]string
}

func (s *server) ui(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	path := r.URL.Path
	if name, ok := strings.CutPrefix(path, "/assets/"); ok {
		if strings.Contains(name, "/") || !slices.Contains([]string{"panel.css", "app.js", "nodes.js", "fleet.js", "manage.js"}, name) {
			http.NotFound(w, r)
			return
		}
		b, e := webFiles.ReadFile("web/" + name)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		kind := "text/javascript"
		if strings.HasSuffix(name, ".css") {
			kind = "text/css"
		}
		w.Header().Set("Content-Type", kind+"; charset=utf-8")
		w.Write(b)
		return
	}
	page := pageByPath(path)
	public := path == "/login" || path == "/setup"
	if page == nil && !public {
		http.NotFound(w, r)
		return
	}
	if s.accounts == nil {
		http.Error(w, "account database unavailable", 503)
		return
	}
	u, err := s.browserUser(r)
	if err != nil {
		http.Error(w, "account database unavailable", 503)
		return
	}
	view := screen{User: u, Next: "/"}
	if dest := pageByPath(r.URL.Query().Get("next")); dest != nil {
		view.Next = dest.Path
	}
	if u == nil {
		done, err := s.accounts.Bootstrapped(r.Context())
		if err != nil {
			http.Error(w, "account database unavailable", 503)
			return
		}
		entry, title := "/login", "เข้าสู่ระบบ"
		if !done {
			entry, title = "/setup", "สร้างบัญชีผู้ดูแลคนแรก"
		}
		if !public {
			view.Next = page.Path
		}
		if path != entry {
			http.Redirect(w, r, entry+"?next="+url.QueryEscape(view.Next), 303)
			return
		}
		view.Page = panelPage{ID: entry[1:], Title: title}
	} else {
		if public {
			http.Redirect(w, r, view.Next, 303)
			return
		}
		if !page.allowed(u) {
			http.Error(w, "ไม่มีสิทธิ์เข้าหน้านี้", 403)
			return
		}
		view.Page = *page
		view.Preferences, err = s.accounts.Preferences(r.Context(), u.Name, page.ID)
		if err != nil {
			http.Error(w, "page preferences unavailable", 503)
			return
		}
		for _, p := range pages {
			if p.allowed(u) {
				if len(view.Groups) == 0 || view.Groups[len(view.Groups)-1].Title != p.Group {
					view.Groups = append(view.Groups, navGroup{Title: p.Group})
				}
				g := &view.Groups[len(view.Groups)-1]
				g.Pages = append(g.Pages, p)
			}
		}
	}
	var body bytes.Buffer
	if err = pagesHTML.ExecuteTemplate(&body, view.Page.ID, view); err != nil {
		http.Error(w, "page unavailable", 500)
		return
	}
	view.Body = template.HTML(body.String()) // Rendered and escaped by html/template.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err = pagesHTML.ExecuteTemplate(w, "layout", view); err != nil {
		return
	}
}
