package logger

import (
	"bytes"
	"fmt"
	"reflect"

	"github.com/sirupsen/logrus"
)

type stringer interface {
	String() string
}

type logPair struct {
	logFn func(...any)
	obj   string
	msg   string
}

const logSize = 1000

var logCh = make(chan logPair, logSize)

func objToString(obj any) (objStr string) {
	if obj == nil {
		objStr = "NIL"
	} else if stringerObj, ok := obj.(stringer); ok {
		objStr = stringerObj.String()
	} else if objStr, ok = obj.(string); ok {
	} else {
		objStr = reflect.TypeOf(obj).Name()
	}
	return
}

func Init(lvl logrus.Level) {
	logrus.SetLevel(lvl)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors:     true,
		FullTimestamp:   true,
		PadLevelText:    true,
		TimestampFormat: "2006/02/01 15:04:05",
	})

	go func() {
		sb := new(bytes.Buffer)
		for logPair := range logCh {
			if len(logPair.obj) > 20 {
				logPair.obj = logPair.obj[:20]
			}
			sb.WriteString(fmt.Sprintf("|%20s|%-100s", logPair.obj, logPair.msg))
			logPair.logFn(sb.String())
			sb.Reset()
		}
	}()
}

func Trace(object any, message string) {
	if logrus.GetLevel() < logrus.TraceLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Trace,
		obj:   objToString(object),
		msg:   message,
	}
}

func Tracef(object any, message string, args ...any) {
	if logrus.GetLevel() < logrus.TraceLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Trace,
		obj:   objToString(object),
		msg:   fmt.Sprintf(message, args...),
	}
}

func Debug(object interface{}, message string) {
	if logrus.GetLevel() < logrus.DebugLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Debug,
		obj:   objToString(object),
		msg:   message,
	}
}

func Debugf(object interface{}, message string, args ...any) {
	if logrus.GetLevel() < logrus.DebugLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Debug,
		obj:   objToString(object),
		msg:   fmt.Sprintf(message, args...),
	}
}

func Info(object interface{}, message string) {
	if logrus.GetLevel() < logrus.InfoLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Info,
		obj:   objToString(object),
		msg:   message,
	}
}

func Infof(object interface{}, message string, args ...any) {
	if logrus.GetLevel() < logrus.InfoLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Info,
		obj:   objToString(object),
		msg:   fmt.Sprintf(message, args...),
	}
}

func Warning(object interface{}, message string) {
	if logrus.GetLevel() < logrus.WarnLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Warning,
		obj:   objToString(object),
		msg:   message,
	}
}

func Warningf(object interface{}, message string, args ...any) {
	if logrus.GetLevel() < logrus.WarnLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Warning,
		obj:   objToString(object),
		msg:   fmt.Sprintf(message, args...),
	}
}

func Error(object interface{}, message string) {
	if logrus.GetLevel() < logrus.ErrorLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Error,
		obj:   objToString(object),
		msg:   message,
	}
}

func Errorf(object interface{}, message string, args ...any) {
	if logrus.GetLevel() < logrus.ErrorLevel {
		return
	}
	logCh <- logPair{
		logFn: logrus.Error,
		obj:   objToString(object),
		msg:   fmt.Sprintf(message, args...),
	}
}

func Fatal(object interface{}, message string) {
	objStr := objToString(object)
	if len(objStr) > 20 {
		objStr = objStr[:20]
	}
	logrus.Fatalf("|%20s|%-100s", objStr, message)
}

func Fatalf(object interface{}, message string, args ...any) {
	objStr := objToString(object)
	if len(objStr) > 20 {
		objStr = objStr[:20]
	}
	logrus.Fatalf("|%20s|%-100s", objStr, fmt.Sprintf(message, args...))
}
