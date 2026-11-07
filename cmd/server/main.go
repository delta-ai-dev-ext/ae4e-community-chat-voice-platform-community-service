// Command server is the entrypoint of community_service: it loads and validates
// configuration, migrates the schema, wires the domain services, the HTTP router
// and the Kafka producer/consumer, and drains everything on SIGTERM/SIGINT.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"community_service/internal/config"
	"community_service/internal/events"
	"community_service/internal/handler"
	"community_service/internal/models"
	"community_service/internal/obs"
	"community_service/internal/repository"
	"community_service/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "community_service: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// `.env` is a local-development convenience only; production reads the
	// environment directly and a missing file is not an error.
	_ = godotenv.Load()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger, err := obs.NewLogger(cfg.LogLevel)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("starting community_service",
		zap.String("log_level", cfg.LogLevel),
		zap.Int("port", cfg.Port),
		zap.Duration("shutdown_timeout", cfg.ShutdownTimeout))

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	migrated, err := repository.Migrate(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if migrated {
		logger.Info("database schema is up to date")
	}

	store, err := repository.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	producer, err := events.NewProducer(brokerConfig(cfg), logger)
	if err != nil {
		return err
	}
	defer func() { _ = producer.Close() }()

	metrics := obs.NewMetrics()

	publisher := &events.RetryingPublisher{
		Publisher: producer,
		Logger:    logger,
		// A state change is committed before its event is published, so a final
		// publish failure is counted and logged rather than lost silently.
		OnFailure: func(topic, eventType string, err error) { metrics.RecordPublishFailure() },
	}

	deps := service.Deps{
		Communities: store,
		Channels:    store,
		Invites:     store,
		Memberships: store,
		Roles:       store,
		Sanctions:   store,
		Publisher:   publisher,
		Clock:       models.SystemClock{},
		Logger:      logger,
		Metrics:     metrics,
	}

	tokens, err := handler.NewTokenVerifier(cfg.IdentityIssuer, cfg.IdentityAudience, cfg.IdentityJWKS, cfg.IdentityJWKSPath)
	if err != nil {
		return err
	}

	sanctionApplier := service.NewSanctionApplier(deps)

	consumer, err := events.NewConsumer(events.ConsumerSettings{
		Brokers: brokerConfig(cfg),
		GroupID: cfg.KafkaConsumerGroup,
		Topic:   events.ModerationTopic,
	}, sanctionApplier, logger)
	if err != nil {
		return err
	}
	consumer.OnLag = metrics.RecordConsumerLag
	consumer.OnError = func(error) { metrics.RecordConsumerError() }
	consumer.OnDeadLetter = func(string, error) { metrics.RecordDeadLetter(events.EventTypeMemberSanctionChanged) }

	state := &readiness{store: store, producer: producer}

	h := &handler.Handler{
		Communities:   service.NewCommunityService(deps),
		Channels:      service.NewChannelService(deps),
		Invites:       service.NewInviteService(deps),
		Memberships:   service.NewMembershipService(deps),
		Roles:         service.NewRoleService(deps),
		Authorization: service.NewAuthorizationService(deps),
		Tokens:        tokens,
		InternalToken: cfg.InternalServiceToken,
		Readiness:     state,
		Logger:        logger,
		Metrics:       metrics,
	}

	engine := h.NewRouter(&handler.Middleware{
		Tokens:        tokens,
		InternalToken: cfg.InternalServiceToken,
		Logger:        logger,
		Metrics:       metrics,
	})

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	consumerCtx, stopConsumer := context.WithCancel(context.Background())
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		if err := consumer.Run(consumerCtx); err != nil {
			logger.Error("kafka consumer stopped", zap.Error(err))
		}
	}()

	logger.Info("http server listening", zap.String("addr", server.Addr))

	var runErr error
	select {
	case err := <-serverErr:
		runErr = err
		logger.Error("http server failed", zap.Error(err))
	case <-rootCtx.Done():
		logger.Info("shutdown signal received")
	}

	// (1) Readiness fails immediately so no new traffic is routed here.
	state.beginShutdown()

	// (2)+(3) Stop accepting requests and drain in-flight work within the budget.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http drain failed", zap.Error(err))
	}

	stopConsumer()
	select {
	case <-consumerDone:
	case <-shutdownCtx.Done():
		logger.Warn("kafka consumer drain timed out")
	}

	// (4) Close the Kafka consumer and client, then the pool (deferred).
	if err := consumer.Close(); err != nil {
		logger.Error("kafka consumer close failed", zap.Error(err))
	}
	if err := producer.Close(); err != nil {
		logger.Error("kafka producer close failed", zap.Error(err))
	}

	logger.Info("shutdown complete")
	return runErr
}

// readiness implements handler.ReadinessChecker: it reports dependency health and
// flips to failing as soon as shutdown begins.
type readiness struct {
	store    *repository.Store
	producer *events.SaramaProducer
	draining atomic.Bool
}

// beginShutdown makes every subsequent readiness probe fail.
func (r *readiness) beginShutdown() { r.draining.Store(true) }

// Ready checks PostgreSQL and Kafka. Liveness is deliberately not modelled here:
// /healthz must never fail on a dependency hiccup.
func (r *readiness) Ready(ctx context.Context) error {
	if r.draining.Load() {
		return errors.New("instance is draining")
	}
	if r.store != nil {
		if err := r.store.Ping(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
	}
	if r.producer != nil {
		if err := r.producer.Healthy(); err != nil {
			return fmt.Errorf("kafka: %w", err)
		}
	}
	return nil
}

// brokerConfig projects the validated configuration onto the Kafka settings.
func brokerConfig(cfg *config.Config) events.BrokerConfig {
	return events.BrokerConfig{
		Brokers:       cfg.KafkaBrokers,
		SASLMechanism: cfg.KafkaSASLMechanism,
		SASLUsername:  cfg.KafkaSASLUsername,
		SASLPassword:  cfg.KafkaSASLPassword,
		TLSEnabled:    cfg.KafkaTLSEnabled,
	}
}
