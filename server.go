package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxBodyBytes      = 64 * 1024
	maxQuestionLen    = 2000
	maxHistoryItem    = 4000
	maxGatewayResp    = 1 << 20 // 1MB limit on gateway response
	refreshTimeout    = 15 * time.Second
	chatRateLimit     = 10             // max requests per minute per IP
	chatRateWindow    = 1 * time.Minute
	chatRateCleanupInterval = 5 * time.Minute
)

// Pre-defined error JSON responses — avoid map alloc + marshal on hot paths
var (
	errChatDisabled  = []byte(`{"error":"AI chat is disabled in config.json"}`)
	errBadBody       = []byte(`{"error":"failed to read body"}`)
	errBadJSON       = []byte(`{"error":"Invalid JSON body"}`)
	errEmptyQ        = []byte(`{"error":"question is required and must be non-empty"}`)
	errBodyTooLarge  = []byte(`{"error":"Request body too large (max 65536 bytes)"}`)
	errQTooLong      = []byte(`{"error":"Question too long (max 2000 chars)"}`)
	errDataMissing   = []byte(`{"error":"data.json not found — refresh in progress, try again shortly"}`)
	errChatRateLimit = []byte(`{"error":"Rate limit exceeded — max 10 requests per minute"}`)
)

// chatRateLimiter implements a simple per-IP token-bucket rate limiter for /api/chat.
// Uses sync.Map for lock-free reads on the hot path.
type chatRateLimiter struct {
	// entries maps IP → *rateBucket
	entries sync.Map
}

type rateBucket struct {
	mu        sync.Mutex
	tokens    int
	lastReset time.Time
}

// allow checks if the given IP is within rate limit. Returns true if allowed.
func (rl *chatRateLimiter) allow(ip string) bool {
	now := time.Now()
	val, _ := rl.entries.LoadOrStore(ip, &rateBucket{
		tokens:    chatRateLimit,
		lastReset: now,
	})
	bucket := val.(*rateBucket)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Reset tokens if window has elapsed
	if now.Sub(bucket.lastReset) >= chatRateWindow {
		bucket.tokens = chatRateLimit
		bucket.lastReset = now
	}

	if bucket.tokens <= 0 {
		return false
	}
	bucket.tokens--
	return true
}

// cleanup removes stale entries older than 2x the rate window.
func (rl *chatRateLimiter) cleanup() {
	cutoff := time.Now().Add(-2 * chatRateWindow)
	rl.entries.Range(func(key, val any) bool {
		bucket := val.(*rateBucket)
		bucket.mu.Lock()
		stale := bucket.lastReset.Before(cutoff)
		bucket.mu.Unlock()
		if stale {
			rl.entries.Delete(key)
		}
		return true
	})
}

type Server struct {
	dir          string
	version      string
	cfg          Config
	gatewayToken string

	indexHTMLRendered  []byte
	indexContentLength string // pre-computed strconv.Itoa(len(indexHTMLRendered))
	corsDefault        string // pre-computed "http://localhost:<port>"
	httpClient         *http.Client

	mu             sync.Mutex
	lastRefresh    time.Time
	refreshRunning bool

	// Cached data.json for /api/chat prompt building
	dataMu          sync.RWMutex
	cachedData      map[string]any
	cachedDataRaw   []byte
	cachedDataMtime time.Time

	// System metrics service
	systemSvc *SystemService

	// Chat rate limiter (10 req/min per IP)
	chatLimiter chatRateLimiter
}

func NewServer(dir, version string, cfg Config, gatewayToken string, indexHTML []byte, serverCtx context.Context) *Server {
	content := string(indexHTML)
	preset := html.EscapeString(cfg.Theme.Preset)
	meta := "<head>\n<meta name=\"oc-theme\" content=\"" + preset + "\">"
	content = strings.Replace(content, "<head>", meta, 1)
	content = strings.ReplaceAll(content, "__VERSION__", html.EscapeString(version))
	content = strings.ReplaceAll(content, "__BILLING_MODE__", html.EscapeString(cfg.BillingMode))
	rendered := []byte(content)
	s := &Server{
		dir:                dir,
		version:            version,
		cfg:                cfg,
		gatewayToken:       gatewayToken,
		indexHTMLRendered:  rendered,
		indexContentLength: strconv.Itoa(len(rendered)),
		corsDefault:        "http://localhost:" + strconv.Itoa(cfg.Server.Port),
		httpClient:         &http.Client{Timeout: 60 * time.Second},
		systemSvc:          NewSystemService(cfg.System, version, serverCtx),
	}
	// Start periodic cleanup of stale rate-limit entries
	go func() {
		ticker := time.NewTicker(chatRateCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.chatLimiter.cleanup()
			case <-serverCtx.Done():
				return
			}
		}
	}()
	return s
}

// PreWarm runs refresh.sh once in the background at startup so data.json
// is ready before the first browser request arrives.
func (s *Server) PreWarm() {
	go func() {
		log.Printf("[dashboard] pre-warming data.json...")
		s.runRefresh()
		log.Printf("[dashboard] pre-warm complete")
	}()
}

// allowedStatic is a whitelist of static files the Go server will serve.
// This is intentionally restrictive — Python serves everything (including
// .git/config, server.py, config.json) which is a security risk.
var allowedStatic = map[string]string{
	"/themes.json":  "application/json",
	"/favicon.ico":  "image/x-icon",
	"/favicon.png":  "image/png",
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Accept both GET and HEAD for all read endpoints
	isRead := r.Method == http.MethodGet || r.Method == http.MethodHead

	switch {
	case isRead && (r.URL.Path == "/" || r.URL.Path == "/index.html"):
		s.handleIndex(w, r)
	case isRead && r.URL.Path == "/api/system":
		s.handleSystem(w, r)
	case isRead && strings.HasPrefix(r.URL.Path, "/api/refresh"):
		s.handleRefresh(w, r)
	case isRead && r.URL.Path == "/api/session-history":
		s.handleSessionHistory(w, r)
	case isRead && r.URL.Path == "/api/logs/stream":
		s.handleLogsStream(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/actions/run-cron":
		s.handleActionRunCron(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/actions/send-message":
		s.handleActionSendMessage(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/actions/restart-gateway":
		s.handleActionRestartGateway(w, r)
	case r.Method == http.MethodOptions:
		s.setCORSHeaders(w, r)
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/api/chat":
		s.handleChat(w, r)
	case isRead:
		// Serve allowlisted static files from disk
		if contentType, ok := allowedStatic[r.URL.Path]; ok {
			s.handleStaticFile(w, r, r.URL.Path, contentType)
			return
		}
		http.NotFound(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleStaticFile serves an allowlisted file from the dashboard directory.
func (s *Server) handleStaticFile(w http.ResponseWriter, r *http.Request, path, contentType string) {
	// Clean the path to prevent traversal
	clean := filepath.Clean(path)
	if clean != path || strings.Contains(clean, "..") {
		http.NotFound(w, r)
		return
	}
	fullPath := filepath.Join(s.dir, clean)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (s *Server) setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	} else {
		w.Header().Set("Access-Control-Allow-Origin", s.corsDefault)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Length", s.indexContentLength)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(s.indexHTMLRendered)
	}
}

// runRefresh executes refresh.sh once using exec.CommandContext.
// Prevents overlapping runs. Updates lastRefresh only on success.
func (s *Server) runRefresh() {
	s.mu.Lock()
	if s.refreshRunning {
		s.mu.Unlock()
		return
	}
	s.refreshRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.refreshRunning = false
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()

	script := filepath.Join(s.dir, "refresh.sh")
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Dir = s.dir

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("[dashboard] refresh.sh timed out after %s", refreshTimeout)
		} else {
			log.Printf("[dashboard] refresh.sh failed: %v", err)
		}
		return // do NOT update lastRefresh — allow retry
	}

	s.mu.Lock()
	s.lastRefresh = time.Now()
	s.mu.Unlock()
}

// loadData reads data.json with mtime-based caching, filling both raw bytes and
// parsed map atomically under one lock acquisition.  Merges the old
// getDataRawCached/getDataCached into a single cache layer to eliminate
// double-read on concurrent requests.
func (s *Server) loadData() ([]byte, map[string]any, error) {
	dataPath := filepath.Join(s.dir, "data.json")
	stat, err := os.Stat(dataPath)
	if err != nil {
		return nil, nil, err
	}
	mtime := stat.ModTime()

	s.dataMu.RLock()
	if s.cachedDataRaw != nil && s.cachedData != nil && !mtime.After(s.cachedDataMtime) {
		raw, parsed := s.cachedDataRaw, s.cachedData
		s.dataMu.RUnlock()
		return raw, parsed, nil
	}
	s.dataMu.RUnlock()

	raw, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return raw, nil, err
	}

	s.dataMu.Lock()
	// Double-check: another goroutine may have updated while we read/parsed
	if s.cachedDataRaw != nil && s.cachedData != nil && !mtime.After(s.cachedDataMtime) {
		raw, parsed = s.cachedDataRaw, s.cachedData
	} else {
		s.cachedDataRaw = raw
		s.cachedData = parsed
		s.cachedDataMtime = mtime
	}
	s.dataMu.Unlock()
	return raw, parsed, nil
}

// getDataRawCached returns cached data.json bytes — delegates to loadData().
func (s *Server) getDataRawCached() ([]byte, error) {
	raw, _, err := s.loadData()
	return raw, err
}

// handleRefresh implements stale-while-revalidate:
// Returns existing data.json immediately, triggers refresh in background if stale.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	debounce := time.Duration(s.cfg.Refresh.IntervalSeconds) * time.Second

	s.mu.Lock()
	shouldRun := !s.refreshRunning && time.Since(s.lastRefresh) >= debounce
	s.mu.Unlock()

	if shouldRun {
		go s.runRefresh()
	}

	data, err := s.getDataRawCached()
	if err != nil {
		if os.IsNotExist(err) {
			s.sendJSONRaw(w, r, http.StatusServiceUnavailable, errDataMissing)
		} else {
			s.sendJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "failed to read dashboard data"})
		}
		return
	}

	s.setCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	log.Printf("[dashboard] GET /api/refresh")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// getDataCached returns parsed data.json — delegates to loadData().
func (s *Server) getDataCached() map[string]any {
	_, parsed, err := s.loadData()
	if err != nil || parsed == nil {
		return map[string]any{}
	}
	return parsed
}

// handleChat handles the AI chat endpoint.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AI.Enabled {
		s.sendJSONRaw(w, r, http.StatusServiceUnavailable, errChatDisabled)
		return
	}

	// Rate limit: 10 req/min per IP
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx >= 0 {
		ip = ip[:idx]
	}
	if !s.chatLimiter.allow(ip) {
		w.Header().Set("Retry-After", "60")
		s.sendJSONRaw(w, r, http.StatusTooManyRequests, errChatRateLimit)
		return
	}

	defer r.Body.Close()
	lr := io.LimitReader(r.Body, int64(maxBodyBytes)+1)
	bodyBytes, err := io.ReadAll(lr)
	if err != nil {
		s.sendJSONRaw(w, r, http.StatusBadRequest, errBadBody)
		return
	}
	if len(bodyBytes) > maxBodyBytes {
		s.sendJSONRaw(w, r, http.StatusRequestEntityTooLarge, errBodyTooLarge)
		return
	}

	var req chatRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		s.sendJSONRaw(w, r, http.StatusBadRequest, errBadJSON)
		return
	}

	q := strings.TrimSpace(req.Question)
	if q == "" {
		s.sendJSONRaw(w, r, http.StatusBadRequest, errEmptyQ)
		return
	}
	if utf8.RuneCountInString(q) > maxQuestionLen {
		s.sendJSONRaw(w, r, http.StatusBadRequest, errQTooLong)
		return
	}

	// Validate + sanitise history — inline switch avoids per-request map alloc
	maxHist := s.cfg.AI.MaxHistory
	history := make([]chatMessage, 0, maxHist)
	start := len(req.History) - maxHist
	if start < 0 {
		start = 0
	}
	for _, msg := range req.History[start:] {
		switch msg.Role {
		case "user", "assistant":
		default:
			continue
		}
		content := msg.Content
		if utf8.RuneCountInString(content) > maxHistoryItem {
			// Truncate on rune boundary
			i := 0
			for j := range content {
				if i >= maxHistoryItem {
					content = content[:j]
					break
				}
				i++
			}
		}
		history = append(history, chatMessage{Role: msg.Role, Content: content})
	}

	// Use cached data.json — avoids re-reading + parsing ~100KB per request
	dashData := s.getDataCached()

	systemPrompt := buildSystemPrompt(dashData)
	answer, err := callGateway(
		r.Context(), systemPrompt, history, q,
		s.cfg.AI.GatewayPort,
		s.gatewayToken,
		s.cfg.AI.Model,
		s.httpClient,
	)
	if err != nil {
		log.Printf("[dashboard] POST /api/chat error: %v", err)
		status := http.StatusBadGateway
		if ge, ok := err.(*gatewayError); ok {
			status = ge.Status
		}
		s.sendJSON(w, r, status, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[dashboard] POST /api/chat")
	s.sendJSON(w, r, http.StatusOK, map[string]string{"answer": answer})
}

// sendJSON sends a JSON response with CORS headers (for dynamic payloads).
func (s *Server) sendJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, _ := json.Marshal(v)
	s.sendJSONRaw(w, r, status, body)
}

// sendJSONRaw sends pre-encoded JSON with CORS headers (zero-alloc for known responses).
// Respects HEAD method: sends headers but no body.
func (s *Server) sendJSONRaw(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	s.setCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// ── Session History endpoint ──
func (s *Server) handleSessionHistory(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	session := r.URL.Query().Get("session")
	limitStr := r.URL.Query().Get("limit")
	if agent == "" {
		agent = "main"
	}
	if session == "" {
		s.sendJSON(w, r, http.StatusBadRequest, map[string]string{"error": "session parameter required"})
		return
	}
	limit := 30
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	// Resolve session ID from sessions.json if needed
	sessionID := session
	home, _ := os.UserHomeDir()
	sessionsJSON := filepath.Join(home, ".openclaw", "agents", agent, "sessions", "sessions.json")
	if raw, err := os.ReadFile(sessionsJSON); err == nil {
		var sessMap map[string]json.RawMessage
		if json.Unmarshal(raw, &sessMap) == nil {
			// Try sessionKey lookup first
			if entry, ok := sessMap[session]; ok {
				var parsed struct {
					SessionID string `json:"sessionId"`
				}
				if json.Unmarshal(entry, &parsed) == nil && parsed.SessionID != "" {
					sessionID = parsed.SessionID
				}
			}
		}
	}

	jsonlPath := filepath.Join(home, ".openclaw", "agents", agent, "sessions", sessionID+".jsonl")
	f, err := os.Open(jsonlPath)
	if err != nil {
		s.sendJSON(w, r, http.StatusNotFound, map[string]string{"error": "session file not found"})
		return
	}
	defer f.Close()

	type historyMsg struct {
		Role      string `json:"role"`
		Timestamp string `json:"timestamp"`
		Text      string `json:"text"`
		Tokens    int    `json:"tokens,omitempty"`
	}

	var allMsgs []historyMsg
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			Usage struct {
				InputTokens  int `json:"inputTokens"`
				OutputTokens int `json:"outputTokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Type != "message" {
			continue
		}
		role := entry.Message.Role
		if role == "" || role == "system" {
			continue
		}

		// Parse content
		var text string
		// Try array of content blocks first
		var blocks []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ToolName string `json:"toolName"`
			Name     string `json:"name"`
		}
		if json.Unmarshal(entry.Message.Content, &blocks) == nil {
			var parts []string
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						parts = append(parts, b.Text)
					}
				case "toolCall", "tool_use":
					name := b.Name
					if name == "" {
						name = b.ToolName
					}
					if name != "" {
						parts = append(parts, "[tool: "+name+"]")
					}
				case "toolResult", "tool_result":
					name := b.ToolName
					if name == "" {
						name = b.Name
					}
					if name != "" {
						parts = append(parts, "[result: "+name+"]")
					} else if b.Text != "" {
						t := b.Text
						if len(t) > 200 {
							t = t[:200] + "…"
						}
						parts = append(parts, t)
					}
				}
			}
			text = strings.Join(parts, "\n")
		} else {
			// Try as plain string
			var plain string
			if json.Unmarshal(entry.Message.Content, &plain) == nil {
				text = plain
			}
		}

		if text == "" && role != "toolResult" {
			continue
		}

		// Truncate to 300 chars
		if utf8.RuneCountInString(text) > 300 {
			runes := []rune(text)
			text = string(runes[:300]) + "…"
		}

		tokens := entry.Usage.InputTokens + entry.Usage.OutputTokens
		allMsgs = append(allMsgs, historyMsg{
			Role:      role,
			Timestamp: entry.Timestamp,
			Text:      text,
			Tokens:    tokens,
		})
	}

	// Return last N messages
	if len(allMsgs) > limit {
		allMsgs = allMsgs[len(allMsgs)-limit:]
	}

	s.sendJSON(w, r, http.StatusOK, allMsgs)
}

// ── Action: Run Cron ──
func (s *Server) handleActionRunCron(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req struct {
		JobID string `json:"jobId"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.JobID == "" {
		s.sendJSON(w, r, http.StatusBadRequest, map[string]string{"error": "jobId required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/opt/homebrew/bin/openclaw", "cron", "run", req.JobID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.sendJSON(w, r, http.StatusInternalServerError, map[string]any{
			"ok":     false,
			"error":  err.Error(),
			"output": string(out),
		})
		return
	}
	s.sendJSON(w, r, http.StatusOK, map[string]any{"ok": true, "output": string(out)})
}

// ── Action: Send Message ──
func (s *Server) handleActionSendMessage(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req struct {
		SessionKey string `json:"sessionKey"`
		Message    string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil || req.Message == "" {
		s.sendJSON(w, r, http.StatusBadRequest, map[string]string{"error": "message required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if req.SessionKey != "" && req.SessionKey != "agent:main:main" {
		cmd = exec.CommandContext(ctx, "/opt/homebrew/bin/openclaw", "sessions", "send",
			"--session", req.SessionKey, "--message", req.Message)
	} else {
		cmd = exec.CommandContext(ctx, "/opt/homebrew/bin/openclaw", "agent",
			"--message", req.Message, "--json")
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		s.sendJSON(w, r, http.StatusInternalServerError, map[string]any{
			"ok":     false,
			"error":  err.Error(),
			"output": string(out),
		})
		return
	}
	s.sendJSON(w, r, http.StatusOK, map[string]any{"ok": true, "output": string(out)})
}

// ── Action: Restart Gateway ──
func (s *Server) handleActionRestartGateway(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/opt/homebrew/bin/openclaw", "gateway", "restart")
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.sendJSON(w, r, http.StatusInternalServerError, map[string]any{
			"ok":     false,
			"error":  err.Error(),
			"output": string(out),
		})
		return
	}
	s.sendJSON(w, r, http.StatusOK, map[string]any{"ok": true, "output": string(out)})
}

// ── Live Log Streamer (SSE) ──
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	s.setCORSHeaders(w, r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()

	type logSource struct {
		path   string
		source string
	}
	sources := []logSource{
		{"/tmp/dash.log", "dash"},
	}
	// Try to find gateway log
	home, _ := os.UserHomeDir()
	gwLogPaths := []string{
		filepath.Join(home, ".openclaw", "gateway.log"),
		"/tmp/openclaw-gateway.log",
	}
	for _, p := range gwLogPaths {
		if _, err := os.Stat(p); err == nil {
			sources = append(sources, logSource{p, "gateway"})
			break
		}
	}

	// Tail each source from end
	type tailState struct {
		path   string
		source string
		offset int64
	}
	var tails []tailState
	for _, src := range sources {
		info, err := os.Stat(src.path)
		offset := int64(0)
		if err == nil {
			offset = info.Size()
		}
		tails = append(tails, tailState{path: src.path, source: src.source, offset: offset})
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for i := range tails {
				t := &tails[i]
				f, err := os.Open(t.path)
				if err != nil {
					continue
				}
				info, err := f.Stat()
				if err != nil {
					f.Close()
					continue
				}
				// Handle file truncation (log rotation)
				if info.Size() < t.offset {
					t.offset = 0
				}
				if info.Size() == t.offset {
					f.Close()
					continue
				}
				f.Seek(t.offset, io.SeekStart)
				scanner := bufio.NewScanner(f)
				for scanner.Scan() {
					line := scanner.Text()
					ts := time.Now().UTC().Format(time.RFC3339)
					evt := fmt.Sprintf(`{"ts":"%s","line":"%s","source":"%s"}`,
						ts,
						strings.ReplaceAll(strings.ReplaceAll(line, `\`, `\\`), `"`, `\"`),
						t.source)
					fmt.Fprintf(w, "data: %s\n\n", evt)
				}
				t.offset, _ = f.Seek(0, io.SeekCurrent)
				f.Close()
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.System.Enabled {
		body := []byte(`{"ok":false,"error":"system metrics disabled"}`)
		s.setCORSHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusServiceUnavailable)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
		return
	}
	ctx := r.Context()
	status, body := s.systemSvc.GetJSON(ctx)
	s.setCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
