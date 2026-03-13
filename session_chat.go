package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ── Session Chat Rate Limiter ──
const (
	sessionSendRateLimit   = 6
	sessionSendRateWindow  = 1 * time.Minute
)

type sessionChatLimiter struct {
	entries sync.Map
}

type sessionRateBucket struct {
	mu        sync.Mutex
	tokens    int
	lastReset time.Time
}

func (rl *sessionChatLimiter) allow(ip string) bool {
	now := time.Now()
	val, _ := rl.entries.LoadOrStore(ip, &sessionRateBucket{
		tokens:    sessionSendRateLimit,
		lastReset: now,
	})
	bucket := val.(*sessionRateBucket)
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	if now.Sub(bucket.lastReset) >= sessionSendRateWindow {
		bucket.tokens = sessionSendRateLimit
		bucket.lastReset = now
	}
	if bucket.tokens <= 0 {
		return false
	}
	bucket.tokens--
	return true
}

// ── SSE Event ──
type sessionSSEEvent struct {
	Role string `json:"role"`
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"`
	Ts   string `json:"ts"`
	ID   string `json:"id"`
}

// ── findActiveSessionJSONL finds the newest .jsonl file in the main sessions dir ──
func findActiveSessionJSONL() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type fileInfo struct {
		path  string
		mtime time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			path:  filepath.Join(dir, e.Name()),
			mtime: info.ModTime(),
		})
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no JSONL session files found")
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.After(files[j].mtime)
	})
	return files[0].path, nil
}

// ── parseJSONLLine parses a JSONL line into an SSE event ──
func parseJSONLLine(line []byte) (*sessionSSEEvent, bool) {
	var entry struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, false
	}
	if entry.Type != "message" {
		return nil, false
	}
	role := entry.Message.Role
	if role == "" || role == "system" || role == "toolResult" || role == "toolCall" {
		return nil, false
	}

	evt := &sessionSSEEvent{
		ID: entry.ID,
		Ts: entry.Timestamp,
	}

	// Parse content blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(entry.Message.Content, &blocks); err == nil {
		// Collect text blocks first
		var parts []string
		var toolName string
		hasToolCall := false
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
			if b.Type == "tool_use" || b.Type == "toolCall" {
				hasToolCall = true
				if b.Name != "" {
					toolName = b.Name
				}
			}
		}
		// Prefer text; fall back to tool event only if no text
		if len(parts) > 0 {
			evt.Text = strings.Join(parts, "\n")
			evt.Role = role
			return evt, true
		}
		if hasToolCall && role == "assistant" {
			evt.Role = "tool"
			evt.Name = toolName
			return evt, true
		}
		return nil, false
	}

	// Try plain string
	var plain string
	if err := json.Unmarshal(entry.Message.Content, &plain); err == nil {
		evt.Role = role
		evt.Text = plain
		return evt, true
	}

	return nil, false
}

// ── readLastNMessages reads the last N messages from a JSONL file ──
func readLastNMessages(path string, n int) []sessionSSEEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var events []sessionSSEEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if evt, ok := parseJSONLLine(line); ok {
			events = append(events, *evt)
		}
	}
	if len(events) > n {
		events = events[len(events)-n:]
	}
	return events
}

// ── handleSessionSend — POST /api/session/send ──
func (s *Server) handleSessionSend(w http.ResponseWriter, r *http.Request) {
	// Rate limit per IP
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx >= 0 {
		ip = ip[:idx]
	}
	if !s.sessionLimiter.allow(ip) {
		w.Header().Set("Retry-After", "60")
		s.sendJSON(w, r, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}

	// Parse multipart form (max 50MB total)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		s.sendJSON(w, r, http.StatusBadRequest, map[string]string{"error": "failed to parse form"})
		return
	}

	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" {
		s.sendJSON(w, r, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}
	if len([]rune(message)) > 4000 {
		s.sendJSON(w, r, http.StatusBadRequest, map[string]string{"error": "message too long (max 4000 chars)"})
		return
	}

	// Handle file uploads
	reqID := newUUID()
	uploadDir := filepath.Join("/tmp", "dashboard-uploads", reqID)

	files := r.MultipartForm.File["files[]"]
	if len(files) > 5 {
		files = files[:5]
	}

	var appendedParts []string
	for _, fh := range files {
		if fh.Size > 10<<20 {
			continue // skip files > 10MB
		}
		src, err := fh.Open()
		if err != nil {
			continue
		}
		if err := os.MkdirAll(uploadDir, 0700); err != nil {
			src.Close()
			continue
		}
		dstPath := filepath.Join(uploadDir, filepath.Base(fh.Filename))
		dst, err := os.Create(dstPath)
		if err != nil {
			src.Close()
			continue
		}
		io.Copy(dst, src)
		src.Close()
		dst.Close()

		// Determine file type annotation
		name := strings.ToLower(fh.Filename)
		var annotation string
		switch {
		case strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") ||
			strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".gif") ||
			strings.HasSuffix(name, ".webp"):
			annotation = fmt.Sprintf("\n[Image attached — analyze via image tool: %s]", dstPath)
		case strings.HasSuffix(name, ".pdf"):
			annotation = fmt.Sprintf("\n[PDF attached — read via pdf tool: %s]", dstPath)
		default:
			annotation = fmt.Sprintf("\n[File attached — read via Read tool: %s]", dstPath)
		}
		appendedParts = append(appendedParts, annotation)
	}

	finalMessage := message + strings.Join(appendedParts, "")

	// Fire and forget — send message to OpenClaw main session
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "openclaw", "agent", "--message", finalMessage, "--session-id", "dashboard-chat-session")
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[session-chat] send error: %v — %s", err, string(out))
		}
	}()

	s.sendJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
}

// ── handleSessionStream — GET /api/session/stream (SSE) ──
func (s *Server) handleSessionStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	s.setCORSHeaders(w, r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()

	// Use dedicated dashboard chat session
	home, _ := os.UserHomeDir()
	jsonlPath := filepath.Join(home, ".openclaw", "agents", "main", "sessions", "dashboard-chat-session.jsonl")
	if _, ferr := os.Stat(jsonlPath); os.IsNotExist(ferr) {
		if f, cerr := os.Create(jsonlPath); cerr == nil { f.Close() }
	}

	// Send history (last 30)
	history := readLastNMessages(jsonlPath, 30)
	for _, evt := range history {
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

	// Get current file size to start tailing from
	var offset int64
	if fi, err := os.Stat(jsonlPath); err == nil {
		offset = fi.Size()
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-ticker.C:
			fi, err := os.Stat(jsonlPath)
			if err != nil {
				continue
			}
			if fi.Size() < offset {
				offset = 0 // truncation/rotation
			}
			if fi.Size() == offset {
				continue
			}

			f, err := os.Open(jsonlPath)
			if err != nil {
				continue
			}
			f.Seek(offset, io.SeekStart)
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)
			for scanner.Scan() {
				line := scanner.Bytes()
				if evt, ok := parseJSONLLine(line); ok {
					data, _ := json.Marshal(evt)
					fmt.Fprintf(w, "data: %s\n\n", data)
				}
			}
			offset, _ = f.Seek(0, io.SeekCurrent)
			f.Close()
			flusher.Flush()
		}
	}
}

// ── handleSessionChatHistory — GET /api/session/history ──
func (s *Server) handleSessionChatHistory(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	home, _ := os.UserHomeDir()
	jsonlPath := filepath.Join(home, ".openclaw", "agents", "main", "sessions", "dashboard-chat-session.jsonl")
	if _, serr := os.Stat(jsonlPath); os.IsNotExist(serr) {
		s.sendJSON(w, r, http.StatusOK, []interface{}{})
		return
	}

	events := readLastNMessages(jsonlPath, limit)
	s.sendJSON(w, r, http.StatusOK, events)
}
