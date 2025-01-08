package config

import (
	"os"
	"runtime"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

var encoderCfg = zapcore.EncoderConfig{
	MessageKey:   "message",
	NameKey:      "name",
	LevelKey:     "level",
	EncodeLevel:  zapcore.LowercaseLevelEncoder,
	CallerKey:    "caller",
	EncodeCaller: zapcore.ShortCallerEncoder,
	TimeKey:      "time",
	EncodeTime:   zapcore.ISO8601TimeEncoder,
	LineEnding:   zapcore.DefaultLineEnding,
}

func InitLogger(level zapcore.Level) {
	zl := zap.New(
		zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderCfg),
			zapcore.Lock(os.Stdout),
			level,
		),
		zap.AddCaller(),
		zap.Fields(
			zap.String("version", runtime.Version()),
			zap.String("build", Version()),
			zap.String("git_commit", GitCommit()),
		),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)

	Log = zl
}
