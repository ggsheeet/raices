package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

type CustomLogger struct {
	base *log.Logger
}

func NewCustomLogger(prefix string, out io.Writer) *CustomLogger {
	if out == nil {
		out = os.Stdout
	}
	return &CustomLogger{base: log.New(out, prefix, log.LstdFlags)}
}

var logger = NewCustomLogger("", nil)

func (l *CustomLogger) Info(format string, args ...any) {
	l.write(false, "INFO", "\033[32m", format, args...)
}

func (l *CustomLogger) Warning(format string, args ...any) {
	l.write(true, "WARN", "\033[33m", format, args...)
}

func (l *CustomLogger) Error(format string, args ...any) {
	l.write(true, "ERROR", "\033[31m", format, args...)
}

func (l *CustomLogger) Debug(format string, args ...any) {
	l.write(true, "DEBUG", "\033[36m", format, args...)
}

func (l *CustomLogger) write(withCaller bool, level, color, format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	where := ""
	if withCaller {
		if _, file, line, ok := runtime.Caller(2); ok {
			where = fmt.Sprintf(" %s:%d", filepath.Base(file), line)
		}
	}
	l.base.Printf("%s%s\033[0m%s %s", color, level, where, msg)
}
