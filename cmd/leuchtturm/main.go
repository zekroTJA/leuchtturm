package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/zekrotja/leuchtturm/pkg/docker"
)

type LogFormat string

const (
	LogFormatText = LogFormat("text")
	LogFormatJson = LogFormat("json")
)

type Args struct {
	LogLevel     slog.Level `arg:"--log-level,env:LT_LOG_LEVEL" default:"info" help:"Log level"`
	LogFormat    LogFormat  `arg:"--log-format,env:LT_LOG_FORMAT" default:"text" help:"Log format (text or json)"`
	KeepOldImage bool       `arg:"--keep-old-image,env:LT_KEEP_OLD_IMAGE" help:"Keep old images after update; override with label leuchtturm.keep-old-image"`
	Schedule     string     `arg:"--schedule,env:LT_SCHEDULE" default:"2 12 * * *" help:"Cron schedule for updates; overrride with label leuchtturm.schedule"`
}

type LogHandlerCreator func(w io.Writer, opts *slog.HandlerOptions) slog.Handler

func getLogHandlerCreator(format LogFormat) (LogHandlerCreator, error) {
	switch format {
	case LogFormatJson:
		return func(w io.Writer, opts *slog.HandlerOptions) slog.Handler { return slog.NewJSONHandler(w, opts) }, nil
	case LogFormatText:
		return func(w io.Writer, opts *slog.HandlerOptions) slog.Handler { return slog.NewTextHandler(w, opts) }, nil
	default:
		return nil, errors.New("invalid log format")
	}
}

func main() {
	start := time.Now()

	var args Args
	arg.MustParse(&args)

	logHandlerCreator, err := getLogHandlerCreator(args.LogFormat)
	if err != nil {
		slog.Error("failed initializing logger", "err", err)
		os.Exit(1)
	}

	slog.SetDefault(slog.New(logHandlerCreator(os.Stderr, &slog.HandlerOptions{
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
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	slog.Info("shutting down ...")
}
