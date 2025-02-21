package log

import (
	"flag"
	"fmt"
	"log"
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
		log.Printf("[DEBUG]: %s", fmt.Sprint(v...))
	}
}

func Error(v ...interface{}) {
	if verbosity&ERROR == ERROR {
		log.Printf("[ERROR]: %s", fmt.Sprint(v...))
	}
}

func Fatal(v ...interface{}) {
	if verbosity&FATAL == FATAL {
		log.Printf("[FATAL]: %s", fmt.Sprint(v...))
	}
}

func Info(v ...interface{}) {
	if verbosity&INFO == INFO {
		log.Printf("[INFO]: %s", fmt.Sprint(v...))
	}
}

func Warn(v ...interface{}) {
	if verbosity&WARN == WARN {
		log.Printf("[WARN]: %s", fmt.Sprint(v...))
	}
}
