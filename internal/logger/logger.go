package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Global logger
// since we are running a TUI, we dont want to write to stdout,
// so we will write to stderr + a log file
var errorLogger *log.Logger

func init() {
	initLog()
}

// initLog creates a new log file with a timestamped name each run.
func initLog() (*os.File, error) {
	logDir := "logs"
	err := os.MkdirAll(logDir, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	} else {
		fmt.Printf("Log directory '%s' created or already exists\n", logDir)
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := filepath.Join(logDir, fmt.Sprintf("signalhound_%s.log", timestamp))

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	} else {
		fmt.Printf("Log file %s created\n", filename)
	}

	// Create a logger that writes to both file and stderr
	errorLogger = log.New(file, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	return file, nil
}

// HandleError logs errors bot to stderr and also a log file
func HandleError(err error) {
	if err != nil {
		// Print to stderr
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		// Log to file
		errorLogger.Println(err)
	}
}
