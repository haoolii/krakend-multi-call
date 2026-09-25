package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultTimeoutDelay = 31 * time.Second

var fabIDs = []string{
	"FAB_A", "FAB_B", "FAB_C", "FAB_D", "FAB_E",
	"FAB_F", "FAB_G", "FAB_H", "FAB_I", "FAB_J",
}

type behavior struct {
	Mode    string        `json:"mode"`
	DelayMS time.Duration `json:"-"`
}

type behaviorResponse struct {
	Mode    string `json:"mode"`
	DelayMS int64  `json:"delayMs"`
}

type server struct {
	mu        sync.RWMutex
	behaviors map[string]behavior
}

func main() {
	s := &server{behaviors: make(map[string]behavior, len(fabIDs))}
	s.reset()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /fabs/{fab}/settings", s.settings)
	mux.HandleFunc("GET /admin/state", s.state)
	mux.HandleFunc("PUT /admin/fabs/{fab}", s.configure)
	mux.HandleFunc("POST /admin/reset", s.resetHandler)

	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("fab settings mock listening on %s", httpServer.Addr)
	log.Fatal(httpServer.ListenAndServe())
}

func (s *server) settings(w http.ResponseWriter, r *http.Request) {
	fab := r.PathValue("fab")
	b, err := s.getBehavior(fab)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": err.Error()})
		return
	}

	if b.DelayMS > 0 {
		select {
		case <-time.After(b.DelayMS):
		case <-r.Context().Done():
			return
		}
	}

	if b.Mode == "error" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "FAB_SETTINGS_UNAVAILABLE",
			"message": fmt.Sprintf("settings service for %s is unavailable", fab),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fab":      fab,
		"timezone": "Asia/Taipei",
		"features": map[string]bool{
			"autoDispatch": true,
			"maintenance":  false,
		},
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *server) configure(w http.ResponseWriter, r *http.Request) {
	fab := r.PathValue("fab")
	if !validFab(fab) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "unknown fab: " + fab})
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode != "success" && mode != "error" && mode != "timeout" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "mode must be success, error, or timeout"})
		return
	}

	delay := time.Duration(0)
	if mode == "timeout" {
		delay = defaultTimeoutDelay
	}
	if rawDelay := r.URL.Query().Get("delay_ms"); rawDelay != "" {
		value, err := strconv.Atoi(rawDelay)
		if err != nil || value < 0 || value > 30000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "delay_ms must be between 0 and 30000"})
			return
		}
		delay = time.Duration(value) * time.Millisecond
	}

	s.mu.Lock()
	s.behaviors[fab] = behavior{Mode: mode, DelayMS: delay}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, behaviorResponse{Mode: mode, DelayMS: delay.Milliseconds()})
}

func (s *server) state(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	state := make(map[string]behaviorResponse, len(s.behaviors))
	for fab, b := range s.behaviors {
		state[fab] = behaviorResponse{Mode: b.Mode, DelayMS: b.DelayMS.Milliseconds()}
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, state)
}

func (s *server) resetHandler(w http.ResponseWriter, _ *http.Request) {
	s.reset()
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *server) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, fab := range fabIDs {
		s.behaviors[fab] = behavior{Mode: "success"}
	}
}

func (s *server) getBehavior(fab string) (behavior, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.behaviors[fab]
	if !ok {
		return behavior{}, errors.New("unknown fab: " + fab)
	}
	return b, nil
}

func validFab(candidate string) bool {
	for _, fab := range fabIDs {
		if fab == candidate {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/health") {
			log.Printf("%s %s duration=%s", r.Method, r.URL.RequestURI(), time.Since(started))
		}
	})
}
