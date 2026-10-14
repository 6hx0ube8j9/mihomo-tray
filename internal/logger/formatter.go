package logger

import "strings"

func CleanError(err error) string {
	if err == nil {
		return ""
	}
	
	cleaned := strings.ReplaceAll(err.Error(), "\n", " ｜ ")
	cleaned = strings.ReplaceAll(cleaned, "\t", " ")
	
	return strings.TrimSpace(cleaned)
}
