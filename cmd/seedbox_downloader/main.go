package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi"
	"github.com/italolelis/seedbox_downloader/internal/dc/putio"
	"github.com/italolelis/seedbox_downloader/internal/downloader"
	"github.com/italolelis/seedbox_downloader/internal/http/rest"
	"github.com/italolelis/seedbox_downloader/internal/http/ui"
	"github.com/italolelis/seedbox_downloader/internal/logctx"
	"github.com/italolelis/seedbox_downloader/internal/notifier"
	"github.com/italolelis/seedbox_downloader/internal/storage"
	"github.com/italolelis/seedbox_downloader/internal/storage/sqlite"
	"github.com/italolelis/seedbox_downloader/internal/svc/arr"
	"github.com/italolelis/seedbox_downloader/internal/svc/transfers"
	"github.com/italolelis/seedbox_downloader/internal/telemetry"
	"github.com/italolelis/seedbox_downloader/internal/transfer"
	"github.com/italolelis/seedbox_downloader/internal/version"
	"github.com/kelseyhightower/envconfig"
)

// Config struct for environment variables.
type config struct {
	DownloadClient string `envconfig:"DOWNLOAD_CLIENT" default:"putio"`

	PutioToken     string  `envconfig:"PUTIO_TOKEN"`
	PutioBaseDir   string  `envconfig:"PUTIO_BASE_DIR"`
	PutioSeedRatio float64 `envconfig:"PUTIO_SEED_RATIO" default:"0"`

	TargetLabel       string         `envconfig:"TARGET_LABEL"`
	DownloadDir       string         `envconfig:"DOWNLOAD_DIR" required:"true"`
	KeepDownloadedFor time.Duration  `envconfig:"KEEP_DOWNLOADED_FOR" default:"24h"`
	PollingInterval   time.Duration  `envconfig:"POLLING_INTERVAL" default:"10m"`
	CleanupInterval   time.Duration  `envconfig:"CLEANUP_INTERVAL" default:"10m"`
	LogLevel          *slog.LevelVar `envconfig:"LOG_LEVEL" default:"INFO"`
	DiscordWebhookURL string         `envconfig:"DISCORD_WEBHOOK_URL"`
	DBPath            string         `envconfig:"DB_PATH" default:"downloads.db"`
	DBMaxOpenConns    int            `envconfig:"DB_MAX_OPEN_CONNS" default:"25"`
	DBMaxIdleConns    int            `envconfig:"DB_MAX_IDLE_CONNS" default:"5"`
	MaxParallel       int            `envconfig:"MAX_PARALLEL" default:"5"`

	Transmission struct {
		Username string `split_words:"true"`
		Password string `split_words:"true"`
	}

	Web struct {
		BindAddress     string        `split_words:"true" default:"0.0.0.0:9091"`
		ReadTimeout     time.Duration `split_words:"true" default:"30s"`
		WriteTimeout    time.Duration `split_words:"true" default:"30s"`
		IdleTimeout     time.Duration `split_words:"true" default:"5s"`
		ShutdownTimeout time.Duration `split_words:"true" default:"30s"`
	}

	// UI configures the human-facing Web UI/API server, served on a dedicated port
	// separate from the Transmission RPC consumed by Sonarr/Radarr.
	UI struct {
		Enabled     bool   `split_words:"true" default:"true"`
		BindAddress string `split_words:"true" default:"0.0.0.0:9092"`
		Username    string `split_words:"true"`
		Password    string `split_words:"true"`
	}

	Telemetry struct {
		Enabled     bool   `split_words:"true" default:"true"`
		OTELAddress string `split_words:"true" default:"0.0.0.0:4317"`
		ServiceName string `split_words:"true" default:"seedbox_downloader"`
	}

	Sonarr arrConfig `envconfig:"SONARR"`
	Radarr arrConfig `envconfig:"RADARR"`
}

type arrConfig struct {
	APIKey  string `envconfig:"API_KEY"`
	BaseURL string `envconfig:"BASE_URL"`
}

func main() {
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Info())
		os.Exit(0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := run(ctx); err != nil {
		slog.ErrorContext(ctx, "fatal error", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, logger, err := initializeConfig()
	if err != nil {
		return err
	}

	ctx = logctx.WithLogger(ctx, logger)
	logger = logger.WithGroup("main")

	logger.InfoContext(ctx, "starting putioarr", "version", version.String(), "build", version.Info())

	// Log configuration loaded with safe values (no secrets)
	logger.InfoContext(ctx, "configuration loaded",
		"version", version.Version,
		"log_level", cfg.LogLevel,
		"target_label", cfg.TargetLabel,
		"download_dir", cfg.DownloadDir,
		"polling_interval", cfg.PollingInterval.String(),
		"cleanup_interval", cfg.CleanupInterval.String(),
		"keep_downloaded_for", cfg.KeepDownloadedFor.String(),
		"max_parallel", cfg.MaxParallel,
		"download_client", cfg.DownloadClient,
		"db_path", cfg.DBPath,
		"bind_address", cfg.Web.BindAddress,
		"telemetry_enabled", cfg.Telemetry.Enabled,
		"putio_seed_ratio", cfg.PutioSeedRatio,
	)

	logger.InfoContext(ctx, "initializing telemetry")

	tel, err := initializeTelemetry(ctx, cfg)
	if err != nil {
		return err
	}

	defer tel.Shutdown(ctx)

	logger.InfoContext(ctx, "telemetry ready",
		"service_name", cfg.Telemetry.ServiceName,
		"otel_enabled", cfg.Telemetry.Enabled,
	)

	logger.InfoContext(ctx, "initializing services")

	services, err := initializeServices(ctx, cfg, tel)
	if err != nil {
		return err
	}

	defer services.Close()

	logger.InfoContext(ctx, "starting HTTP server")

	servers, err := startServers(ctx, cfg, tel, services)
	if err != nil {
		return err
	}

	logger.InfoContext(ctx, "server ready", "bind_address", cfg.Web.BindAddress)

	logger.InfoContext(ctx, "service ready",
		"bind_address", cfg.Web.BindAddress,
		"target_label", cfg.TargetLabel,
		"version", version.String(),
	)

	return runMainLoop(ctx, cfg, servers)
}

type services struct {
	downloader           *downloader.Downloader
	transferOrchestrator *transfer.TransferOrchestrator
	localDownloads       storage.DownloadRepository
	uiService            *transfers.Service
}

func (s *services) Close() {
	// Use default logger with shutdown group since context may be cancelled
	logger := slog.Default().WithGroup("shutdown")

	logger.Info("stopping services")
	logger.Info("stopping downloader")
	s.downloader.Close()
	logger.Info("downloader stopped")

	logger.Info("stopping transfer orchestrator")
	s.transferOrchestrator.Close()
	logger.Info("transfer orchestrator stopped")

	logger.Info("services stopped")
}

type servers struct {
	api     *http.Server
	ui      *http.Server
	metrics *http.Server
	errors  chan error
}

func initializeConfig() (*config, *slog.Logger, error) {
	var cfg config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, nil, fmt.Errorf("failed to load the env vars: %w", err)
	}

	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})
	traceHandler := logctx.NewTraceHandler(jsonHandler)
	logger := slog.New(traceHandler)

	slog.SetDefault(logger)

	return &cfg, logger, nil
}

func initializeTelemetry(ctx context.Context, cfg *config) (*telemetry.Telemetry, error) {
	tel, err := telemetry.New(ctx, telemetry.Config{
		ServiceName:    cfg.Telemetry.ServiceName,
		ServiceVersion: version.Version,
		OTELAddress:    cfg.Telemetry.OTELAddress,
	})
	if err != nil {
		logger := logctx.LoggerFromContext(ctx)
		logger.ErrorContext(ctx, "telemetry initialization failed",
			"component", "telemetry",
			"service_name", cfg.Telemetry.ServiceName,
			"otel_address", cfg.Telemetry.OTELAddress,
			"err", err)

		return nil, fmt.Errorf("failed to initialize telemetry: %w", err)
	}

	return tel, nil
}

func initializeServices(ctx context.Context, cfg *config, tel *telemetry.Telemetry) (*services, error) {
	logger := logctx.LoggerFromContext(ctx)

	logger.InfoContext(ctx, "initializing database")

	database, err := sqlite.InitDB(ctx, cfg.DBPath, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		logger.ErrorContext(ctx, "database initialization failed",
			"component", "database",
			"db_path", cfg.DBPath,
			"max_open_conns", cfg.DBMaxOpenConns,
			"max_idle_conns", cfg.DBMaxIdleConns,
			"err", err)

		return nil, fmt.Errorf("failed to initialize the database: %w", err)
	}

	logger.InfoContext(ctx, "database ready",
		"db_path", cfg.DBPath,
		"max_open_conns", cfg.DBMaxOpenConns,
		"max_idle_conns", cfg.DBMaxIdleConns,
	)

	dr := sqlite.NewInstrumentedDownloadRepository(database, tel)

	logger.InfoContext(ctx, "initializing download client")

	dc, err := buildDownloadClient(cfg)
	if err != nil {
		logger.ErrorContext(ctx, "download client build failed",
			"component", "download_client",
			"client_type", cfg.DownloadClient,
			"err", err)

		return nil, fmt.Errorf("failed to build download client: %w", err)
	}

	instrumentedDC := transfer.NewInstrumentedDownloadClient(dc, tel, cfg.DownloadClient)
	if err := instrumentedDC.Authenticate(ctx); err != nil {
		logger.ErrorContext(ctx, "download client authentication failed",
			"component", "download_client",
			"client_type", cfg.DownloadClient,
			"err", err)

		return nil, fmt.Errorf("failed to authenticate with the download client: %w", err)
	}

	logger.InfoContext(ctx, "download client ready", "client_type", cfg.DownloadClient)

	arrServices := []*arr.Client{
		arr.NewClient(cfg.Sonarr.APIKey, cfg.Sonarr.BaseURL),
		arr.NewClient(cfg.Radarr.APIKey, cfg.Radarr.BaseURL),
	}

	instrumentedTC := transfer.NewInstrumentedTransferClient(dc.(transfer.TransferClient), tel, cfg.DownloadClient)

	downloader := downloader.NewDownloader(
		cfg.DownloadDir,
		cfg.MaxParallel,
		instrumentedDC,
		instrumentedTC,
		arrServices,
	)

	setupNotificationForDownloader(ctx, dr, downloader, cfg, cfg.PutioSeedRatio)

	transferOrchestrator := transfer.NewTransferOrchestrator(dr, instrumentedDC, cfg.TargetLabel, cfg.PollingInterval)
	transferOrchestrator.ProduceTransfers(ctx)
	downloader.WatchDownloads(ctx, transferOrchestrator.OnDownloadQueued)

	uiService := transfers.NewService(
		instrumentedDC,
		dr,
		transferOrchestrator,
		downloader,
		instrumentedTC,
		cfg.TargetLabel,
		cfg.DownloadDir,
	)

	return &services{
		downloader:           downloader,
		transferOrchestrator: transferOrchestrator,
		localDownloads:       dr,
		uiService:            uiService,
	}, nil
}

func startServers(
	ctx context.Context, cfg *config, tel *telemetry.Telemetry, svcs *services,
) (*servers, error) {
	logger := logctx.LoggerFromContext(ctx)

	serverErrors := make(chan error, 2)

	server, err := setupServer(ctx, cfg, tel, svcs.localDownloads)
	if err != nil {
		logger.ErrorContext(ctx, "server setup failed",
			"component", "http_server",
			"bind_address", cfg.Web.BindAddress,
			"err", err)

		return nil, fmt.Errorf("failed to setup server: %w", err)
	}

	go func() {
		logger.InfoContext(ctx, "initializing Transmission RPC support", "host", cfg.Web.BindAddress)
		serverErrors <- server.ListenAndServe()
	}()

	result := &servers{
		api:    server,
		errors: serverErrors,
	}

	if cfg.UI.Enabled {
		uiServer, err := setupUIServer(ctx, cfg, tel, svcs.uiService)
		if err != nil {
			logger.ErrorContext(ctx, "ui server setup failed",
				"component", "ui_server",
				"bind_address", cfg.UI.BindAddress,
				"err", err)

			return nil, fmt.Errorf("failed to setup ui server: %w", err)
		}

		result.ui = uiServer

		go func() {
			logger.InfoContext(ctx, "initializing Web UI support", "host", cfg.UI.BindAddress)
			serverErrors <- uiServer.ListenAndServe()
		}()
	}

	return result, nil
}

func runMainLoop(ctx context.Context, cfg *config, servers *servers) error {
	logger := logctx.LoggerFromContext(ctx)

	for {
		select {
		case err := <-servers.errors:
			return fmt.Errorf("server error: %w", err)
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Web.ShutdownTimeout)
			defer cancel()

			logger.InfoContext(shutdownCtx, "starting graceful shutdown",
				"shutdown_timeout", cfg.Web.ShutdownTimeout.String())

			// Phase 1: Stop metrics server (if present)
			if servers.metrics != nil {
				logger.InfoContext(shutdownCtx, "stopping metrics server")

				if err := servers.metrics.Shutdown(shutdownCtx); err != nil {
					logger.ErrorContext(shutdownCtx, "failed to gracefully shutdown metrics server", "err", err)
				}

				logger.InfoContext(shutdownCtx, "metrics server stopped")
			}

			// Phase 2: Stop Web UI server (if present)
			if servers.ui != nil {
				logger.InfoContext(shutdownCtx, "stopping Web UI server")

				if err := servers.ui.Shutdown(shutdownCtx); err != nil {
					logger.ErrorContext(shutdownCtx, "failed to gracefully shutdown Web UI server", "err", err)
				}

				logger.InfoContext(shutdownCtx, "Web UI server stopped")
			}

			// Phase 3: Stop HTTP server
			logger.InfoContext(shutdownCtx, "stopping HTTP server")

			if err := servers.api.Shutdown(shutdownCtx); err != nil {
				logger.ErrorContext(shutdownCtx, "failed to gracefully shutdown the server", "err", err)

				if err = servers.api.Close(); err != nil {
					return fmt.Errorf("failed to stop server gracefully: %w", err)
				}
			}

			logger.InfoContext(shutdownCtx, "HTTP server stopped")

			// Phase 3: Services are stopped via defer in run() - services.Close() logs its own shutdown

			logger.InfoContext(shutdownCtx, "graceful shutdown complete")

			return ctx.Err()
		}
	}
}

func setupNotificationForDownloader(
	ctx context.Context,
	repo storage.DownloadRepository,
	downloader *downloader.Downloader,
	cfg *config,
	seedRatio float64,
) {
	logger := logctx.LoggerFromContext(ctx).WithGroup("notification")

	var notif notifier.Notifier
	if cfg.DiscordWebhookURL != "" {
		notif = &notifier.DiscordNotifier{WebhookURL: cfg.DiscordWebhookURL}
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(ctx, "notification loop panic",
					"operation", "notification_loop",
					"panic", r,
					"stack", string(debug.Stack()))

				// Restart with clean state if context not cancelled
				if ctx.Err() == nil {
					logger.InfoContext(ctx, "restarting notification loop after panic",
						"operation", "notification_loop")
					time.Sleep(time.Second) // Brief backoff before restart
					setupNotificationForDownloader(ctx, repo, downloader, cfg, seedRatio)
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "notification loop shutdown",
					"operation", "notification_loop",
					"reason", "context_cancelled")

				return
			case t := <-downloader.OnTransferDownloadError:
				handleDownloadError(ctx, logger, repo, notif, t)
			case t := <-downloader.OnTransferDownloadFinished:
				handleDownloadFinished(ctx, logger, repo, notif, downloader, t, cfg.PollingInterval)
			case t := <-downloader.OnTransferImported:
				handleTransferImported(ctx, logger, notif, downloader, t, cfg.PollingInterval, seedRatio)
			case event := <-downloader.OnTransferMissing:
				handleTransferMissing(ctx, logger, repo, notif, event)
			}
		}
	}()
}

func handleDownloadError(
	ctx context.Context,
	logger *slog.Logger,
	repo storage.DownloadRepository,
	notif notifier.Notifier,
	t *transfer.Transfer,
) {
	err := repo.UpdateTransferStatus(t.ID, "failed")
	if err != nil {
		logger.ErrorContext(ctx, "failed to update transfer status", "transfer_id", t.ID, "err", err)

		return
	}

	logger.WarnContext(ctx, "transfer download error", "transfer_id", t.ID, "transfer_name", t.Name)

	if notif != nil {
		if notifyErr := notif.Notify(
			"❌ Download failed for transfer: " + t.Name + " (" + t.ID + ")",
		); notifyErr != nil {
			logger.ErrorContext(ctx, "failed to send notification", "err", notifyErr)
		}
	}
}

func handleDownloadFinished(
	ctx context.Context,
	logger *slog.Logger,
	repo storage.DownloadRepository,
	notif notifier.Notifier,
	dl *downloader.Downloader,
	t *transfer.Transfer,
	pollingInterval time.Duration,
) {
	err := repo.UpdateTransferStatus(t.ID, "downloaded")
	if err != nil {
		logger.ErrorContext(ctx, "failed to update transfer status", "transfer_id", t.ID, "err", err)

		return
	}

	dl.WatchForImported(ctx, t, pollingInterval)

	logger.InfoContext(ctx, "transfer download finished", "transfer_id", t.ID, "transfer_name", t.Name)

	if notif != nil {
		if notifyErr := notif.Notify(
			"✅ Download finished for transfer: " + t.Name + " (" + t.ID + ")",
		); notifyErr != nil {
			logger.ErrorContext(ctx, "failed to send notification", "err", notifyErr)
		}
	}
}

func handleTransferImported(
	ctx context.Context,
	logger *slog.Logger,
	notif notifier.Notifier,
	dl *downloader.Downloader,
	t *transfer.Transfer,
	pollingInterval time.Duration,
	seedRatio float64,
) {
	if seedRatio > 0 {
		dl.WatchForSeeding(ctx, t, pollingInterval, seedRatio)
	} else {
		dl.CleanupTransfer(ctx, t)
	}

	if notif != nil {
		if notifyErr := notif.Notify(
			"📪 Transfer imported: " + t.Name + " (" + t.ID + ")",
		); notifyErr != nil {
			logger.ErrorContext(ctx, "failed to send notification", "err", notifyErr)
		}
	}
}

func handleTransferMissing(
	ctx context.Context,
	logger *slog.Logger,
	repo storage.DownloadRepository,
	notif notifier.Notifier,
	event downloader.MissingTransferEvent,
) {
	if err := repo.UpdateTransferStatus(event.Transfer.ID, "missing"); err != nil {
		logger.ErrorContext(ctx, "failed to update transfer status to missing", "transfer_id", event.Transfer.ID, "err", err)
	}

	logger.WarnContext(ctx, "tracked transfer missing from Put.io",
		"transfer_id", event.Transfer.ID,
		"transfer_name", event.Transfer.Name,
		"missing_type", event.MissingType)

	if notif != nil {
		title := "Transfer Removed"
		description := "This transfer was removed from Put.io before download could complete."
		statusLabel := "Transfer Removed"

		if event.MissingType == "files_missing" {
			title = "Transfer Files Missing"
			description = "The files for this transfer were deleted from Put.io while download was in progress."
			statusLabel = "Files Missing"
		}

		embed := notifier.Embed{
			Title:       title,
			Description: description,
			Color:       15158332, // 0xE74C3C red
			Fields: []notifier.EmbedField{
				{Name: "Transfer Name", Value: event.Transfer.Name, Inline: true},
				{Name: "Transfer ID", Value: event.Transfer.ID, Inline: true},
				{Name: "Status", Value: statusLabel, Inline: true},
			},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		if notifyErr := notif.NotifyEmbed(embed); notifyErr != nil {
			logger.WarnContext(ctx, "failed to send missing transfer notification", "transfer_id", event.Transfer.ID, "err", notifyErr)
		}
	}
}

// This is an abstract factory for the download client.
func buildDownloadClient(cfg *config) (transfer.DownloadClient, error) {
	switch cfg.DownloadClient {
	case "putio":
		return putio.NewClient(cfg.PutioToken, true), nil
	}

	return nil, fmt.Errorf("invalid download client: %s", cfg.DownloadClient)
}

// setupServer prepares the handlers and services to create the http rest server.
func setupServer(
	ctx context.Context, cfg *config, tel *telemetry.Telemetry, localDownloads rest.LocalDownloadTracker,
) (*http.Server, error) {
	r := chi.NewRouter()

	// Middleware order is critical:
	// 1. RequestID - generates request_id, stores in context
	r.Use(telemetry.RequestID)

	// 2. otelhttp - creates span, adds trace context to r.Context()
	r.Use(telemetry.NewHTTPMiddleware(cfg.Telemetry.ServiceName))

	// 3. HTTPLogging - logs after handler completes with request_id, trace_id, span_id
	r.Use(telemetry.HTTPLogging)

	var tHandler *rest.TransmissionHandler

	// Get the original client for the transmission handler
	originalClient, err := buildDownloadClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build download client for handler: %w", err)
	}

	if putioClient, ok := originalClient.(*putio.Client); ok {
		tHandler = rest.NewTransmissionHandler(
			cfg.Transmission.Username,
			cfg.Transmission.Password,
			putioClient,
			cfg.TargetLabel,
			cfg.PutioBaseDir,
			cfg.DownloadDir,
			localDownloads,
			tel,
		)
		r.Mount("/", tHandler.Routes())
	} else {
		logger := logctx.LoggerFromContext(ctx)
		logger.ErrorContext(ctx, "invalid download client type",
			"component", "http_server",
			"expected", "putio",
			"actual", cfg.DownloadClient,
			"err", "download client is not a putio client")

		return nil, fmt.Errorf("download client is not a putio client: %s", cfg.DownloadClient)
	}

	return &http.Server{
		Addr:         cfg.Web.BindAddress,
		ReadTimeout:  cfg.Web.ReadTimeout,
		WriteTimeout: cfg.Web.WriteTimeout,
		IdleTimeout:  cfg.Web.IdleTimeout,
		Handler:      r,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}, nil
}

// setupUIServer builds the Web UI/API server served on the dedicated UI port.
func setupUIServer(
	ctx context.Context, cfg *config, tel *telemetry.Telemetry, uiService *transfers.Service,
) (*http.Server, error) {
	logger := logctx.LoggerFromContext(ctx)

	r := chi.NewRouter()
	r.Use(telemetry.RequestID)
	r.Use(telemetry.NewHTTPMiddleware(cfg.Telemetry.ServiceName))
	r.Use(telemetry.HTTPLogging)

	if cfg.UI.Username != "" && cfg.UI.Password != "" {
		r.Use(rest.BasicAuth(cfg.UI.Username, cfg.UI.Password))
		logger.InfoContext(ctx, "Web UI authentication enabled", "component", "ui_server")
	} else {
		logger.InfoContext(ctx, "Web UI authentication disabled; set UI_USERNAME and UI_PASSWORD to require login",
			"component", "ui_server")
	}

	confirmSecret, err := generateConfirmSecret()
	if err != nil {
		return nil, fmt.Errorf("failed to generate confirm token secret: %w", err)
	}

	staticHandler, err := ui.Handler()
	if err != nil {
		return nil, fmt.Errorf("failed to build ui static handler: %w", err)
	}

	uiHandler := rest.NewUIHandler(uiService, buildConfigSnapshot(cfg), confirmSecret, staticHandler)
	r.Mount("/", uiHandler.Routes())

	return &http.Server{
		Addr:         cfg.UI.BindAddress,
		ReadTimeout:  cfg.Web.ReadTimeout,
		WriteTimeout: cfg.Web.WriteTimeout,
		IdleTimeout:  cfg.Web.IdleTimeout,
		Handler:      r,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}, nil
}

// generateConfirmSecret returns a random secret used to sign confirmation tokens.
func generateConfirmSecret() ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("failed to read random bytes: %w", err)
	}

	return secret, nil
}

// buildConfigSnapshot assembles the non-secret configuration exposed to the Web UI.
func buildConfigSnapshot(cfg *config) rest.ConfigSnapshot {
	return rest.ConfigSnapshot{
		Version:           version.Version,
		DownloadDir:       cfg.DownloadDir,
		TargetLabel:       cfg.TargetLabel,
		MaxParallel:       cfg.MaxParallel,
		PollingInterval:   cfg.PollingInterval.String(),
		CleanupInterval:   cfg.CleanupInterval.String(),
		KeepDownloadedFor: cfg.KeepDownloadedFor.String(),
		DownloadClient:    cfg.DownloadClient,
		PutioSeedRatio:    cfg.PutioSeedRatio,
		SonarrConfigured:  cfg.Sonarr.APIKey != "" && cfg.Sonarr.BaseURL != "",
		RadarrConfigured:  cfg.Radarr.APIKey != "" && cfg.Radarr.BaseURL != "",
	}
}
