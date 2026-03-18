// Package examplelogger provides a logrus-based Logger implementation for use in
// examples and applications. Copy and adapt this file as needed.
package examplelogger

import (
	"fmt"
	"reflect"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia/utils/logger"
)

type stringer interface{ String() string }

func objToString(obj any) string {
	if obj == nil {
		return "NIL"
	}
	if s, ok := obj.(stringer); ok {
		return s.String()
	}
	if s, ok := obj.(string); ok {
		return s
	}
	return reflect.TypeOf(obj).Name()
}

func format(obj any, msg string) string {
	name := objToString(obj)
	if len(name) > 25 {
		name = name[:25]
	}
	return fmt.Sprintf("|%25s|%-100s", name, msg)
}

type logrusLogger struct{}

// New returns a Logger that writes to logrus using the |component|message format.
func New(level logrus.Level) logger.Logger {
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors:     true,
		FullTimestamp:   true,
		PadLevelText:    true,
		TimestampFormat: "2006/02/01 15:04:05",
	})
	return logrusLogger{}
}

func (logrusLogger) Trace(obj any, msg string) {
	logrus.Trace(format(obj, msg))
}
func (logrusLogger) Tracef(obj any, msg string, args ...any) {
	logrus.Trace(format(obj, fmt.Sprintf(msg, args...)))
}
func (logrusLogger) Debug(obj any, msg string) {
	logrus.Debug(format(obj, msg))
}
func (logrusLogger) Debugf(obj any, msg string, args ...any) {
	logrus.Debug(format(obj, fmt.Sprintf(msg, args...)))
}
func (logrusLogger) Info(obj any, msg string) {
	logrus.Info(format(obj, msg))
}
func (logrusLogger) Infof(obj any, msg string, args ...any) {
	logrus.Info(format(obj, fmt.Sprintf(msg, args...)))
}
func (logrusLogger) Warning(obj any, msg string) {
	logrus.Warning(format(obj, msg))
}
func (logrusLogger) Warningf(obj any, msg string, args ...any) {
	logrus.Warning(format(obj, fmt.Sprintf(msg, args...)))
}
func (logrusLogger) Error(obj any, msg string) {
	logrus.Error(format(obj, msg))
}
func (logrusLogger) Errorf(obj any, msg string, args ...any) {
	logrus.Error(format(obj, fmt.Sprintf(msg, args...)))
}
