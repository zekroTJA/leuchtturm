package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/alexflint/go-arg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLogHandlerCreator(t *testing.T) {
	t.Run("json format produces json output", func(t *testing.T) {
		creator, err := getLogHandlerCreator(LogFormatJson)
		require.NoError(t, err)

		var buffer bytes.Buffer
		slog.New(creator(&buffer, &slog.HandlerOptions{})).Info("hello", "key", "value")

		var record map[string]any
		require.NoError(t, json.Unmarshal(buffer.Bytes(), &record))
		assert.Equal(t, "hello", record["msg"])
		assert.Equal(t, "value", record["key"])
	})

	t.Run("text format produces text output", func(t *testing.T) {
		creator, err := getLogHandlerCreator(LogFormatText)
		require.NoError(t, err)

		var buffer bytes.Buffer
		slog.New(creator(&buffer, &slog.HandlerOptions{})).Info("hello", "key", "value")

		assert.Contains(t, buffer.String(), "msg=hello")
		assert.Contains(t, buffer.String(), "key=value")
	})

	t.Run("handler respects log level", func(t *testing.T) {
		creator, err := getLogHandlerCreator(LogFormatText)
		require.NoError(t, err)

		var buffer bytes.Buffer
		logger := slog.New(creator(&buffer, &slog.HandlerOptions{Level: slog.LevelWarn}))
		logger.Info("hidden")
		logger.Warn("visible")

		assert.NotContains(t, buffer.String(), "hidden")
		assert.Contains(t, buffer.String(), "visible")
	})

	for _, format := range []LogFormat{"", "JSON", "Text", "yaml", " json"} {
		t.Run("invalid format "+string(format), func(t *testing.T) {
			creator, err := getLogHandlerCreator(format)
			assert.Error(t, err)
			assert.Nil(t, creator)
		})
	}
}

func parseArgs(t *testing.T, cliArgs ...string) (args Args, err error) {
	t.Helper()

	parser, err := arg.NewParser(arg.Config{Program: "leuchtturm"}, &args)
	require.NoError(t, err)

	err = parser.Parse(cliArgs)
	return args, err
}

func TestArgs(t *testing.T) {
	for _, key := range []string{"LT_LOG_LEVEL", "LT_LOG_FORMAT", "LT_SCHEDULE", "LT_KEEP_OLD_IMAGE", "LT_REMOVE_VOLUMES"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}

	t.Run("defaults", func(t *testing.T) {
		args, err := parseArgs(t)

		require.NoError(t, err)
		assert.Equal(t, slog.LevelInfo, args.LogLevel)
		assert.Equal(t, LogFormatText, args.LogFormat)
		assert.Equal(t, "2 12 * * *", args.Schedule)
		assert.False(t, args.KeepOldImage)
		assert.False(t, args.RemoveVolumes)
	})

	t.Run("cli flags", func(t *testing.T) {
		args, err := parseArgs(t,
			"--log-level", "debug",
			"--log-format", "json",
			"--schedule", "@hourly",
			"--keep-old-image",
			"--remove-volumes",
		)

		require.NoError(t, err)
		assert.Equal(t, slog.LevelDebug, args.LogLevel)
		assert.Equal(t, LogFormatJson, args.LogFormat)
		assert.Equal(t, "@hourly", args.Schedule)
		assert.True(t, args.KeepOldImage)
		assert.True(t, args.RemoveVolumes)
	})

	t.Run("environment variables", func(t *testing.T) {
		t.Setenv("LT_LOG_LEVEL", "warn")
		t.Setenv("LT_LOG_FORMAT", "json")
		t.Setenv("LT_SCHEDULE", "*/5 * * * *")
		t.Setenv("LT_KEEP_OLD_IMAGE", "true")
		t.Setenv("LT_REMOVE_VOLUMES", "1")

		args, err := parseArgs(t)

		require.NoError(t, err)
		assert.Equal(t, slog.LevelWarn, args.LogLevel)
		assert.Equal(t, LogFormatJson, args.LogFormat)
		assert.Equal(t, "*/5 * * * *", args.Schedule)
		assert.True(t, args.KeepOldImage)
		assert.True(t, args.RemoveVolumes)
	})

	t.Run("cli flags override environment variables", func(t *testing.T) {
		t.Setenv("LT_SCHEDULE", "@daily")

		args, err := parseArgs(t, "--schedule", "@weekly")

		require.NoError(t, err)
		assert.Equal(t, "@weekly", args.Schedule)
	})

	t.Run("invalid log level", func(t *testing.T) {
		_, err := parseArgs(t, "--log-level", "verbose")
		assert.Error(t, err)
	})

	t.Run("invalid boolean environment variable", func(t *testing.T) {
		t.Setenv("LT_KEEP_OLD_IMAGE", "maybe")

		_, err := parseArgs(t)
		assert.Error(t, err)
	})

	t.Run("unknown flag", func(t *testing.T) {
		_, err := parseArgs(t, "--does-not-exist")
		assert.Error(t, err)
	})

	t.Run("missing flag value", func(t *testing.T) {
		_, err := parseArgs(t, "--schedule")
		assert.Error(t, err)
	})

	t.Run("invalid log format is accepted by parser but rejected by handler", func(t *testing.T) {
		args, err := parseArgs(t, "--log-format", "xml")
		require.NoError(t, err)

		_, err = getLogHandlerCreator(args.LogFormat)
		assert.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "invalid log format"))
	})
}
