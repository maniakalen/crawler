package log

import (
	"flag"
	"fmt"
	"log"
	"time"
)

const (
	INFO  = 1
	WARN  = 2
	ERROR = 4
	DEBUG = 8
	FATAL = 16
)

var verbosity int

func init() {
	flag.IntVar(&verbosity, "v", 0, "verbosity level")
	flag.Parse()
	fmt.Println("verbosity level:", verbosity)
}

func Debug(v ...interface{}) {
	if verbosity&DEBUG == DEBUG {
		log.Printf("[%s][DEBUG]: %s", time.Now().Format("02/01/2006 15:04:05"), fmt.Sprint(v...))
	}
}

func Error(v ...interface{}) {
	if verbosity&ERROR == ERROR {
		log.Printf("[%s][ERROR]: %s", time.Now().Format("02/01/2006 15:04:05"), fmt.Sprint(v...))
	}
}

func Fatal(v ...interface{}) {
	if verbosity&FATAL == FATAL {
		log.Printf("[%s][FATAL]: %s", time.Now().Format("02/01/2006 15:04:05"), fmt.Sprint(v...))
	}
}

func Info(v ...interface{}) {
	if verbosity&INFO == INFO {
		log.Printf("[%s][INFO]: %s", time.Now().Format("02/01/2006 15:04:05"), fmt.Sprint(v...))
	}
}

func Warn(v ...interface{}) {
	if verbosity&WARN == WARN {
		log.Printf("[%s][WARN]: %s", time.Now().Format("02/01/2006 15:04:05"), fmt.Sprint(v...))
	}
}
