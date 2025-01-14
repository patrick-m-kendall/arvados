// Copyright (C) The Arvados Authors. All rights reserved.
//
// SPDX-License-Identifier: AGPL-3.0

package main

import (
	"context"
	"flag"
	"fmt"
	"mime"
	"os"

	"git.arvados.org/arvados.git/lib/cmd"
	"git.arvados.org/arvados.git/lib/config"
	"git.arvados.org/arvados.git/sdk/go/arvados"
	"git.arvados.org/arvados.git/sdk/go/ctxlog"
	"github.com/coreos/go-systemd/daemon"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
)

var (
	version = "dev"
)

// Config specifies server configuration.
type Config struct {
	Client  arvados.Client
	Cache   cache
	cluster *arvados.Cluster
}

func newConfig(logger logrus.FieldLogger, arvCfg *arvados.Config) *Config {
	cfg := Config{}
	var cls *arvados.Cluster
	var err error
	if cls, err = arvCfg.GetCluster(""); err != nil {
		log.Fatal(err)
	}
	cfg.cluster = cls
	cfg.Cache.config = &cfg.cluster.Collections.WebDAVCache
	cfg.Cache.cluster = cls
	cfg.Cache.logger = logger
	return &cfg
}

func init() {
	// MakeArvadosClient returns an error if this env var isn't
	// available as a default token (even if we explicitly set a
	// different token before doing anything with the client). We
	// set this dummy value during init so it doesn't clobber the
	// one used by "run test servers".
	if os.Getenv("ARVADOS_API_TOKEN") == "" {
		os.Setenv("ARVADOS_API_TOKEN", "xxx")
	}

	log.SetFormatter(&log.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000000000Z07:00",
	})
}

func configure(logger log.FieldLogger, args []string) (*Config, error) {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)

	loader := config.NewLoader(os.Stdin, logger)
	loader.SetupFlags(flags)

	dumpConfig := flags.Bool("dump-config", false,
		"write current configuration to stdout and exit")
	getVersion := flags.Bool("version", false,
		"print version information and exit.")

	prog := args[0]
	args = loader.MungeLegacyConfigArgs(logger, args[1:], "-legacy-keepweb-config")
	if ok, code := cmd.ParseFlags(flags, prog, args, "", os.Stderr); !ok {
		os.Exit(code)
	} else if *getVersion {
		fmt.Printf("%s %s\n", args[0], version)
		return nil, nil
	}

	arvCfg, err := loader.Load()
	if err != nil {
		return nil, err
	}
	cfg := newConfig(logger, arvCfg)

	if *dumpConfig {
		out, err := yaml.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		_, err = os.Stdout.Write(out)
		return nil, err
	}
	return cfg, nil
}

func main() {
	initLogger := log.StandardLogger()
	logger := initLogger.WithField("PID", os.Getpid())
	cfg, err := configure(logger, os.Args)
	if err != nil {
		log.Fatal(err)
	} else if cfg == nil {
		return
	}
	logger = logger.WithField("ClusterID", cfg.cluster.ClusterID)
	logger.Printf("keep-web %s started", version)
	ctx := ctxlog.Context(context.Background(), logger)

	if ext := ".txt"; mime.TypeByExtension(ext) == "" {
		log.Warnf("cannot look up MIME type for %q -- this probably means /etc/mime.types is missing -- clients will see incorrect content types", ext)
	}

	os.Setenv("ARVADOS_API_HOST", cfg.cluster.Services.Controller.ExternalURL.Host)
	srv := &server{Config: cfg}
	if err := srv.Start(ctx, initLogger); err != nil {
		logger.Fatal(err)
	}
	if _, err := daemon.SdNotify(false, "READY=1"); err != nil {
		logger.Printf("Error notifying init daemon: %v", err)
	}
	logger.Println("Listening at", srv.Addr)
	if err := srv.Wait(); err != nil {
		logger.Fatal(err)
	}
}
