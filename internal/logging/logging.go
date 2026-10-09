// SPDX-License-Identifier: AGPL-3.0-only

package logging

import (
	"os"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func New(opts *zap.Options) logr.Logger {
	return zapr.NewLogger(NewRaw(opts))
}

func NewRaw(opts *zap.Options) *uberzap.Logger {
	o := *opts

	if o.DestWriter == nil {
		o.DestWriter = os.Stderr
	}

	zapOpts := append([]uberzap.Option{}, o.ZapOpts...)
	if o.Development {
		if o.NewEncoder == nil {
			o.NewEncoder = consoleEncoder
		}
		if o.Level == nil {
			o.Level = uberzap.NewAtomicLevelAt(zapcore.DebugLevel)
		}
		if o.StacktraceLevel == nil {
			o.StacktraceLevel = uberzap.NewAtomicLevelAt(zapcore.WarnLevel)
		}
		zapOpts = append(zapOpts, uberzap.Development())
	} else {
		if o.NewEncoder == nil {
			o.NewEncoder = jsonEncoder
		}
		if o.Level == nil {
			o.Level = uberzap.NewAtomicLevelAt(zapcore.InfoLevel)
		}
		if o.StacktraceLevel == nil {
			o.StacktraceLevel = uberzap.NewAtomicLevelAt(zapcore.ErrorLevel)
		}
	}

	if o.TimeEncoder == nil {
		o.TimeEncoder = zapcore.RFC3339TimeEncoder
	}
	encoderOpts := append([]zap.EncoderConfigOption{func(c *zapcore.EncoderConfig) {
		c.EncodeTime = o.TimeEncoder
	}}, o.EncoderConfigOptions...)

	encoder := o.Encoder
	if encoder == nil {
		encoder = o.NewEncoder(encoderOpts...)
	}

	sink := zapcore.AddSync(o.DestWriter)
	zapOpts = append(zapOpts, uberzap.AddStacktrace(o.StacktraceLevel), uberzap.ErrorOutput(sink))

	core := zapcore.NewCore(&zap.KubeAwareEncoder{Encoder: encoder, Verbose: o.Development}, sink, o.Level)
	return uberzap.New(core, zapOpts...)
}

func jsonEncoder(opts ...zap.EncoderConfigOption) zapcore.Encoder {
	c := uberzap.NewProductionEncoderConfig()
	for _, opt := range opts {
		opt(&c)
	}
	return zapcore.NewJSONEncoder(c)
}

func consoleEncoder(opts ...zap.EncoderConfigOption) zapcore.Encoder {
	c := uberzap.NewDevelopmentEncoderConfig()
	for _, opt := range opts {
		opt(&c)
	}
	return zapcore.NewConsoleEncoder(c)
}
