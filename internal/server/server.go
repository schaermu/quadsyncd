// Package server implements the HTTP server for the webhook daemon and Web UI.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/runstore"
	"github.com/schaermu/quadsyncd/internal/service"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
	"github.com/schaermu/quadsyncd/internal/systemduser"
	"github.com/schaermu/quadsyncd/internal/webui"
)

// Server implements the webhook HTTP server and Web UI.
type Server struct {
	cfg           *config.Config
	runnerFactory quadsyncd.RunnerFactory
	systemd       systemduser.Systemd
	logger        *slog.Logger
	store         runstore.ReadWriter
	broadcaster   *Broadcaster
	secret        []byte
	syncSvc       *service.SyncService
	planSvc       *service.PlanService
	debounce      *debouncer
	uiHandler     http.Handler // serves embedded SPA assets
}

// NewServer creates a new webhook/API server.
func NewServer(cfg *config.Config, runnerFactory quadsyncd.RunnerFactory, systemd systemduser.Systemd, store runstore.ReadWriter, logger *slog.Logger) (*Server, error) {
	if store == nil {
		return nil, fmt.Errorf("runstore.ReadWriter cannot be nil")
	}
	// Reject typed-nil pointers wrapped in the interface (e.g. (*runstore.Store)(nil)).
	if v := reflect.ValueOf(store); v.Kind() == reflect.Ptr && v.IsNil() {
		return nil, fmt.Errorf("runstore.ReadWriter is a nil pointer wrapped in an interface")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if systemd == nil {
		return nil, fmt.Errorf("systemd client cannot be nil")
	}
	if runnerFactory == nil {
		return nil, fmt.Errorf("runner factory cannot be nil")
	}

	// Load webhook secret from file; trim surrounding whitespace/newlines.
	secretData, err := os.ReadFile(cfg.Serve.GitHubWebhookSecretFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read webhook secret: %w", err)
	}
	secret := []byte(strings.TrimSpace(string(secretData)))

	s := &Server{
		cfg:           cfg,
		runnerFactory: runnerFactory,
		systemd:       systemd,
		logger:        logger,
		store:         store,
		secret:        secret,
	}

	// Initialise service layer.
	s.syncSvc = service.NewSyncService(cfg, runnerFactory, store, logger, secret)
	s.planSvc = service.NewPlanService(cfg, runnerFactory, store, logger, secret)

	// Initialise the SSE broadcaster watching the runs directory.
	runsDir := filepath.Join(cfg.Paths.StateDir, "runs")
	s.broadcaster = newBroadcaster(runsDir, logger, defaultBroadcastInterval)

	// Initialise the embedded SPA file server.
	uiFS, err := webui.FS()
	if err != nil {
		return nil, fmt.Errorf("failed to open embedded webui assets: %w", err)
	}
	s.uiHandler = http.FileServer(http.FS(uiFS))

	// Initialise the webhook debouncer with a 2-second delay.
	s.debounce = &debouncer{delay: 2 * time.Second}

	return s, nil
}

// Start binds to the configured address and starts the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.Serve.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to bind to %s: %w", s.cfg.Serve.ListenAddr, err)
	}
	s.logger.Info("webhook server bound to address", "addr", s.cfg.Serve.ListenAddr)
	return s.StartWithListener(ctx, listener)
}

// StartWithListener starts the HTTP server using a provided listener (supports
// systemd socket activation). It performs an initial sync before accepting traffic.
func (s *Server) StartWithListener(ctx context.Context, listener net.Listener) error {
	s.logger.Info("performing initial sync before starting webhook server")
	s.syncSvc.TriggerSync(ctx, runstore.TriggerStartup)

	// Start the SSE broadcaster in the background.
	go s.broadcaster.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/assets/", s.handleAssets)
	mux.HandleFunc("/api/plan", s.handlePlan)
	mux.HandleFunc("/api/", s.handleAPI)

	httpServer := &http.Server{
		Handler:           securityHeadersMiddleware(csrfMiddleware(mux)),
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		// WriteTimeout is left at 30 s here; SSE connections clear their own
		// write deadline via http.ResponseController inside handleEvents.
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("webhook server starting", "addr", listener.Addr().String())
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		s.logger.Info("shutting down webhook server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
