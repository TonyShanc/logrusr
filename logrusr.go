package logrusr

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
)

// According to the specification of the Logger interface calling the InfoLogger
// directly on the logger should be the same as calling them on V(0). Since
// logrus level 0 is PanicLevel and Infolevel doesn't start until V(4) we use
// this constant to be able to calculate what V(n) values should mean.
const logrusDiffToInfo = 4

// FormatFunc is the function to format log values with for non primitive data.
// By default, this is empty and the data will be JSON marshaled.
type FormatFunc func(interface{}) string

// Option is options to give when construction a logrusr logger.
type Option func(l *logrusr)

// WithFormatter will set the FormatFunc to use.
func WithFormatter(f FormatFunc) Option {
	return func(l *logrusr) {
		l.defaultFormatter = f
	}
}

// WithReportCaller will enable reporting of the caller.
func WithReportCaller() Option {
	return func(l *logrusr) {
		l.reportCaller = true
	}
}

// WithName will set an initial name instead of having to call `WithName` on the
// logger itself after constructing it.
func WithName(name ...string) Option {
	return func(l *logrusr) {
		l.name = name

		l.logger = l.logger.WithField(
			"logger", strings.Join(l.name, "."),
		)
	}
}

type logrusr struct {
	name             []string
	depth            int
	reportCaller     bool
	logger           logrus.FieldLogger
	defaultFormatter FormatFunc
}

// New will return a new logr.Logger created from a logrus.FieldLogger.
func New(l logrus.FieldLogger, opts ...Option) logr.Logger {
	logger := &logrusr{
		depth:  0,
		logger: l,
	}

	for _, o := range opts {
		o(logger)
	}

	return logr.New(logger)
}

// NewLoggerWithFormatter will return a new logr.Logger from a
// logrus.FieldLogger that uses provided function to format complex data types.
func NewLoggerWithFormatter(l logrus.FieldLogger, formatter func(interface{}) string, name ...string) logr.Logger {
	logger := &logrusr{
		name:             name,
		depth:            0,
		logger:           l,
		defaultFormatter: formatter,
	}

	return logr.New(logger)
}

// Init receives optional information about the library.
func (l *logrusr) Init(ri logr.RuntimeInfo) {
	l.depth = ri.CallDepth
}

// Enabled tests whether this Logger is enabled. It will return true if the
// logrus.Logger has a level set to logrus.InfoLevel or higher (Warn/Panic).
// According to the documentation, level V(0) should be equivalent as calling
// Info() directly on the logger. To ensure this the constant `logrusDiffToInfo`
// will be added to all passed values so that V(0) creates a logger with level
// logrus.InfoLevel and V(2) would create a logger with level logrus.TraceLevel.
// This menas that if logrus is  set to logrus.InfoLevel or **higher** this
// method will return true, otherwise false.
func (l *logrusr) Enabled(level int) bool {
	var log *logrus.Logger

	switch t := l.logger.(type) {
	case *logrus.Logger:
		log = t

	case *logrus.Entry:
		log = t.Logger
	}

	// logrus.InfoLevel has value 4 so if the level on the logger is set to 0 we
	// should only be seen as enabled if the logrus logger has a severity of
	// info or higher.
	return log.IsLevelEnabled(logrus.Level(level + logrusDiffToInfo))
}

// Info logs info messages if the logger is enabled, that is if the level on the
// logger is set to logrus.InfoLevel or less.
func (l *logrusr) Info(level int, msg string, keysAndValues ...interface{}) {
	if c := l.caller(); c != "" {
		l.logger = l.logger.WithField("caller", c)
	}

	l.logger.
		WithFields(listToLogrusFields(l.defaultFormatter, keysAndValues...)).
		Info(msg)
}

// Error logs error messages. Since the log will be written with `Error` level
// it won't show if the severity of the underlying logrus logger is less than
// Error.
func (l *logrusr) Error(err error, msg string, keysAndValues ...interface{}) {
	if c := l.caller(); c != "" {
		l.logger = l.logger.WithField("caller", c)
	}

	l.logger.
		WithFields(listToLogrusFields(l.defaultFormatter, keysAndValues...)).
		WithError(err).
		Error(msg)
}

// WithValues returns a new logger with additional key/values pairs. This is
// equivalent to logrus WithFields() but takes a list of even arguments
// (key/value pairs) instead of a map as input. If an odd number of arguments
// are sent all values will be discarded.
func (l *logrusr) WithValues(keysAndValues ...interface{}) logr.LogSink {
	newLogger := l.copyLogger()
	newLogger.logger = l.logger.WithFields(
		listToLogrusFields(l.defaultFormatter, keysAndValues...),
	)

	return newLogger
}

// WithName is a part of the Logger interface. This will set the key "logger" as
// a logrus field to identify the instance.
func (l *logrusr) WithName(name string) logr.LogSink {
	newLogger := l.copyLogger()
	newLogger.name = append(newLogger.name, name)

	newLogger.logger = l.logger.WithField(
		"logger", strings.Join(newLogger.name, "."),
	)

	return newLogger
}

// listToLogrusFields converts a list of arbitrary length to key/value paris.
func listToLogrusFields(formatter func(interface{}) string, keysAndValues ...interface{}) logrus.Fields {
	f := make(logrus.Fields)

	// Skip all fields if it's not an even length list.
	if len(keysAndValues)%2 != 0 {
		return f
	}

	for i := 0; i < len(keysAndValues); i += 2 {
		k, v := keysAndValues[i], keysAndValues[i+1]

		if s, ok := k.(string); ok {
			// Try to avoid marshaling known types.
			switch vVal := v.(type) {
			case int, int8, int16, int32, int64,
				uint, uint8, uint16, uint32, uint64,
				float32, float64, complex64, complex128,
				string, bool:
				f[s] = vVal

			case []byte:
				f[s] = string(vVal)

			default:
				if formatter != nil {
					f[s] = formatter(v)
				} else {
					j, _ := json.Marshal(vVal)
					f[s] = string(j)
				}
			}
		}
	}

	return f
}

// copyLogger copies the logger creating a new slice of the name but preserving
// the formatter and actual logrus logger.
func (l *logrusr) copyLogger() *logrusr {
	newLogger := &logrusr{
		name:             make([]string, len(l.name)),
		depth:            l.depth,
		reportCaller:     l.reportCaller,
		logger:           l.logger,
		defaultFormatter: l.defaultFormatter,
	}

	copy(newLogger.name, l.name)

	return newLogger
}

// WithCallDepth implements the optional WithCallDepth to offset the call stack
// when reporting caller.
func (l *logrusr) WithCallDepth(depth int) logr.LogSink {
	newLogger := l.copyLogger()
	newLogger.depth = depth

	return newLogger
}

// caller will return the caller of the logging method.
func (l *logrusr) caller() string {
	// Check if we should even report the caller.
	if !l.reportCaller {
		return ""
	}

	// +1 for this frame.
	// +1 for frame calling here (Info/Error)
	// +1 for logr frame
	_, file, line, ok := runtime.Caller(l.depth + 3)
	if !ok {
		return ""
	}

	return fmt.Sprintf("%s:%d", filepath.Base(file), line)
}
