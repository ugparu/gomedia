package logger

//go:generate mockgen -source=logger.go -destination=../../mocks/mock_logger.go -package=mocks

// Logger is the interface used throughout the library for structured logging.
// Each method takes an obj parameter for component identification and a message.
// The default implementation is a no-op; inject a real logger via WithLogger options.
type Logger interface {
	Trace(obj any, msg string)
	Tracef(obj any, msg string, args ...any)
	Debug(obj any, msg string)
	Debugf(obj any, msg string, args ...any)
	Info(obj any, msg string)
	Infof(obj any, msg string, args ...any)
	Warning(obj any, msg string)
	Warningf(obj any, msg string, args ...any)
	Error(obj any, msg string)
	Errorf(obj any, msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Trace(any, string)            {}
func (nopLogger) Tracef(any, string, ...any)   {}
func (nopLogger) Debug(any, string)            {}
func (nopLogger) Debugf(any, string, ...any)   {}
func (nopLogger) Info(any, string)             {}
func (nopLogger) Infof(any, string, ...any)    {}
func (nopLogger) Warning(any, string)          {}
func (nopLogger) Warningf(any, string, ...any) {}
func (nopLogger) Error(any, string)            {}
func (nopLogger) Errorf(any, string, ...any)   {}

// Default is the fallback logger used when no logger is injected via WithLogger.
// Library users can set this to a real implementation or supply per-component loggers.
var Default Logger = nopLogger{}
