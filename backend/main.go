package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	uploadMutex sync.Mutex
)

type FileMetadata struct {
	OriginalName string `json:"original_name"`
	ContentType  string `json:"content_type"`
	FileHash     string `json:"file_hash"`
}

func enableCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		if r.Method == "OPTIONS" {
			return
		}

		next.ServeHTTP(w, r)
	}
}

func main() {
	http.HandleFunc("/upload", enableCORS(uploadHandler))
	http.HandleFunc("/complete/", enableCORS(completeHandler))
	http.HandleFunc("/download/", enableCORS(downloadHandler))
	http.HandleFunc("/metadata/", enableCORS(metadataHandler))

	fmt.Println("Server running on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(32 << 20)

	fileId := r.FormValue("fileId")
	chunkIndex := r.FormValue("chunkIndex")

	chunkDir := filepath.Join("chunks", fileId)
	if err := os.MkdirAll(chunkDir, os.ModePerm); err != nil {
		http.Error(w, "Failed to create chunk directory", http.StatusInternalServerError)
		return
	}

	file, _, err := r.FormFile("chunk")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	chunkPath := filepath.Join(chunkDir, chunkIndex)
	out, err := os.Create(chunkPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		http.Error(w, "Failed to save chunk", http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Chunk uploaded successfully"))
}

func completeHandler(w http.ResponseWriter, r *http.Request) {
	uploadMutex.Lock()
	defer uploadMutex.Unlock()

	encodedFileName := r.URL.Path[len("/complete/"):]
	decodedBytes, err := base64.URLEncoding.DecodeString(encodedFileName)
	if err != nil {
		http.Error(w, "Invalid file name encoding", http.StatusBadRequest)
		return
	}

	originalFileName, err := url.QueryUnescape(string(decodedBytes))
	if err != nil {
		http.Error(w, "Failed to decode file name", http.StatusBadRequest)
		return
	}

	// Tạo thư mục uploads nếu chưa tồn tại
	if err := os.MkdirAll("uploads", os.ModePerm); err != nil {
		http.Error(w, "Failed to create uploads directory", http.StatusInternalServerError)
		return
	}

	dataFilePath := filepath.Join("uploads", encodedFileName)
	metadataFilePath := filepath.Join("uploads", encodedFileName+".json")

	// Merge chunks
	chunkDir := filepath.Join("chunks", encodedFileName)
	files, err := os.ReadDir(chunkDir)
	if err != nil {
		http.Error(w, "Failed to read chunks directory", http.StatusInternalServerError)
		return
	}

	out, err := os.Create(dataFilePath)
	if err != nil {
		http.Error(w, "Failed to create output file", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	hasher := sha256.New()
	mw := io.MultiWriter(out, hasher) // Vừa ghi file vừa tính hash

	for _, f := range files {
		chunkPath := filepath.Join(chunkDir, f.Name())
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			http.Error(w, "Failed to open chunk", http.StatusInternalServerError)
			return
		}

		if _, err := io.Copy(mw, chunkFile); err != nil {
			chunkFile.Close()
			http.Error(w, "Failed to merge chunk", http.StatusInternalServerError)
			return
		}
		chunkFile.Close()
	}

	// Xác định ContentType
	// default="image/jpeg,image/gif,image/png,image/bmp,image/webp,audio/ogg,audio/mpeg,audio/mp4,video/mp4,video/mpeg,video/quicktime,video/webm,application/msword,application/excel,application/pdf,application/powerpoint,text/plain,application/x-zip"

	contentType := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(originalFileName)) {
	case ".mov":
		contentType = "video/quicktime"
	case ".mp4":
		contentType = "video/mp4"
	case ".png":
		contentType = "image/png"
	case ".jpg":
		contentType = "image/jpeg"
	case ".jpeg":
		contentType = "image/jpeg"
	case ".gif":
		contentType = "image/gif"
	case ".webp":
		contentType = "image/webp"
	}

	// Lưu metadata
	metadata := FileMetadata{
		OriginalName: originalFileName,
		ContentType:  contentType,
		FileHash:     fmt.Sprintf("%x", hasher.Sum(nil)),
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		http.Error(w, "Failed to marshal metadata", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(metadataFilePath, metadataJSON, 0644); err != nil {
		http.Error(w, "Failed to save metadata", http.StatusInternalServerError)
		return
	}

	// Dọn dẹp chunks
	if err := os.RemoveAll(chunkDir); err != nil {
		log.Printf("Warning: Failed to clean up chunks: %v", err)
	}

	w.Write([]byte(encodedFileName))
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	encodedName := r.URL.Path[len("/download/"):]
	dataPath := filepath.Join("uploads", encodedName)
	metadataPath := filepath.Join("uploads", encodedName+".json")

	// Kiểm tra file data
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Đọc metadata
	metadataJSON, err := os.ReadFile(metadataPath)
	if err != nil {
		http.Error(w, "Metadata not found", http.StatusNotFound)
		return
	}

	var metadata FileMetadata
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		http.Error(w, "Invalid metadata format", http.StatusInternalServerError)
		return
	}

	// Set headers
	w.Header().Set("Content-Type", metadata.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, metadata.OriginalName))
	w.Header().Set("ETag", metadata.FileHash)

	// Phục vụ file
	http.ServeFile(w, r, dataPath)
}

func metadataHandler(w http.ResponseWriter, r *http.Request) {
	encodedName := r.URL.Path[len("/metadata/"):]
	metadataPath := filepath.Join("uploads", encodedName+".json")

	metadataJSON, err := os.ReadFile(metadataPath)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Metadata not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(metadataJSON)
}
