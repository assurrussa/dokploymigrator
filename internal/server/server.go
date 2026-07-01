// Package server exposes the Web UI and JSON API.
package server

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/assurrussa/dokploymigrator/internal/jobs"
	"github.com/assurrussa/dokploymigrator/internal/model"
	"github.com/assurrussa/dokploymigrator/internal/state"
)

const (
	statusKey             = "status"
	jobHistoryPageSize    = 50
	protectedJobCount     = 50
	maxJobHistoryPageSize = 50
)

//go:embed static/*
var staticFiles embed.FS

// Config controls HTTP auth.
type Config struct {
	BasicUser     string
	BasicPassword string
	AdminToken    string
	DeadAfter     time.Duration
}

// Server is the HTTP API and embedded UI.
type Server struct {
	cfg     Config
	store   *state.Store
	manager *jobs.Manager
}

// New creates a server.
func New(cfg Config, store *state.Store, manager *jobs.Manager) *Server {
	return &Server{cfg: cfg, store: store, manager: manager}
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/servers", s.handleServers)
	mux.HandleFunc("GET /api/jobs", s.handleJobs)
	mux.HandleFunc("DELETE /api/jobs/{id}", s.handleDeleteJob)
	mux.HandleFunc("POST /api/plan", s.handlePlan)
	mux.HandleFunc("POST /api/apply", s.handleApply)
	mux.HandleFunc("POST /api/rollback", s.handleRollback)

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(fmt.Sprintf("static fs: %v", err))
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return s.basicAuth(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{statusKey: "ok"})
}

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	serverList, err := s.manager.ListServers(r.Context(), s.cfg.DeadAfter)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, serverList)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	limit := boundedQueryInt(r, "limit", jobHistoryPageSize, maxJobHistoryPageSize)
	offset := nonNegativeQueryInt(r, "offset", 0)
	jobList, err := s.store.ListJobsPage(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	total, err := s.store.CountJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, jobsResponse{
		Jobs:           jobList,
		Total:          total,
		Limit:          limit,
		Offset:         offset,
		ProtectedCount: protectedJobCount,
	})
}

type jobsResponse struct {
	Jobs           []state.Job `json:"jobs"`
	Total          int         `json:"total"`
	Limit          int         `json:"limit"`
	Offset         int         `json:"offset"`
	ProtectedCount int         `json:"protectedCount"`
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminToken(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("job id is required"))
		return
	}
	protectedIDs, err := s.store.ProtectedJobIDs(r.Context(), protectedJobCount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, ok := protectedIDs[id]; ok {
		writeError(w, http.StatusConflict, fmt.Errorf("latest %d jobs cannot be deleted", protectedJobCount))
		return
	}
	if err := s.store.DeleteJob(r.Context(), id); err != nil {
		if errors.Is(err, state.ErrJobNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{statusKey: "deleted"})
}

type planRequest struct {
	SourceServerID string              `json:"sourceServerId"`
	TargetServerID string              `json:"targetServerId"`
	Mode           model.MigrationMode `json:"mode"`
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminToken(w, r) {
		return
	}
	var req planRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Mode == "" {
		req.Mode = model.ModeDeadRecovery
	}
	job, plan, err := s.manager.Plan(r.Context(), req.SourceServerID, req.TargetServerID, req.Mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "plan": plan})
}

type planActionRequest struct {
	JobID              string              `json:"jobId"`
	Plan               model.MigrationPlan `json:"plan"`
	SchemaHashApproval string              `json:"schemaHashApproval"`
	ConfirmationText   string              `json:"confirmationText"`
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminToken(w, r) {
		return
	}
	var req planActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	opts := jobs.ApplyOptions{
		SchemaHashApproval: req.SchemaHashApproval,
		ConfirmationText:   req.ConfirmationText,
	}
	if err := s.manager.Apply(r.Context(), req.JobID, req.Plan, opts); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{statusKey: "applied"})
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminToken(w, r) {
		return
	}
	var req planActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.manager.Rollback(r.Context(), req.JobID, req.Plan, jobs.RollbackOptions{
		SchemaHashApproval: req.SchemaHashApproval,
	}); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{statusKey: "rolled_back"})
}

func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.BasicUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.BasicPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="dokploy-migrator"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) checkAdminToken(w http.ResponseWriter, r *http.Request) bool {
	got := r.Header.Get("X-Migrator-Admin-Token")
	if s.cfg.AdminToken == "" || got == "" ||
		subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminToken)) != 1 {
		writeError(w, http.StatusForbidden, errors.New("invalid admin token"))
		return false
	}
	return true
}

func boundedQueryInt(r *http.Request, name string, fallback int, maxValue int) int {
	value := nonNegativeQueryInt(r, name, fallback)
	if value <= 0 {
		return fallback
	}
	if maxValue > 0 && value > maxValue {
		return maxValue
	}
	return value
}

func nonNegativeQueryInt(r *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(body, '\n')); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// ListenAndServe starts the HTTP server.
func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), httpShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

const (
	httpShutdownTimeout = 5 * time.Second
	readHeaderTimeout   = 5 * time.Second
)
