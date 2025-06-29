// internal/controllers/logging.go
package controllers

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

type CommandLog struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	UserID        string    `json:"user_id"`
	UserName      string    `json:"user_name"`
	PodName       string    `json:"pod_name"`
	Command       string    `json:"command"`
	Timestamp     time.Time `json:"timestamp"`
	SessionID     string    `json:"session_id"`
}

type LoggingController struct {
	logDir    string
	logFile   *os.File
	logWriter *bufio.Writer
	mutex     sync.Mutex
	redisClient *redis.Client
	adminToken string
}

func NewLoggingController(logDir string) *LoggingController {
	if logDir == "" {
		logDir = "/var/log/k8s-playground"
	}
	
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Warning: failed to create log directory %s: %v", logDir, err)
		logDir = "/tmp/k8s-playground-logs" // fallback
		os.MkdirAll(logDir, 0755)
	}
	
	// Get admin token from environment variable or generate one
	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		adminToken = generateAdminToken()
		log.Printf("Generated admin token for log access: %s", adminToken)
	} else {
		log.Printf("Using admin token from environment: %s", adminToken[:8]+"...")
	}
	
	return &LoggingController{
		logDir: logDir,
		adminToken: adminToken,
	}
}

func NewLoggingControllerWithRedis(logDir string, redisClient *redis.Client) *LoggingController {
	lc := NewLoggingController(logDir)
	lc.redisClient = redisClient
	return lc
}

func (lc *LoggingController) Start(ctx context.Context) error {
	log.Println("Logging controller started")
	
	// Initialize current day log file
	if err := lc.rotateLogFileIfNeeded(); err != nil {
		log.Printf("Failed to initialize log file: %v", err)
		return err
	}
	
	// Start log processor if Redis client is available
	if lc.redisClient != nil {
		go lc.processLogBuffer(ctx)
	}

	// Start daily rotation and compression ticker
	ticker := time.NewTicker(1 * time.Hour) // Check every hour for rotation
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			lc.rotateLogFileIfNeeded()
			lc.compressOldLogFiles()
		case <-ctx.Done():
			log.Println("Logging controller stopping...")
			lc.closeLogFile()
			return nil
		}
	}
}

// LogCommandToBuffer logs a user command to Redis buffer (used by app-controller)
func (lc *LoggingController) LogCommandToBuffer(environmentID, userID, userName, podName, command, sessionID string) error {
	commandLog := CommandLog{
		ID:            fmt.Sprintf("log_%d", time.Now().UnixNano()),
		EnvironmentID: environmentID,
		UserID:        userID,
		UserName:      userName,
		PodName:       podName,
		Command:       command,
		Timestamp:     time.Now(),
		SessionID:     sessionID,
	}

	logData, err := json.Marshal(commandLog)
	if err != nil {
		return fmt.Errorf("failed to marshal command log: %v", err)
	}

	// Push to Redis list buffer
	if lc.redisClient != nil {
		ctx := context.Background()
		if err := lc.redisClient.LPush(ctx, "command_log_buffer", string(logData)).Err(); err != nil {
			return fmt.Errorf("failed to buffer command log to Redis: %v", err)
		}
	}

	log.Printf("Command buffered: User %s (%s) executed '%s' in env %s (pod %s)", 
		userName, userID, command, environmentID, podName)
	
	return nil
}

// LogCommand logs a user command directly to file (fallback method)
func (lc *LoggingController) LogCommand(environmentID, userID, userName, podName, command, sessionID string) error {
	lc.mutex.Lock()
	defer lc.mutex.Unlock()

	commandLog := CommandLog{
		ID:            fmt.Sprintf("log_%d", time.Now().UnixNano()),
		EnvironmentID: environmentID,
		UserID:        userID,
		UserName:      userName,
		PodName:       podName,
		Command:       command,
		Timestamp:     time.Now(),
		SessionID:     sessionID,
	}

	logData, err := json.Marshal(commandLog)
	if err != nil {
		return fmt.Errorf("failed to marshal command log: %v", err)
	}

	// Ensure log file is current
	if err := lc.rotateLogFileIfNeeded(); err != nil {
		log.Printf("Warning: failed to rotate log file: %v", err)
	}

	// Write to file
	if lc.logWriter != nil {
		if _, err := lc.logWriter.WriteString(string(logData) + "\n"); err != nil {
			return fmt.Errorf("failed to write command log: %v", err)
		}
		if err := lc.logWriter.Flush(); err != nil {
			return fmt.Errorf("failed to flush command log: %v", err)
		}
	}

	log.Printf("Command logged: User %s (%s) executed '%s' in env %s (pod %s)", 
		userName, userID, command, environmentID, podName)
	
	return nil
}

// rotateLogFileIfNeeded rotates log file daily
func (lc *LoggingController) rotateLogFileIfNeeded() error {
	currentDate := time.Now().Format("2006-01-02")
	logFileName := fmt.Sprintf("commands-%s.log", currentDate)
	logFilePath := filepath.Join(lc.logDir, logFileName)

	// If current file is already for today, do nothing
	if lc.logFile != nil {
		currentFilePath := lc.logFile.Name()
		if currentFilePath == logFilePath {
			return nil
		}
		// Close current file
		lc.closeLogFile()
	}

	// Open new log file
	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %v", logFilePath, err)
	}

	lc.logFile = file
	lc.logWriter = bufio.NewWriter(file)
	
	log.Printf("Rotated to new log file: %s", logFilePath)
	return nil
}

// closeLogFile closes current log file
func (lc *LoggingController) closeLogFile() {
	if lc.logWriter != nil {
		lc.logWriter.Flush()
		lc.logWriter = nil
	}
	if lc.logFile != nil {
		lc.logFile.Close()
		lc.logFile = nil
	}
}

// GetCommandLogs retrieves command logs from files with optional filters
func (lc *LoggingController) GetCommandLogs(userID, environmentID string, limit int, offset int) ([]CommandLog, error) {
	var allLogs []CommandLog

	// Get list of log files (sorted by date, newest first)
	logFiles, err := lc.getLogFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to get log files: %v", err)
	}

	// Read logs from files
	for _, logFile := range logFiles {
		fileLogs, err := lc.readLogsFromFile(logFile)
		if err != nil {
			log.Printf("Warning: failed to read logs from %s: %v", logFile, err)
			continue
		}
		allLogs = append(allLogs, fileLogs...)
	}

	// Filter logs based on criteria
	var filteredLogs []CommandLog
	for _, logEntry := range allLogs {
		if userID != "" && logEntry.UserID != userID {
			continue
		}
		if environmentID != "" && logEntry.EnvironmentID != environmentID {
			continue
		}
		filteredLogs = append(filteredLogs, logEntry)
	}

	// Sort by timestamp (newest first)
	sort.Slice(filteredLogs, func(i, j int) bool {
		return filteredLogs[i].Timestamp.After(filteredLogs[j].Timestamp)
	})

	// Apply offset and limit
	start := offset
	if start >= len(filteredLogs) {
		return []CommandLog{}, nil
	}

	end := start + limit
	if end > len(filteredLogs) {
		end = len(filteredLogs)
	}

	return filteredLogs[start:end], nil
}

// getLogFiles returns sorted list of log files (newest first), including compressed files
func (lc *LoggingController) getLogFiles() ([]string, error) {
	// Get both .log and .log.gz files
	logFiles, err := filepath.Glob(filepath.Join(lc.logDir, "commands-*.log"))
	if err != nil {
		return nil, fmt.Errorf("failed to glob log files: %v", err)
	}

	compressedFiles, err := filepath.Glob(filepath.Join(lc.logDir, "commands-*.log.gz"))
	if err != nil {
		return nil, fmt.Errorf("failed to glob compressed log files: %v", err)
	}

	// Combine both lists
	allFiles := append(logFiles, compressedFiles...)

	// Sort files by date (newest first)
	sort.Sort(sort.Reverse(sort.StringSlice(allFiles)))
	
	return allFiles, nil
}

// readLogsFromFile reads command logs from a single file (supports both regular and compressed files)
func (lc *LoggingController) readLogsFromFile(filePath string) ([]CommandLog, error) {
	// Check if it's a compressed file
	if strings.HasSuffix(filePath, ".gz") {
		return lc.readCompressedLogFile(filePath)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %v", filePath, err)
	}
	defer file.Close()

	var logs []CommandLog
	scanner := bufio.NewScanner(file)
	
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var commandLog CommandLog
		if err := json.Unmarshal([]byte(line), &commandLog); err != nil {
			log.Printf("Warning: failed to unmarshal log line in %s: %v", filePath, err)
			continue
		}
		
		logs = append(logs, commandLog)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading log file %s: %v", filePath, err)
	}

	return logs, nil
}

// compressOldLogFiles compresses log files older than 1 day
func (lc *LoggingController) compressOldLogFiles() {
	files, err := filepath.Glob(filepath.Join(lc.logDir, "commands-*.log"))
	if err != nil {
		log.Printf("Warning: failed to find log files for compression: %v", err)
		return
	}

	for _, file := range files {
		// Skip current day's log file
		if strings.Contains(file, time.Now().Format("2006-01-02")) {
			continue
		}

		// Skip files already compressed
		if strings.HasSuffix(file, ".gz") {
			continue
		}

		// Compress files older than yesterday
		if err := lc.compressLogFile(file); err != nil {
			log.Printf("Warning: failed to compress log file %s: %v", file, err)
		}
	}

	// Clean up very old compressed files (older than 30 days)
	lc.cleanupOldCompressedFiles()
}

// compressLogFile compresses a single log file
func (lc *LoggingController) compressLogFile(filePath string) error {
	// Create compressed file
	compressedPath := filePath + ".gz"
	
	inputFile, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %v", err)
	}
	defer inputFile.Close()

	outputFile, err := os.Create(compressedPath)
	if err != nil {
		return fmt.Errorf("failed to create compressed file: %v", err)
	}
	defer outputFile.Close()

	gzipWriter := gzip.NewWriter(outputFile)
	defer gzipWriter.Close()

	// Copy data
	if _, err := io.Copy(gzipWriter, inputFile); err != nil {
		return fmt.Errorf("failed to compress data: %v", err)
	}

	// Remove original file
	if err := os.Remove(filePath); err != nil {
		log.Printf("Warning: failed to remove original file %s: %v", filePath, err)
		// Don't return error as compression succeeded
	}

	log.Printf("Compressed log file: %s -> %s", filePath, compressedPath)
	return nil
}

// cleanupOldCompressedFiles removes compressed files older than 30 days
func (lc *LoggingController) cleanupOldCompressedFiles() {
	files, err := filepath.Glob(filepath.Join(lc.logDir, "commands-*.log.gz"))
	if err != nil {
		log.Printf("Warning: failed to find compressed files for cleanup: %v", err)
		return
	}

	cutoffDate := time.Now().AddDate(0, 0, -30) // 30 days ago

	for _, file := range files {
		stat, err := os.Stat(file)
		if err != nil {
			continue
		}

		if stat.ModTime().Before(cutoffDate) {
			if err := os.Remove(file); err != nil {
				log.Printf("Warning: failed to remove old compressed file %s: %v", file, err)
			} else {
				log.Printf("Removed old compressed file: %s", file)
			}
		}
	}
}

// readCompressedLogFile reads logs from a compressed file
func (lc *LoggingController) readCompressedLogFile(filePath string) ([]CommandLog, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open compressed log file %s: %v", filePath, err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader for %s: %v", filePath, err)
	}
	defer gzipReader.Close()

	var logs []CommandLog
	scanner := bufio.NewScanner(gzipReader)
	
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var commandLog CommandLog
		if err := json.Unmarshal([]byte(line), &commandLog); err != nil {
			log.Printf("Warning: failed to unmarshal log line in %s: %v", filePath, err)
			continue
		}
		
		logs = append(logs, commandLog)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading compressed log file %s: %v", filePath, err)
	}

	return logs, nil
}

// processLogBuffer continuously processes logs from Redis buffer
func (lc *LoggingController) processLogBuffer(ctx context.Context) {
	log.Println("Starting log buffer processor...")
	
	for {
		select {
		case <-ctx.Done():
			log.Println("Log buffer processor stopping...")
			return
		default:
			// Try to get logs from Redis buffer (blocking pop with timeout)
			result, err := lc.redisClient.BRPop(ctx, 5*time.Second, "command_log_buffer").Result()
			if err != nil {
				// Timeout or error - continue
				continue
			}
			
			if len(result) >= 2 {
				logData := result[1] // BRPop returns [key, value]
				
				// Parse the log entry
				var commandLog CommandLog
				if err := json.Unmarshal([]byte(logData), &commandLog); err != nil {
					log.Printf("Warning: failed to unmarshal buffered log: %v", err)
					continue
				}
				
				// Write to file
				if err := lc.writeLogToFile(commandLog); err != nil {
					log.Printf("Error writing log to file: %v", err)
					// Re-queue the log to prevent data loss
					if err := lc.redisClient.LPush(ctx, "command_log_buffer", logData).Err(); err != nil {
						log.Printf("Critical: failed to re-queue log entry: %v", err)
					}
				}
			}
		}
	}
}

// writeLogToFile writes a single log entry to file
func (lc *LoggingController) writeLogToFile(commandLog CommandLog) error {
	lc.mutex.Lock()
	defer lc.mutex.Unlock()

	// Ensure log file is current
	if err := lc.rotateLogFileIfNeeded(); err != nil {
		log.Printf("Warning: failed to rotate log file: %v", err)
	}

	// Marshal log entry
	logData, err := json.Marshal(commandLog)
	if err != nil {
		return fmt.Errorf("failed to marshal command log: %v", err)
	}

	// Write to file
	if lc.logWriter != nil {
		if _, err := lc.logWriter.WriteString(string(logData) + "\n"); err != nil {
			return fmt.Errorf("failed to write command log: %v", err)
		}
		if err := lc.logWriter.Flush(); err != nil {
			return fmt.Errorf("failed to flush command log: %v", err)
		}
	}

	log.Printf("Log persisted: User %s (%s) executed '%s' in env %s (pod %s)", 
		commandLog.UserName, commandLog.UserID, commandLog.Command, 
		commandLog.EnvironmentID, commandLog.PodName)
	
	return nil
}


// commandBuffer stores partial commands being typed by users per session
var commandBuffer = sync.Map{}

// ParseCommandFromWebSocketData extracts executable commands from WebSocket data
func (lc *LoggingController) ParseCommandFromWebSocketData(data []byte) string {
	return lc.ParseCommandFromWebSocketDataWithSession(data, "default")
}

// ParseCommandFromWebSocketDataWithSession extracts executable commands with session tracking
func (lc *LoggingController) ParseCommandFromWebSocketDataWithSession(data []byte, sessionID string) string {
	// Handle JSON messages (resize commands, etc.)
	var controlMsg map[string]interface{}
	if err := json.Unmarshal(data, &controlMsg); err == nil {
		// Skip control messages like resize
		if _, ok := controlMsg["resize"]; ok {
			return ""
		}
	}

	// Convert bytes to string
	str := string(data)
	
	// Skip ANSI escape sequences and control characters except CR/LF
	if len(str) > 0 && str[0] == '\x1b' {
		return ""
	}
	
	// Get current buffer for this session
	bufferInterface, _ := commandBuffer.LoadOrStore(sessionID, "")
	currentBuffer := bufferInterface.(string)
	
	// Check for Enter key (CR, LF, or CRLF) - command execution
	if strings.Contains(str, "\r") || strings.Contains(str, "\n") {
		// Command was executed, return the accumulated buffer
		command := strings.TrimSpace(currentBuffer)
		
		// Clear the buffer for this session
		commandBuffer.Store(sessionID, "")
		
		// Return all input, regardless of content
		if len(command) > 0 {
			log.Printf("DEBUG: Input recorded: %q", command)
			return command
		}
		return ""
	}
	
	// Check for backspace (delete character from buffer)
	if str == "\x08" || str == "\x7f" {
		if len(currentBuffer) > 0 {
			currentBuffer = currentBuffer[:len(currentBuffer)-1]
			commandBuffer.Store(sessionID, currentBuffer)
		}
		return ""
	}
	
	// Add printable characters to buffer
	for _, r := range str {
		if r >= 32 && r <= 126 || r == ' ' || r == '\t' {
			currentBuffer += string(r)
		}
	}
	
	// Store updated buffer
	commandBuffer.Store(sessionID, currentBuffer)
	
	return ""
}


// Admin authentication and log viewing functions

func generateAdminToken() string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("admin-%d", time.Now().Unix())))
	return hex.EncodeToString(hash[:])[:16]
}

func (lc *LoggingController) VerifyAdminToken(token string) bool {
	return token == lc.adminToken
}


func (lc *LoggingController) HandleAdminAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Token string `json:"token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if lc.VerifyAdminToken(req.Token) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"authenticated": true})
	} else {
		http.Error(w, "Invalid admin token", http.StatusUnauthorized)
	}
}

func (lc *LoggingController) HandleAdminLogs(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Admin-Token")
	if !lc.VerifyAdminToken(token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	limit := 100
	if r.URL.Query().Get("limit") != "" {
		if l, err := fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit); err != nil || l != 1 {
			limit = 100
		}
	}

	logs, err := lc.GetCommandLogs("", "", limit, 0)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to retrieve logs: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": logs,
		"count": len(logs),
	})
}