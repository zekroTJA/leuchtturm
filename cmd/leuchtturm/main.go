package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/zekrotja/leuchtturm/pkg/docker"
)

type Args struct {
	LogLevel     slog.Level `arg:"--log-level,env:LT_LOGLEVEL" default:"info" help:"Log level"`
	KeepOldImage bool       `arg:"--keep-old-image,env:LT_KEEP_OLD_IMAGE" help:"Keep old images after update; override with label leuchtturm.keep-old-imag"`
	Schedule     string     `arg:"--schedule,env:LT_SCHEDULE" default:"2 12 * * *" help:"Cron schedule for updates; overrride with label leuchtturm.schedule"`
}

func main() {
	start := time.Now()

	var args Args
	arg.MustParse(&args)

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: args.LogLevel,
	})))

	dc, err := docker.New(args.Schedule, args.KeepOldImage)
	if err != nil {
		slog.Error("docker controller initialization failed", "err", err)
		os.Exit(1)
	}
	defer dc.Close()

	slog.Info("leuchtturm started", "took", time.Since(start))
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
}
