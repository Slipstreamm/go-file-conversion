package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// fileExpiryDuration is how long files are kept before being cleaned up.
	fileExpiryDuration = 10 * time.Minute
	// ramLimitBytes is the approximate limit for storing files in RAM (16 GB).
	ramLimitBytes = 16 * 1024 * 1024 * 1024
	// cleanupInterval is how often the cleanup routine runs.
	cleanupInterval = 1 * time.Minute
	// defaultDiskPath is where files are stored if RAM limit is exceeded or if configured.
	// On Linux, you might set this to a directory within /dev/shm for RAM-backed disk storage.
	// Example: "/dev/shm/fileconverter_temp"
	// IMPORTANT: Ensure this directory exists and the server has write permissions.
	defaultDiskPath = "temp_files" // Relative to where the app is run
)

// FileMetadata stores information about an uploaded file.
type FileMetadata struct {
	ID            string    `json:"id"`
	OriginalName  string    `json:"originalName"`
	ConvertedName string    `json:"convertedName"` // Name after "conversion"
	Size          int64     `json:"size"`
	UploadTime    time.Time `json:"uploadTime"`
	ExpiryTime    time.Time `json:"expiryTime"`
	IsInMemory    bool      `json:"isInMemory"`
	Path          string    `json:"-"` // Path if stored on disk, not exposed in JSON
	ContentType   string    `json:"contentType"`
}

// FileStore manages the storage of files, either in RAM or on disk.
type FileStore struct {
	mu              sync.Mutex
	files           map[string]*FileMetadata // fileID -> metadata
	ramStore        map[string][]byte        // fileID -> file content
	currentRAMUsage int64
	diskPath        string
}

// NewFileStore creates a new FileStore.
func NewFileStore(diskPath string) *FileStore {
	if diskPath == "" {
		diskPath = defaultDiskPath
	}
	// Ensure the disk path exists
	if err := os.MkdirAll(diskPath, 0755); err != nil {
		log.Printf("Warning: Could not create disk storage path '%s': %v. Falling back to current directory for disk storage.", diskPath, err)
		// Attempt to use a local temp_files directory if the primary one fails
		if err := os.MkdirAll(defaultDiskPath, 0755); err != nil {
			log.Fatalf("Fatal: Could not create any disk storage path: %v", err)
		}
		diskPath = defaultDiskPath
	}
	log.Printf("Using disk storage path: %s", diskPath)

	fs := &FileStore{
		files:           make(map[string]*FileMetadata),
		ramStore:        make(map[string][]byte),
		currentRAMUsage: 0,
		diskPath:        diskPath,
	}
	go fs.cleanupRoutine()
	return fs
}

// generateID creates a unique ID for a file.
func generateID() (string, error) {
	b := make([]byte, 16) // 128-bit random ID
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// AddFile stores an uploaded file.
func (fs *FileStore) AddFile(file multipart.File, header *multipart.FileHeader) (*FileMetadata, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fileID, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate file ID: %w", err)
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file content: %w", err)
	}
	fileSize := int64(len(fileBytes))

	meta := &FileMetadata{
		ID:           fileID,
		OriginalName: header.Filename,
		// In a real scenario, ConvertedName might change based on target format
		ConvertedName: header.Filename,
		Size:          fileSize,
		UploadTime:    time.Now(),
		ExpiryTime:    time.Now().Add(fileExpiryDuration),
		ContentType:   header.Header.Get("Content-Type"),
	}

	// Simulate conversion - replace this with actual conversion logic
	// For this example, the "converted" content is the same as the original.
	// convertedFileBytes, convertedFileName, err := performConversion(fileBytes, header.Filename, "target_format_placeholder")
	// if err != nil {
	// 	return nil, fmt.Errorf("conversion failed: %w", err)
	// }
	// meta.ConvertedName = convertedFileName
	// fileSize = int64(len(convertedFileBytes)) // Update size if conversion changes it
	// fileBytes = convertedFileBytes // Use converted bytes for storage

	// Decision: Store in RAM or on Disk
	if fs.currentRAMUsage+fileSize <= ramLimitBytes {
		fs.ramStore[fileID] = fileBytes
		fs.currentRAMUsage += fileSize
		meta.IsInMemory = true
		log.Printf("Stored file %s (%s, %.2f MB) in RAM. Current RAM usage: %.2f MB / %.2f MB",
			fileID, meta.OriginalName, float64(fileSize)/1024/1024, float64(fs.currentRAMUsage)/1024/1024, float64(ramLimitBytes)/1024/1024)
	} else {
		diskFilePath := filepath.Join(fs.diskPath, fileID+"_"+header.Filename)
		err := os.WriteFile(diskFilePath, fileBytes, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to write file to disk: %w", err)
		}
		meta.IsInMemory = false
		meta.Path = diskFilePath
		log.Printf("Stored file %s (%s, %.2f MB) on Disk at %s. RAM limit exceeded.",
			fileID, meta.OriginalName, float64(fileSize)/1024/1024, diskFilePath)
	}

	fs.files[fileID] = meta
	return meta, nil
}

// GetFile retrieves a file for download.
func (fs *FileStore) GetFile(fileID string) (*FileMetadata, []byte, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	meta, exists := fs.files[fileID]
	if !exists || time.Now().After(meta.ExpiryTime) {
		if exists { // File expired, remove it
			fs.deleteFileInternal(fileID)
		}
		return nil, nil, fmt.Errorf("file not found or expired")
	}

	if meta.IsInMemory {
		content, ok := fs.ramStore[fileID]
		if !ok { // Should not happen if metadata is consistent
			return nil, nil, fmt.Errorf("file metadata inconsistency: RAM file not found")
		}
		return meta, content, nil
	}

	// File is on disk
	content, err := os.ReadFile(meta.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read file from disk: %w", err)
	}
	return meta, content, nil
}

// deleteFileInternal performs the actual deletion of a file and its metadata.
// This function expects the lock to be already held.
func (fs *FileStore) deleteFileInternal(fileID string) {
	meta, exists := fs.files[fileID]
	if !exists {
		return
	}

	if meta.IsInMemory {
		if data, ok := fs.ramStore[fileID]; ok {
			fs.currentRAMUsage -= int64(len(data))
			delete(fs.ramStore, fileID)
		}
	} else {
		if err := os.Remove(meta.Path); err != nil {
			log.Printf("Error deleting file %s from disk: %v", meta.Path, err)
		}
	}
	delete(fs.files, fileID)
	log.Printf("Deleted file %s (%s). RAM usage: %.2f MB", fileID, meta.OriginalName, float64(fs.currentRAMUsage)/1024/1024)
}

// cleanupRoutine periodically removes expired files.
func (fs *FileStore) cleanupRoutine() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		fs.mu.Lock()
		now := time.Now()
		for id, meta := range fs.files {
			if now.After(meta.ExpiryTime) {
				log.Printf("Cleaning up expired file: %s (%s)", id, meta.OriginalName)
				fs.deleteFileInternal(id)
			}
		}
		fs.mu.Unlock()
	}
}

// handleUpload handles file uploads.
func handleUpload(fs *FileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
			return
		}

		// Max upload size: e.g., 500MB. Adjust as needed.
		// This is important to prevent abuse.
		if err := r.ParseMultipartForm(500 << 20); err != nil {
			log.Printf("Error parsing multipart form: %v", err)
			http.Error(w, fmt.Sprintf("Could not parse multipart form: %v", err), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			log.Printf("Error retrieving file from form: %v", err)
			http.Error(w, "Error retrieving file from form-data", http.StatusBadRequest)
			return
		}
		defer file.Close()

		// targetFormat := r.FormValue("targetFormat") // If you add target format selection

		meta, err := fs.AddFile(file, header)
		if err != nil {
			log.Printf("Error adding file: %v", err)
			http.Error(w, fmt.Sprintf("Error processing file: %v", err), http.StatusInternalServerError)
			return
		}

		response := map[string]string{
			"fileId":      meta.ID,
			"fileName":    meta.ConvertedName, // Send the name of the "converted" file
			"downloadUrl": "/download/" + meta.ID,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("Error encoding response: %v", err)
			// Client already received 200, too late to send error code
		}
	}
}

// handleDownload handles file downloads.
func handleDownload(fs *FileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fileID := filepath.Base(r.URL.Path) // Extract fileID from path like "/download/fileID"

		meta, content, err := fs.GetFile(fileID)
		if err != nil {
			log.Printf("Error getting file %s for download: %v", fileID, err)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		// Set headers for download
		w.Header().Set("Content-Disposition", "attachment; filename=\""+meta.ConvertedName+"\"")
		if meta.ContentType != "" {
			w.Header().Set("Content-Type", meta.ContentType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream") // Generic binary
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", int64(len(content))))

		_, err = io.Copy(w, bytes.NewReader(content))
		if err != nil {
			log.Printf("Error writing file %s to response: %v", fileID, err)
			// Don't try to write an http.Error if headers already sent
		}
	}
}

// performConversion is a PLACEHOLDER for actual file conversion logic.
// You need to implement this function based on the types of conversions you want to support.
// This might involve using external libraries or command-line tools.
//
// Parameters:
//   - inputFileBytes: The byte content of the original file.
//   - originalFilename: The original name of the file, useful for context or extension.
//   - targetFormat: A string indicating the desired output format (e.g., "png", "pdf").
//
// Returns:
//   - outputFileBytes: The byte content of the converted file.
//   - outputFilename: The desired filename for the converted file.
//   - error: An error if conversion fails.
func performConversion(inputFileBytes []byte, originalFilename string, targetFormat string) ([]byte, string, error) {
	log.Printf("Attempting to 'convert' file: %s to target format: %s", originalFilename, targetFormat)
	// --- START OF PLACEHOLDER ---
	// In this placeholder, we are just returning the original file without any changes.
	// You would replace this with your actual conversion code.
	// For example, if converting an image to PNG, you'd use an image library.
	// If converting a document to PDF, you might use 'soffice', 'pandoc', or a Go PDF library.

	outputFileBytes := make([]byte, len(inputFileBytes))
	copy(outputFileBytes, inputFileBytes)

	// Example: Change extension if it was a real conversion
	// outputFilename := strings.TrimSuffix(originalFilename, filepath.Ext(originalFilename)) + "." + targetFormat
	outputFilename := originalFilename // Keep original name for this passthrough example

	log.Printf("Placeholder conversion: returning original content for %s", originalFilename)
	// --- END OF PLACEHOLDER ---
	return outputFileBytes, outputFilename, nil
}

func main() {
	// You can get this path from an environment variable or config file
	// For /dev/shm, ensure the directory exists and has correct permissions.
	// E.g., export FILECONVERTER_DISK_PATH="/dev/shm/myconverter_temp"
	diskStoragePath := os.Getenv("FILECONVERTER_DISK_PATH")
	if diskStoragePath == "" {
		diskStoragePath = defaultDiskPath // Fallback to local "temp_files"
	}

	fileStore := NewFileStore(diskStoragePath)

	mux := http.NewServeMux()

	// Serve static HTML page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// This assumes the HTML file (from the first immersive block) is named index.html
		// and is in the same directory as the Go executable, or you adjust the path.
		// For simplicity in this example, I'm not embedding the HTML.
		// You would typically serve it from a file.
		// http.ServeFile(w, r, "index.html")
		// For now, let's just send a simple message if index.html is not present.
		// To make this runnable: save the HTML from the first block as 'index.html'
		// in the same directory as this Go program.
		http.ServeFile(w, r, "index.html")
	})

	mux.HandleFunc("/upload", handleUpload(fileStore))
	mux.HandleFunc("/download/", handleDownload(fileStore)) // Note the trailing slash

	port := "8080"
	log.Printf("Server starting on port %s", port)
	log.Printf("File storage: RAM (up to %.2f GB), fallback to disk at '%s'", float64(ramLimitBytes)/1024/1024/1024, fileStore.diskPath)
	log.Printf("Uploaded files persist for %v", fileExpiryDuration)

	err := http.ListenAndServe(":"+port, mux)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
