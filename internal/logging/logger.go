package logging

import (
    "fmt"
    "log"
    "os"
    "time"
)

type Logger struct {
    env string
    l   *log.Logger
}

func NewLogger(env string) *Logger {
    return &Logger{
        env: env,
        l:   log.New(os.Stdout, "", 0),
    }
}

func (l *Logger) log(level, msg string, args ...any) {
    ts := time.Now().Format(time.RFC3339)
    line := fmt.Sprintf("%s level=%s env=%s %s", ts, level, l.env, fmt.Sprintf(msg, args...))
    l.l.Println(line)
}

func (l *Logger) Infof(msg string, args ...any)  { l.log("INFO", msg, args...) }
func (l *Logger) Errorf(msg string, args ...any) { l.log("ERROR", msg, args...) }


