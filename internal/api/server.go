package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	"golang.org/x/oauth2"

	"mdwiki/internal/appsettings"
	"mdwiki/internal/config"
	"mdwiki/internal/gitops"
	"mdwiki/internal/indexbuilder"
	"mdwiki/internal/oauth"
	"mdwiki/internal/search"
	"mdwiki/internal/session"
	"mdwiki/internal/space"
	wshub "mdwiki/internal/ws"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type deviceFlowEntry struct {
	DeviceCode string
	ExpiresAt  time.Time
}

// Server wires HTTP + WebSocket.
type Server struct {
	Cfg      config.Config
	Registry *space.Registry
	Store    appsettings.Store
	Sessions *session.Store
	Hub      *wshub.Hub
	oauth    oauth.Config

	deviceMu    sync.Mutex
	deviceFlows map[string]*deviceFlowEntry
}

// New creates API server.
func New(cfg config.Config, reg *space.Registry, store appsettings.Store, sess *session.Store, hub *wshub.Hub) *Server {
	return &Server{
		Cfg:      cfg,
		Registry: reg,
		Store:    store,
		Sessions: sess,
		Hub:      hub,
		oauth: oauth.Config{
			ClientID:     cfg.GitHubClientID,
			ClientSecret: cfg.GitHubSecret,
			RedirectURL:  cfg.GitHubCallbackURL,
		},
		deviceFlows: make(map[string]*deviceFlowEntry),
	}
}

func (s *Server) searchForSpace(key string) (*search.Conn, error) {
	path := filepath.Join(s.Cfg.DataDir, "search", key+".sqlite")
	return search.Open(path)
}

// Router builds chi mux.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors(s.Cfg.FrontendOrigin))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	if os.Getenv("MDWIKI_DEV") == "1" {
		r.Get("/api/dev/login", s.devLogin)
	}

	r.Get("/api/setup/status", s.getSetupStatus)
	r.Post("/api/setup/init", s.setupInitialSpace)

	r.Get("/api/spaces", s.listSpaces)
	r.Get("/api/session", s.getSession)

	r.Get("/auth/github", s.githubStart)
	r.Get("/auth/github/callback", s.githubCallback)
	r.Post("/auth/github/device/start", s.githubDeviceStart)
	r.Get("/auth/github/device/poll", s.githubDevicePoll)

	r.Get("/api/spaces/{space}/page", s.getPage)
	r.Get("/api/spaces/{space}/pages", s.listPages)
	r.Post("/api/spaces/{space}/pages", s.createPage)
	r.Post("/api/spaces/{space}/comments", s.addComment)
	r.Get("/api/spaces/{space}/git", s.gitConsole)
	r.Post("/api/spaces/{space}/page", s.savePage)
	r.Post("/api/spaces/{space}/index", s.reindexSpace)
	r.Post("/api/spaces/{space}/index-mdwiki", s.rebuildRoutingIndex)

	r.Get("/api/spaces/{space}/search", s.searchSpace)

	// WebSocket Yjs
	r.With(s.requireSession).Get("/ws", s.handleWS)

	return r
}

func cors(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Cookie")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) listSpaces(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Load(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var out []map[string]string
	for _, sp := range cfg.Spaces {
		out = append(out, map[string]string{
			"key":          sp.Key,
			"display_name": sp.DisplayName,
			"repo_url":     cfg.RootRepoPath,
			"branch":       "main",
		})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sid := sessionFromCookie(r)
	sess, ok := s.Sessions.Get(sid)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"login": sess.Login, "name": sess.Name, "avatar_url": sess.AvatarURL,
	})
}

func (s *Server) githubStart(w http.ResponseWriter, r *http.Request) {
	o := s.oauth.OAuth2()
	url := o.AuthCodeURL("state", oauth2.AccessTypeOffline)
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) githubCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	o := s.oauth.OAuth2()
	tok, err := o.Exchange(context.Background(), code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	prof, err := oauth.FetchGitHubUser(context.Background(), tok)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sid := session.NewID()
	sess := session.FromOAuth(sid, struct {
		ID        int64
		Login     string
		Name      string
		AvatarURL string
	}{prof.ID, prof.Login, prof.Name, prof.AvatarURL}, tok)
	s.Sessions.Put(sess)
	http.SetCookie(w, &http.Cookie{
		Name:     "mdwiki_session",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 7,
	})
	http.Redirect(w, r, s.Cfg.FrontendOrigin+"/", http.StatusFound)
}

func (s *Server) githubDeviceStart(w http.ResponseWriter, r *http.Request) {
	if s.oauth.ClientID == "" {
		http.Error(w, "github oauth not configured", http.StatusServiceUnavailable)
		return
	}
	o := s.oauth.OAuth2()
	dc, err := oauth.RequestDeviceCode(r.Context(), s.oauth.ClientID, o.Scopes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	flowID := session.NewID()
	s.deviceMu.Lock()
	s.deviceFlows[flowID] = &deviceFlowEntry{
		DeviceCode: dc.DeviceCode,
		ExpiresAt:  time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second),
	}
	s.deviceMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"flow_id":                   flowID,
		"user_code":                 dc.UserCode,
		"verification_uri":          dc.VerificationURI,
		"verification_uri_complete": dc.VerificationURIComplete,
		"expires_in":                dc.ExpiresIn,
		"interval":                  dc.Interval,
	})
}

func (s *Server) githubDevicePoll(w http.ResponseWriter, r *http.Request) {
	flowID := r.URL.Query().Get("flow_id")
	if flowID == "" {
		http.Error(w, "missing flow_id", http.StatusBadRequest)
		return
	}
	s.deviceMu.Lock()
	entry, ok := s.deviceFlows[flowID]
	if !ok {
		s.deviceMu.Unlock()
		http.Error(w, "unknown or expired flow", http.StatusNotFound)
		return
	}
	if time.Now().After(entry.ExpiresAt) {
		delete(s.deviceFlows, flowID)
		s.deviceMu.Unlock()
		http.Error(w, "flow expired", http.StatusGone)
		return
	}
	deviceCode := entry.DeviceCode
	s.deviceMu.Unlock()

	tok, err := oauth.ExchangeDeviceCode(r.Context(), s.oauth.ClientID, deviceCode)
	if err != nil {
		if errors.Is(err, oauth.ErrDeviceAuthorizationPending) || errors.Is(err, oauth.ErrDeviceSlowDown) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			retry := 0
			if errors.Is(err, oauth.ErrDeviceSlowDown) {
				retry = 5
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "pending", "retry_after": retry})
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	prof, err := oauth.FetchGitHubUser(r.Context(), tok)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.deviceMu.Lock()
	delete(s.deviceFlows, flowID)
	s.deviceMu.Unlock()

	sid := session.NewID()
	sess := session.FromOAuth(sid, struct {
		ID        int64
		Login     string
		Name      string
		AvatarURL string
	}{prof.ID, prof.Login, prof.Name, prof.AvatarURL}, tok)
	s.Sessions.Put(sess)
	http.SetCookie(w, &http.Cookie{
		Name:     "mdwiki_session",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 7,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "complete",
		"login":      sess.Login,
		"name":       sess.Name,
		"avatar_url": sess.AvatarURL,
	})
}

func sessionFromCookie(r *http.Request) string {
	c, err := r.Cookie("mdwiki_session")
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid := sessionFromCookie(r)
		if _, ok := s.Sessions.Get(sid); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type saveBody struct {
	Path      string   `json:"path"`
	Content   string   `json:"content"`
	CoAuthors []string `json:"co_authors"`
}

func (s *Server) savePage(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	var body saveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Path == "" {
		body.Path = "README.md"
	}
	root, ent, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	authorName := "mdwiki"
	authorEmail := "local@mdwiki"
	if sid := sessionFromCookie(r); sid != "" {
		if sess, ok := s.Sessions.Get(sid); ok {
			if strings.TrimSpace(sess.Name) != "" {
				authorName = sess.Name
			} else if strings.TrimSpace(sess.Login) != "" {
				authorName = sess.Login
			}
			if strings.TrimSpace(sess.Login) != "" {
				authorEmail = sess.Login + "@users.noreply.github.com"
			}
		}
	}
	if err := gitops.WritePageLocal(root, body.Path, body.Content, authorName, authorEmail, body.CoAuthors); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	commit := GitHeadShort(root)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":           true,
		"path":         body.Path,
		"commit":       commit,
		"repo_url":     root,
		"branch":       "main",
		"display_name": ent.DisplayName,
		"message":      "Committed locally",
	})
}

func (s *Server) getPage(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "README.md"
	}
	root, _, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	_ = gitops.EnsureSpaceMeta(root, spaceKey)
	b, err := gitops.ReadFile(root, path)
	if err != nil {
		if os.IsNotExist(err) {
			b = []byte("# Welcome\n\nStart editing this page. Preview uses GitHub Flavored Markdown.\n")
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"path":    path,
		"content": string(b),
	})
}

func (s *Server) pushToken(r *http.Request) string {
	sid := sessionFromCookie(r)
	sess, ok := s.Sessions.Get(sid)
	if ok && sess.AccessToken != "" {
		return sess.AccessToken
	}
	return s.Cfg.ServerGitToken
}

func (s *Server) reindexSpace(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, _, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	db, err := s.searchForSpace(spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()
	n := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		title := strings.TrimSuffix(filepath.Base(path), ".md")
		if err := search.UpsertPage(db, rel, title, string(b)); err != nil {
			log.Printf("index upsert: %v", err)
		}
		n++
		return nil
	})
	_ = json.NewEncoder(w).Encode(map[string]int{"indexed": n})
}

func (s *Server) searchSpace(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	spaceKey := chi.URLParam(r, "space")
	db, err := s.searchForSpace(spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()
	hits, err := search.Search(db, q, 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(hits)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	sid := sessionFromCookie(r)
	sess, ok := s.Sessions.Get(sid)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	spaceKey := r.URL.Query().Get("space")
	pagePath := r.URL.Query().Get("page")
	if spaceKey == "" || pagePath == "" {
		http.Error(w, "space and page required", http.StatusBadRequest)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	room := spaceKey + ":" + pagePath
	cl := &wshub.Client{
		ID:     session.NewID(),
		Room:   room,
		Conn:   conn,
		Send:   make(chan []byte, 256),
		Hub:    s.Hub,
		UserID: sess.Login,
	}
	s.Hub.Register(cl)

	go s.writePump(cl)
	s.readPump(cl)
}

func (s *Server) readPump(c *wshub.Client) {
	defer func() {
		c.Hub.Unregister(c)
		_ = c.Conn.Close()
	}()
	_ = c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		_ = c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		mt, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.BinaryMessage {
			c.Hub.BroadcastYjs(c.Room, message, c)
			continue
		}
		// JSON control
		var ctrl wshub.Control
		if json.Unmarshal(message, &ctrl) == nil {
			switch ctrl.Type {
			case wshub.MsgStateBlob:
				b, err := wshub.HandleStateBlobPayload(ctrl.ForClient, ctrl.DataB64)
				if err == nil {
					c.Hub.ForwardStateBlob(c.Room, ctrl.ForClient, b)
				}
			case wshub.MsgNeedSync:
				c.Hub.TryPeerStateSync(c.Room, c, 3)
			}
		}
	}
}

func (s *Server) writePump(c *wshub.Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.Send:
			if !ok {
				return
			}
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) devLogin(w http.ResponseWriter, r *http.Request) {
	sid := session.NewID()
	s.Sessions.Put(&session.Session{
		ID:          sid,
		Login:       "dev",
		Name:        "Developer",
		AccessToken: os.Getenv("MDWIKI_SERVER_GIT_TOKEN"),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "mdwiki_session",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	http.Redirect(w, r, s.Cfg.FrontendOrigin+"/", http.StatusFound)
}

func (s *Server) rebuildRoutingIndex(w http.ResponseWriter, r *http.Request) {
	spaceKey := chi.URLParam(r, "space")
	root, _, ok, err := s.resolveSpaceRoot(r, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown space", http.StatusNotFound)
		return
	}
	doc, err := indexbuilder.ScanMarkdown(root, spaceKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := indexbuilder.WriteIndex(root, doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(doc)
}
