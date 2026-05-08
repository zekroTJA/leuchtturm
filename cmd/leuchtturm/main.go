package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/alexflint/go-arg"
	"github.com/zekrotja/leuchtturm/pkg/docker"
)

type Args struct {
	LogLevel slog.Level `arg:"--log-level,env:LT_LOGLEVEL" default:"info"`
}

func main() {
	var args Args
	arg.MustParse(&args)

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: args.LogLevel,
	})))

	dc, err := docker.New()
	if err != nil {
		slog.Error("docker controller initialization failed", "err", err)
		os.Exit(1)
	}
	defer dc.Close()

	if err = dc.UpdateAll(context.Background()); err != nil {
		slog.Error("update all failed", "err", err)
		os.Exit(1)
	}
}
