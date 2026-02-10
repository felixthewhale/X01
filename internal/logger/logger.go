package logger

import (
	"fmt"
	"time"
)

const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Purple = "\033[35m"
	Cyan   = "\033[36m"
	Gray   = "\033[37m"
	Bold   = "\033[1m"
)

func LogInfo(format string, a ...interface{}) {
	fmt.Printf("%s[%s] INFO: %s%s\n", Gray, time.Now().Format("15:04:05"), fmt.Sprintf(format, a...), Reset)
}

func LogSuccess(format string, a ...interface{}) {
	fmt.Printf("%s[%s] SUCCESS: %s%s\n", Green, time.Now().Format("15:04:05"), fmt.Sprintf(format, a...), Reset)
}

func LogError(format string, a ...interface{}) {
	fmt.Printf("%s[%s] ERROR: %s%s\n", Red, time.Now().Format("15:04:05"), fmt.Sprintf(format, a...), Reset)
}

func LogWarning(format string, a ...interface{}) {
	fmt.Printf("%s[%s] WARNING: %s%s\n", Yellow, time.Now().Format("15:04:05"), fmt.Sprintf(format, a...), Reset)
}

func LogThink(format string, a ...interface{}) {
	fmt.Printf("%s[%s] %sTHINKING: %s%s\n", Cyan, time.Now().Format("15:04:05"), Bold, fmt.Sprintf(format, a...), Reset)
}

func LogTool(name string, format string, a ...interface{}) {
	fmt.Printf("%s[%s] %sTOOL(%s): %s%s\n", Purple, time.Now().Format("15:04:05"), Bold, name, fmt.Sprintf(format, a...), Reset)
}

func LogAgent(format string, a ...interface{}) {
	fmt.Printf("%s[%s] %sAGENT: %s%s\n", Blue, time.Now().Format("15:04:05"), Bold, fmt.Sprintf(format, a...), Reset)
}

func LogHeader(title string) {
	fmt.Printf("\n%s%s%s\n", Yellow+Bold, title, Reset)
	fmt.Printf("%s%s%s\n", Yellow, "========================================", Reset)
}
