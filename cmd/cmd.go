// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/replicator"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	version = "development"
	sha     = ""
)

type cmd struct {
	cfgile string
	debug  bool
	log    *logrus.Entry
}

func Run() {
	c := &cmd{}

	help := fmt.Sprintf("Choria Stream Replicator version %s", version)
	if sha != "" {
		help = fmt.Sprintf("Choria Stream Replicator version %s (%s)", version, sha)
	}

	app := kingpin.New("stream-replicator", help)
	app.Author("R.I.Pienaar <rip@devco.net>")
	app.Version(version)

	repl := app.Command("replicate", "Starts the Stream Replicator process").Default().Action(c.replicateAction)
	repl.Flag("config", "Configuration file").Required().ExistingFileVar(&c.cfgile)
	repl.Flag("debug", "Enables debug logging").BoolVar(&c.debug)

	kingpin.MustParse(app.Parse(os.Args[1:]))
}

func (c *cmd) replicateAction(_ *kingpin.ParseContext) error {
	cfg, err := config.Load(c.cfgile)
	if err != nil {
		return err
	}

	c.log, err = c.configureLogging(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg := &sync.WaitGroup{}

	go c.interruptHandler(ctx, cancel)

	go c.setupPrometheus(cfg.MonitorPort)

	for _, s := range cfg.Streams {
		c.log.Debugf("Configuring stream %s", s.Name)
		stream, err := replicator.NewStream(s, cfg, c.log)
		if err != nil {
			return err
		}

		wg.Add(1)
		go func(s *config.Stream) {
			defer wg.Done()

			wg.Add(1)
			err = stream.Run(ctx, wg)
			if err != nil {
				c.log.Errorf("Could not start replicator for %s: %v", s.Name, err)
			}
		}(s)
	}

	wg.Wait()

	return nil
}

func (c *cmd) setupPrometheus(port int) {
	if port == 0 {
		c.log.Infof("Skipping Prometheus setup")
		return
	}

	c.log.Infof("Listening for /metrics on %d", port)
	http.Handle("/metrics", promhttp.Handler())
	c.log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func (c *cmd) configureLogging(cfg *config.Config) (*logrus.Entry, error) {
	logger := logrus.New()

	if cfg.LogFile != "" {
		logger.SetFormatter(&logrus.JSONFormatter{})

		file, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			return nil, err
		}

		logger.SetOutput(file)
	}

	switch cfg.LogLevel {
	case "debug":
		logger.SetLevel(logrus.DebugLevel)
	case "warn":
		logger.SetLevel(logrus.WarnLevel)
	default:
		logger.SetLevel(logrus.InfoLevel)
	}

	if c.debug {
		logger.SetLevel(logrus.DebugLevel)
		logger.Infof("Forcing debug logging due to CLI override")
	}

	return logrus.NewEntry(logger), nil
}

func (c *cmd) interruptHandler(ctx context.Context, cancel context.CancelFunc) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case sig := <-sigs:
			c.log.Warnf("Shutting down on signal %s", sig)
			cancel()
		case <-ctx.Done():
			return
		}
	}
}
