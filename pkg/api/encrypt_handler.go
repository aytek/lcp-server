// Copyright 2025 iTech Mobi. All rights reserved.

package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/readium/readium-lcp-server/encrypt"
)

// EncryptResponse is returned as JSON in the X-Encrypt-Metadata header.
type EncryptResponse struct {
	UUID          string `json:"uuid"`
	EncryptionKey string `json:"encryption_key"` // base64-encoded
	Size          uint32 `json:"size"`
	Checksum      string `json:"checksum"`
	ContentType   string `json:"content_type"`
	Title         string `json:"title"`
	FileName      string `json:"file_name"`
}

// EncryptEPUB accepts an EPUB upload, encrypts it, and returns the encrypted
// file as the response body with metadata in the X-Encrypt-Metadata header.
// It does NOT store the file permanently or create a publication record.
func (a *APICtrl) EncryptEPUB(w http.ResponseWriter, r *http.Request) {
	log.Info("EncryptEPUB: request received")

	// 1. Parse multipart form (max 50 MB)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		log.Errorf("EncryptEPUB: failed to parse multipart form: %v", err)
		http.Error(w, "failed to parse multipart form", http.StatusBadRequest)
		return
	}

	// 2. Get the uploaded file
	file, header, err := r.FormFile("file")
	if err != nil {
		log.Errorf("EncryptEPUB: missing file field: %v", err)
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Optional title field
	title := r.FormValue("title")

	// 3. Create temp directory for processing
	tempDir, err := os.MkdirTemp("", "lcp-encrypt-*")
	if err != nil {
		log.Errorf("EncryptEPUB: failed to create temp dir: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tempDir)

	// 4. Save the uploaded file to temp directory
	inputPath := filepath.Join(tempDir, header.Filename)
	if err := saveMultipartFile(file, inputPath); err != nil {
		log.Errorf("EncryptEPUB: failed to save uploaded file: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// 5. Generate UUID
	contentID := uuid.New().String()

	// 6. Create output directory
	outputDir := filepath.Join(tempDir, "output")
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		log.Errorf("EncryptEPUB: failed to create output dir: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// 7. Encrypt the publication
	// Parameters: contentID, contentKey, inputPath, tempRepo, outputRepo,
	//             storageRepo, storageURL, storageFilename, extractCover, pdfNoMeta
	publication, err := encrypt.ProcessEncryption(
		contentID, "", inputPath, "", outputDir,
		"", "", "", false, false,
	)
	if err != nil {
		log.Errorf("EncryptEPUB: encryption failed: %v", err)
		http.Error(w, "encryption failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Use the title from the EPUB metadata if not provided in form
	pubTitle := publication.Title
	if title != "" {
		pubTitle = title
	}

	// 8. Read the encrypted file
	encryptedPath := filepath.Join(outputDir, publication.FileName)
	encryptedFile, err := os.Open(encryptedPath)
	if err != nil {
		log.Errorf("EncryptEPUB: failed to open encrypted file: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer encryptedFile.Close()

	// 9. Build metadata
	metadata := EncryptResponse{
		UUID:          publication.UUID,
		EncryptionKey: base64.StdEncoding.EncodeToString(publication.EncryptionKey),
		Size:          publication.Size,
		Checksum:      publication.Checksum,
		ContentType:   publication.ContentType,
		Title:         pubTitle,
		FileName:      publication.FileName,
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		log.Errorf("EncryptEPUB: failed to marshal metadata: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// 10. Set metadata in header, stream encrypted file as body
	w.Header().Set("X-Encrypt-Metadata", string(metadataJSON))
	w.Header().Set("Content-Type", publication.ContentType)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+publication.FileName+"\"")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, encryptedFile); err != nil {
		log.Errorf("EncryptEPUB: failed to stream encrypted file: %v", err)
		return
	}

	log.Infof("EncryptEPUB: success, uuid=%s, title=%s, size=%d", publication.UUID, pubTitle, publication.Size)
}

// saveMultipartFile saves an uploaded multipart file to disk.
func saveMultipartFile(src io.Reader, dst string) error {
	if src == nil {
		return errors.New("source is nil")
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}
