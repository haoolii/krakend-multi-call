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

const (
	defaultTimeoutDelay = 4 * time.Second
	defaultPageItems    = 3
	defaultPayloadKB    = 1024
)

var fabIDs = []string{
	"FAB_A", "FAB_B", "FAB_C", "FAB_D", "FAB_E",
	"FAB_F", "FAB_G", "FAB_H", "FAB_I", "FAB_J", "FAB_WRONG",
}

type behavior struct {
	Mode      string
	DelayMS   time.Duration
	PageItems int
	PayloadKB int
}

type behaviorResponse struct {
	Mode      string `json:"mode"`
	DelayMS   int64  `json:"delayMs"`
	PageItems int    `json:"pageItems"`
	PayloadKB int    `json:"payloadKB"`
}

type stateResponse struct {
	Fabs          map[string]behaviorResponse `json:"fabs"`
	Calls         map[string]int              `json:"calls"`
	Active        int                         `json:"active"`
	ActiveByFab   map[string]int              `json:"activeByFab"`
	PeakActive    int                         `json:"peakActive"`
	CanceledTotal int                         `json:"canceledTotal"`
}

type fabItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Fab  string `json:"fab"`
	Page []any  `json:"page"`
}

type server struct {
	mu            sync.RWMutex
	behaviors     map[string]behavior
	calls         map[string]int
	active        int
	activeByFab   map[string]int
	peakActive    int
	canceledTotal int
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	s := &server{}
	s.reset()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /fabs/{fab}/settings", s.settings)
	mux.HandleFunc("GET /admin/state", s.state)
	mux.HandleFunc("GET /admin/calls", s.callState)
	mux.HandleFunc("PUT /admin/fabs/{fab}", s.configureFab)
	mux.HandleFunc("POST /admin/reset", s.resetHandler)

	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("fab mock listening on %s", httpServer.Addr)
	log.Fatal(httpServer.ListenAndServe())
}

func (s *server) settings(w http.ResponseWriter, r *http.Request) {
	fab := r.PathValue("fab")
	b, err := s.recordCallAndGetBehavior(fab)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": err.Error()})
		return
	}
	s.beginRequest(fab)
	defer s.endRequest(fab)

	if b.Mode == "ignore_cancel" {
		time.Sleep(b.DelayMS)
	} else if b.Mode != "slow_body" && !waitForDelay(r, b.DelayMS) {
		s.recordCanceled()
		return
	}
	if b.Mode == "hang" {
		<-r.Context().Done()
		s.recordCanceled()
		return
	}
	if b.Mode == "error" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "FAB_UNAVAILABLE",
			"message": fmt.Sprintf("service for %s is unavailable", fab),
		})
		return
	}
	if fab == "FAB_WRONG" {
		// Keeps the root array contract but intentionally violates the item contract.
		writeJSON(w, http.StatusOK, []map[string]any{
			{
				"id":           "not-an-integer",
				"name":         []string{"not-a-string"},
				"fab":          "WRONG",
				"unexpected":   true,
				"wrongPayload": map[string]any{"nested": "unrecognized field"},
			},
		})
		return
	}

	fabName := strings.TrimPrefix(fab, "FAB_")
	page := buildPage(b.PageItems, b.PayloadKB)
	response := []fabItem{
		{ID: 1, Name: "example-1", Fab: fabName, Page: page},
		{ID: 2, Name: "example-2", Fab: fabName, Page: page},
	}
	if b.Mode == "slow_body" {
		if !writeJSONAfterHeaders(r, w, b.DelayMS, response) {
			s.recordCanceled()
		}
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) configureFab(w http.ResponseWriter, r *http.Request) {
	fab := r.PathValue("fab")
	if fab != "all" && !validFab(fab) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "unknown fab: " + fab})
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode != "success" && mode != "error" && mode != "timeout" && mode != "hang" && mode != "ignore_cancel" && mode != "slow_body" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "mode must be success, error, timeout, hang, ignore_cancel, or slow_body"})
		return
	}

	defaultDelay := time.Duration(0)
	if mode == "timeout" {
		defaultDelay = defaultTimeoutDelay
	}
	delay, err := parseDelay(r, defaultDelay)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	pageItems, err := parseRange(r, "page_items", 0, 100)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	payloadKB, err := parseRange(r, "payload_kb", 0, 1024)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	configured := behavior{Mode: mode, DelayMS: delay, PageItems: pageItems, PayloadKB: payloadKB}

	s.mu.Lock()
	if fab == "all" {
		for _, id := range fabIDs {
			s.behaviors[id] = configured
		}
	} else {
		s.behaviors[fab] = configured
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, behaviorResponse{
		Mode: mode, DelayMS: delay.Milliseconds(), PageItems: pageItems, PayloadKB: payloadKB,
	})
}

func (s *server) state(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	fabs := make(map[string]behaviorResponse, len(s.behaviors))
	for fab, b := range s.behaviors {
		fabs[fab] = behaviorResponse{Mode: b.Mode, DelayMS: b.DelayMS.Milliseconds(), PageItems: b.PageItems, PayloadKB: b.PayloadKB}
	}
	calls := make(map[string]int, len(s.calls))
	for fab, count := range s.calls {
		calls[fab] = count
	}
	activeByFab := make(map[string]int, len(s.activeByFab))
	for fab, count := range s.activeByFab {
		activeByFab[fab] = count
	}
	active, peakActive, canceledTotal := s.active, s.peakActive, s.canceledTotal
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, stateResponse{
		Fabs: fabs, Calls: calls, Active: active, ActiveByFab: activeByFab, PeakActive: peakActive, CanceledTotal: canceledTotal,
	})
}

func (s *server) callState(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	calls := make(map[string]int, len(s.calls))
	for fab, count := range s.calls {
		calls[fab] = count
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, calls)
}

func (s *server) resetHandler(w http.ResponseWriter, _ *http.Request) {
	s.reset()
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *server) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.behaviors = make(map[string]behavior, len(fabIDs))
	s.calls = make(map[string]int, len(fabIDs))
	if s.activeByFab == nil {
		s.activeByFab = make(map[string]int, len(fabIDs))
	}
	s.peakActive = s.active
	s.canceledTotal = 0
	for _, fab := range fabIDs {
		s.behaviors[fab] = behavior{
			Mode:      "success",
			PageItems: defaultPageItems,
			PayloadKB: defaultPayloadKB,
		}
		s.calls[fab] = 0
		if _, ok := s.activeByFab[fab]; !ok {
			s.activeByFab[fab] = 0
		}
	}
}

func (s *server) recordCallAndGetBehavior(fab string) (behavior, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.behaviors[fab]
	if !ok {
		return behavior{}, errors.New("unknown fab: " + fab)
	}
	s.calls[fab]++
	return b, nil
}

func (s *server) beginRequest(fab string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active++
	s.activeByFab[fab]++
	if s.active > s.peakActive {
		s.peakActive = s.active
	}
}

func (s *server) endRequest(fab string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active > 0 {
		s.active--
	}
	if s.activeByFab[fab] > 0 {
		s.activeByFab[fab]--
	}
}

func (s *server) recordCanceled() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canceledTotal++
}

func (s *server) metrics(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	activeByFab := make(map[string]int, len(s.activeByFab))
	for fab, count := range s.activeByFab {
		activeByFab[fab] = count
	}
	active, peakActive, canceledTotal := s.active, s.peakActive, s.canceledTotal
	calls := make(map[string]int, len(s.calls))
	for fab, count := range s.calls {
		calls[fab] = count
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintln(w, "# TYPE fab_mock_active_requests gauge")
	_, _ = fmt.Fprintf(w, "fab_mock_active_requests %d\n", active)
	_, _ = fmt.Fprintln(w, "# TYPE fab_mock_active_requests_by_fab gauge")
	for _, fab := range fabIDs {
		_, _ = fmt.Fprintf(w, "fab_mock_active_requests_by_fab{fab=%q} %d\n", fab, activeByFab[fab])
	}
	_, _ = fmt.Fprintln(w, "# TYPE fab_mock_peak_active_requests gauge")
	_, _ = fmt.Fprintf(w, "fab_mock_peak_active_requests %d\n", peakActive)
	_, _ = fmt.Fprintln(w, "# TYPE fab_mock_canceled_requests_total counter")
	_, _ = fmt.Fprintf(w, "fab_mock_canceled_requests_total %d\n", canceledTotal)
	_, _ = fmt.Fprintln(w, "# TYPE fab_mock_calls_total counter")
	for _, fab := range fabIDs {
		_, _ = fmt.Fprintf(w, "fab_mock_calls_total{fab=%q} %d\n", fab, calls[fab])
	}
}

func parseDelay(r *http.Request, defaultDelay time.Duration) (time.Duration, error) {
	rawDelay := r.URL.Query().Get("delay_ms")
	if rawDelay == "" {
		return defaultDelay, nil
	}
	value, err := strconv.Atoi(rawDelay)
	if err != nil || value < 0 || value > 30000 {
		return 0, errors.New("delay_ms must be between 0 and 30000")
	}
	return time.Duration(value) * time.Millisecond, nil
}

func parseRange(r *http.Request, name string, minimum, maximum int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return value, nil
}

func buildPage(items, payloadKB int) []any {
	page := make([]any, 0, items)
	payload := strings.Repeat("x", payloadKB*1024)
	for id := 1; id <= items; id++ {
		page = append(page, map[string]any{
			"id":      id,
			"title":   fmt.Sprintf("page-%d", id),
			"payload": payload,
		})
	}
	return page
}

func waitForDelay(r *http.Request, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	select {
	case <-time.After(delay):
		return true
	case <-r.Context().Done():
		return false
	}
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

func writeJSONAfterHeaders(r *http.Request, w http.ResponseWriter, delay time.Duration, value any) bool {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if !waitForDelay(r, delay) {
		return false
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode delayed response: %v", err)
	}
	return true
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = "missing"
		}
		log.Printf("event=fab.request.start request_id=%s method=%s path=%s", requestID, r.Method, r.URL.RequestURI())
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		log.Printf("event=fab.request.end request_id=%s method=%s path=%s status=%d duration=%s", requestID, r.Method, r.URL.RequestURI(), recorder.status, time.Since(started))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
