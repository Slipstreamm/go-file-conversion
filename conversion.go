package main

import (
	"bytes"
	"fmt"
	"image"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/mholt/archiver/v3"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	_ "golang.org/x/image/tiff" // Import TIFF decoder
)

// FileType represents the type of file
type FileType string

const (
	FileTypeImage   FileType = "image"
	FileTypeAudio   FileType = "audio"
	FileTypeVideo   FileType = "video"
	FileTypeDoc     FileType = "document"
	FileTypeArchive FileType = "archive"
	FileTypeOther   FileType = "other"
)

// ConversionMap maps file types to their supported conversion formats
var ConversionMap = map[FileType]map[string][]string{
	FileTypeImage: {
		"jpg":  {"png", "gif", "webp", "bmp", "tiff"},
		"jpeg": {"png", "gif", "webp", "bmp", "tiff"},
		"png":  {"jpg", "gif", "webp", "bmp", "tiff"},
		"gif":  {"jpg", "png", "webp", "bmp", "tiff"},
		"webp": {"jpg", "png", "gif", "bmp", "tiff"},
		"bmp":  {"jpg", "png", "gif", "webp", "tiff"},
		"tiff": {"jpg", "png", "gif", "webp", "bmp"},
		"svg":  {"png", "jpg"},
	},
	FileTypeAudio: {
		"mp3":  {"wav", "ogg", "flac", "aac", "wma"},
		"wav":  {"mp3", "ogg", "flac", "aac", "wma"},
		"ogg":  {"mp3", "wav", "flac", "aac", "wma"},
		"flac": {"mp3", "wav", "ogg", "aac", "wma"},
		"aac":  {"mp3", "wav", "ogg", "flac", "wma"},
		"wma":  {"mp3", "wav", "ogg", "flac", "aac"},
	},
	FileTypeVideo: {
		"mp4":  {"avi", "mov", "webm", "mkv", "flv", "mp3", "wav", "ogg", "flac", "aac"},
		"avi":  {"mp4", "mov", "webm", "mkv", "flv", "mp3", "wav", "ogg", "flac", "aac"},
		"mov":  {"mp4", "avi", "webm", "mkv", "flv", "mp3", "wav", "ogg", "flac", "aac"},
		"webm": {"mp4", "avi", "mov", "mkv", "flv", "mp3", "wav", "ogg", "flac", "aac"},
		"mkv":  {"mp4", "avi", "mov", "webm", "flv", "mp3", "wav", "ogg", "flac", "aac"},
		"flv":  {"mp4", "avi", "mov", "webm", "mkv", "mp3", "wav", "ogg", "flac", "aac"},
	},
	FileTypeDoc: {
		"docx": {"pdf", "txt", "html", "md"},
		"doc":  {"pdf", "txt", "html", "md"},
		"pdf":  {"txt", "html", "md"},
		"txt":  {"pdf", "html", "md"},
		"html": {"pdf", "txt", "md"},
		"md":   {"html", "txt", "pdf"},
		"pptx": {"pdf"},
		"ppt":  {"pdf"},
		"xlsx": {"csv", "pdf"},
		"xls":  {"csv", "pdf"},
	},
	FileTypeArchive: {
		"zip": {"tar"},
		"tar": {"zip"},
		"rar": {"zip", "tar"},
	},
}

// DetectFileType determines the type of file based on content and extension
func DetectFileType(fileBytes []byte, filename string) (FileType, string) {
	// Get file extension
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != "" {
		ext = ext[1:] // Remove the dot
	}

	// Detect content type
	contentType := http.DetectContentType(fileBytes)

	// Determine file type based on content type and extension
	if strings.HasPrefix(contentType, "image/") {
		return FileTypeImage, ext
	} else if strings.HasPrefix(contentType, "audio/") {
		return FileTypeAudio, ext
	} else if strings.HasPrefix(contentType, "video/") {
		return FileTypeVideo, ext
	} else if strings.HasPrefix(contentType, "application/pdf") ||
		strings.HasPrefix(contentType, "application/msword") ||
		strings.HasPrefix(contentType, "application/vnd.openxmlformats-officedocument.wordprocessingml.document") ||
		strings.HasPrefix(contentType, "text/") {
		return FileTypeDoc, ext
	} else if strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/x-tar") ||
		strings.HasPrefix(contentType, "application/x-rar-compressed") {
		return FileTypeArchive, ext
	}

	// Fallback to extension-based detection
	switch ext {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "tiff", "svg":
		return FileTypeImage, ext
	case "mp3", "wav", "ogg", "flac", "aac", "wma":
		return FileTypeAudio, ext
	case "mp4", "avi", "mov", "webm", "mkv", "flv":
		return FileTypeVideo, ext
	case "pdf", "doc", "docx", "txt", "html", "md", "ppt", "pptx", "xls", "xlsx", "csv":
		return FileTypeDoc, ext
	case "zip", "tar", "rar":
		return FileTypeArchive, ext
	}

	return FileTypeOther, ext
}

// GetSupportedConversionFormats returns a list of supported target formats for a given file
func GetSupportedConversionFormats(fileType FileType, extension string) []string {
	if formatMap, ok := ConversionMap[fileType]; ok {
		if formats, ok := formatMap[extension]; ok {
			return formats
		}
	}
	return []string{}
}

// performConversion handles file conversion based on file type and target format
func performConversion(inputFileBytes []byte, originalFilename string, targetFormat string) ([]byte, string, error) {
	log.Printf("Converting file: %s to target format: %s", originalFilename, targetFormat)

	// Detect file type
	fileType, sourceExt := DetectFileType(inputFileBytes, originalFilename)

	// Generate output filename
	baseName := strings.TrimSuffix(originalFilename, filepath.Ext(originalFilename))
	outputFilename := baseName + "." + targetFormat

	// Check if conversion is supported
	supported := false
	if formatMap, ok := ConversionMap[fileType]; ok {
		if formats, ok := formatMap[sourceExt]; ok {
			// Check if targetFormat is in the list of supported formats
			for _, format := range formats {
				if format == targetFormat {
					supported = true
					break
				}
			}
		}
	}

	if !supported {
		return nil, "", fmt.Errorf("conversion from %s to %s is not supported", sourceExt, targetFormat)
	}

	// Perform conversion based on file type
	switch fileType {
	case FileTypeImage:
		return convertImage(inputFileBytes, outputFilename, targetFormat)
	case FileTypeAudio:
		return convertAudio(inputFileBytes, outputFilename, sourceExt, targetFormat)
	case FileTypeVideo:
		return convertVideo(inputFileBytes, outputFilename, sourceExt, targetFormat)
	case FileTypeDoc:
		return convertDocument(inputFileBytes, outputFilename, sourceExt, targetFormat)
	case FileTypeArchive:
		return convertArchive(inputFileBytes, outputFilename, sourceExt, targetFormat)
	default:
		return nil, "", fmt.Errorf("unsupported file type for conversion")
	}
}

// convertImage converts image files using the imaging library
func convertImage(inputFileBytes []byte, outputFilename, targetFormat string) ([]byte, string, error) {
	// Read the image
	src, _, err := image.Decode(bytes.NewReader(inputFileBytes))
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image: %w", err)
	}

	// Create a temporary file for the output
	tempDir := os.TempDir()
	tempOutputPath := filepath.Join(tempDir, outputFilename)

	// Handle SVG to raster format conversion
	if strings.HasSuffix(strings.ToLower(outputFilename), ".svg") {
		return nil, "", fmt.Errorf("conversion to SVG is not supported")
	} else if strings.HasSuffix(strings.ToLower(outputFilename), ".png") ||
		strings.HasSuffix(strings.ToLower(outputFilename), ".jpg") ||
		strings.HasSuffix(strings.ToLower(outputFilename), ".jpeg") {

		// Check if input is SVG
		if bytes.HasPrefix(inputFileBytes, []byte("<?xml")) || bytes.HasPrefix(inputFileBytes, []byte("<svg")) {
			// Convert SVG to PNG/JPG
			return convertSVGToRaster(inputFileBytes, outputFilename, targetFormat)
		}
	}

	// Convert the image using imaging
	img := imaging.Clone(src)

	// For WebP format, we need to use a different approach since imaging doesn't support WebP encoding
	if targetFormat == "webp" {
		// For WebP, we'll use FFmpeg as a fallback since imaging doesn't support WebP encoding
		// First save as PNG temporarily
		tempPngPath := filepath.Join(tempDir, "temp_for_webp.png")
		err = imaging.Save(img, tempPngPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to save intermediate image: %w", err)
		}

		// Check if FFmpeg is installed
		_, err := exec.LookPath("ffmpeg")
		if err != nil {
			os.Remove(tempPngPath) // Clean up the temporary PNG
			return nil, "", fmt.Errorf("WebP conversion requires FFmpeg which is not installed or not in PATH")
		}

		// Use FFmpeg to convert PNG to WebP with proper parameters
		cmd := exec.Command("ffmpeg", "-i", tempPngPath, "-c:v", "libwebp", "-quality", "80", "-y", tempOutputPath)
		output, err := cmd.CombinedOutput()

		// Clean up the temporary PNG
		os.Remove(tempPngPath)

		if err != nil {
			return nil, "", fmt.Errorf("WebP conversion failed: %s - %w", string(output), err)
		}
	} else {
		// For other formats, use imaging library
		err = imaging.Save(img, tempOutputPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to save converted image: %w", err)
		}
	}

	// Read the converted file
	outputBytes, err := os.ReadFile(tempOutputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read converted image: %w", err)
	}

	// Clean up
	os.Remove(tempOutputPath)

	return outputBytes, outputFilename, nil
}

// convertSVGToRaster converts SVG to raster formats like PNG or JPG
func convertSVGToRaster(inputFileBytes []byte, outputFilename, _ string) ([]byte, string, error) {
	// Create a temporary file for the output
	tempDir := os.TempDir()
	tempOutputPath := filepath.Join(tempDir, outputFilename)

	// Parse SVG
	icon, err := oksvg.ReadIconStream(bytes.NewReader(inputFileBytes))
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse SVG: %w", err)
	}

	// Set size
	width := 1000.0
	height := 1000.0
	icon.SetTarget(0, 0, width, height)

	// Create raster image
	rgba := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	scanner := rasterx.NewScannerGV(int(width), int(height), rgba, rgba.Bounds())
	raster := rasterx.NewDasher(int(width), int(height), scanner)
	icon.Draw(raster, 1.0)

	// Save the image
	err = imaging.Save(rgba, tempOutputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to save converted image: %w", err)
	}

	// Read the converted file
	outputBytes, err := os.ReadFile(tempOutputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read converted image: %w", err)
	}

	// Clean up
	os.Remove(tempOutputPath)

	return outputBytes, outputFilename, nil
}

// convertAudio converts audio files using FFmpeg
func convertAudio(inputFileBytes []byte, outputFilename, sourceExt, targetFormat string) ([]byte, string, error) {
	return convertMediaWithFFmpeg(inputFileBytes, outputFilename, sourceExt, targetFormat, "audio")
}

// convertVideo converts video files using FFmpeg
func convertVideo(inputFileBytes []byte, outputFilename, sourceExt, targetFormat string) ([]byte, string, error) {
	mediaType := "video"
	if targetFormat == "mp3" || targetFormat == "wav" || targetFormat == "ogg" || targetFormat == "flac" || targetFormat == "aac" {
		mediaType = "audio" // Audio extraction from video
	}
	return convertMediaWithFFmpeg(inputFileBytes, outputFilename, sourceExt, targetFormat, mediaType)
}

// convertMediaWithFFmpeg uses FFmpeg to convert audio and video files
func convertMediaWithFFmpeg(inputFileBytes []byte, outputFilename, sourceExt, _ string, mediaType string) ([]byte, string, error) {
	// Check if FFmpeg is installed
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, "", fmt.Errorf("FFmpeg is not installed or not in PATH")
	}

	// Create temporary files for input and output
	tempDir := os.TempDir()
	tempInputPath := filepath.Join(tempDir, "input."+sourceExt)
	tempOutputPath := filepath.Join(tempDir, outputFilename)

	// Write input file
	if err := os.WriteFile(tempInputPath, inputFileBytes, 0644); err != nil {
		return nil, "", fmt.Errorf("failed to write temporary input file: %w", err)
	}

	// Prepare FFmpeg command
	var cmd *exec.Cmd

	// Handle different conversion scenarios
	if mediaType == "audio" && (strings.HasPrefix(sourceExt, "mp4") ||
		strings.HasPrefix(sourceExt, "avi") ||
		strings.HasPrefix(sourceExt, "mov") ||
		strings.HasPrefix(sourceExt, "webm") ||
		strings.HasPrefix(sourceExt, "mkv") ||
		strings.HasPrefix(sourceExt, "flv")) {
		// Extract audio from video
		cmd = exec.Command("ffmpeg", "-i", tempInputPath, "-vn", "-acodec", "copy", tempOutputPath)
	} else if mediaType == "audio" {
		// Audio conversion with quality options
		bitrate := "192k" // Default bitrate
		cmd = exec.Command("ffmpeg", "-i", tempInputPath, "-ab", bitrate, tempOutputPath)
	} else {
		// Video conversion with quality options
		resolution := "1280x720" // Default resolution (720p)
		cmd = exec.Command("ffmpeg", "-i", tempInputPath, "-s", resolution, tempOutputPath)
	}

	// Execute FFmpeg
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("FFmpeg conversion failed: %s - %w", string(output), err)
	}

	// Read the converted file
	outputBytes, err := os.ReadFile(tempOutputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read converted file: %w", err)
	}

	// Clean up
	os.Remove(tempInputPath)
	os.Remove(tempOutputPath)

	return outputBytes, outputFilename, nil
}

// convertDocument converts document files using external tools
func convertDocument(inputFileBytes []byte, outputFilename, sourceExt, targetFormat string) ([]byte, string, error) {
	// Create temporary files for input and output
	tempDir := os.TempDir()
	tempInputPath := filepath.Join(tempDir, "input."+sourceExt)
	tempOutputPath := filepath.Join(tempDir, outputFilename)

	// Write input file
	if err := os.WriteFile(tempInputPath, inputFileBytes, 0644); err != nil {
		return nil, "", fmt.Errorf("failed to write temporary input file: %w", err)
	}

	// Handle Markdown conversions
	if sourceExt == "md" && (targetFormat == "html" || targetFormat == "txt") {
		return convertMarkdown(tempInputPath, tempOutputPath, targetFormat)
	}

	// For text to PDF conversion, we can use a simple approach
	if sourceExt == "txt" && targetFormat == "pdf" {
		// Check if wkhtmltopdf is installed (a common tool for HTML/text to PDF conversion)
		_, err := exec.LookPath("wkhtmltopdf")
		if err != nil {
			os.Remove(tempInputPath)
			return nil, "", fmt.Errorf("PDF conversion requires wkhtmltopdf which is not installed or not in PATH")
		}

		// Use wkhtmltopdf to convert text to PDF
		cmd := exec.Command("wkhtmltopdf", tempInputPath, tempOutputPath)
		output, err := cmd.CombinedOutput()

		// Clean up the temporary input file
		os.Remove(tempInputPath)

		if err != nil {
			return nil, "", fmt.Errorf("PDF conversion failed: %s - %w", string(output), err)
		}
	} else {
		// For other document conversions, we would need more specialized tools
		os.Remove(tempInputPath)
		return nil, "", fmt.Errorf("document conversion from %s to %s is not implemented yet", sourceExt, targetFormat)
	}

	// Read the converted file
	outputBytes, err := os.ReadFile(tempOutputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read converted document: %w", err)
	}

	// Clean up
	os.Remove(tempOutputPath)

	return outputBytes, outputFilename, nil
}

// convertMarkdown converts Markdown to HTML or TXT
func convertMarkdown(inputPath, outputPath, targetFormat string) ([]byte, string, error) {
	// Read the markdown content
	mdContent, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read markdown file: %w", err)
	}

	var outputContent []byte

	if targetFormat == "html" {
		// Simple markdown to HTML conversion
		// In a real implementation, you would use a proper markdown parser
		htmlContent := "<html><body>\n"
		lines := strings.Split(string(mdContent), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "# ") {
				htmlContent += "<h1>" + line[2:] + "</h1>\n"
			} else if strings.HasPrefix(line, "## ") {
				htmlContent += "<h2>" + line[3:] + "</h2>\n"
			} else if strings.HasPrefix(line, "### ") {
				htmlContent += "<h3>" + line[4:] + "</h3>\n"
			} else if strings.HasPrefix(line, "- ") {
				htmlContent += "<li>" + line[2:] + "</li>\n"
			} else if line == "" {
				htmlContent += "<br/>\n"
			} else {
				htmlContent += "<p>" + line + "</p>\n"
			}
		}
		htmlContent += "</body></html>"
		outputContent = []byte(htmlContent)
	} else if targetFormat == "txt" {
		// Markdown to plain text (just strip markdown syntax)
		outputContent = mdContent
	} else {
		return nil, "", fmt.Errorf("unsupported markdown conversion to %s", targetFormat)
	}

	// Write the output
	if err := os.WriteFile(outputPath, outputContent, 0644); err != nil {
		return nil, "", fmt.Errorf("failed to write converted file: %w", err)
	}

	// Clean up the input file
	os.Remove(inputPath)

	// Read the converted file
	outputBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read converted file: %w", err)
	}

	// Get the filename from the output path
	outputFilename := filepath.Base(outputPath)

	return outputBytes, outputFilename, nil
}

// convertArchive handles archive operations (compression/extraction)
func convertArchive(inputFileBytes []byte, outputFilename, sourceExt, targetFormat string) ([]byte, string, error) {
	// Create temporary files for input and output
	tempDir := os.TempDir()
	tempInputPath := filepath.Join(tempDir, "input."+sourceExt)
	tempOutputPath := filepath.Join(tempDir, outputFilename)

	// Write input file
	if err := os.WriteFile(tempInputPath, inputFileBytes, 0644); err != nil {
		return nil, "", fmt.Errorf("failed to write temporary input file: %w", err)
	}

	// Create a temporary directory for extraction
	tempExtractDir := filepath.Join(tempDir, "extract_"+filepath.Base(tempInputPath))
	if err := os.MkdirAll(tempExtractDir, 0755); err != nil {
		os.Remove(tempInputPath)
		return nil, "", fmt.Errorf("failed to create temporary extraction directory: %w", err)
	}

	// Handle archive conversion
	var err error

	// First extract the source archive
	switch sourceExt {
	case "zip":
		err = archiver.Unarchive(tempInputPath, tempExtractDir)
	case "tar":
		err = archiver.Unarchive(tempInputPath, tempExtractDir)
	case "rar":
		err = archiver.Unarchive(tempInputPath, tempExtractDir)
	default:
		err = fmt.Errorf("unsupported archive format: %s", sourceExt)
	}

	if err != nil {
		os.Remove(tempInputPath)
		os.RemoveAll(tempExtractDir)
		return nil, "", fmt.Errorf("failed to extract archive: %w", err)
	}

	// Then create the target archive
	switch targetFormat {
	case "zip":
		err = archiver.Archive([]string{tempExtractDir}, tempOutputPath)
	case "tar":
		err = archiver.Archive([]string{tempExtractDir}, tempOutputPath)
	case "rar":
		err = fmt.Errorf("creating RAR archives is not supported: RAR is a proprietary format that requires licensing")
	default:
		err = fmt.Errorf("unsupported archive format: %s", targetFormat)
	}

	// Clean up the input file and extraction directory
	os.Remove(tempInputPath)
	os.RemoveAll(tempExtractDir)

	if err != nil {
		return nil, "", fmt.Errorf("failed to create archive: %w", err)
	}

	// Read the converted file
	outputBytes, err := os.ReadFile(tempOutputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read converted archive: %w", err)
	}

	// Clean up
	os.Remove(tempOutputPath)

	return outputBytes, outputFilename, nil
}
