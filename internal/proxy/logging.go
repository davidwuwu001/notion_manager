package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

var debugLoggingEnabled atomic.Bool
var apiInputLoggingEnabled atomic.Bool
var apiOutputLoggingEnabled atomic.Bool
var notionRequestLoggingEnabled atomic.Bool
var notionResponseLoggingEnabled atomic.Bool
var globalLogWriter = newDebugFilterWriter()

var debugLogTags = []string{
	"[debug]",
	"[bridge]",
	"[session]",
	"[thinking]",
	"[req]",
	"[search]",
	"[upload-debug]",
}

type debugFilterWriter struct {
	mu       sync.Mutex
	out      io.Writer
	file     *os.File
	path     string
	size     int64
	maxBytes int64
	backups  int
}

func newDebugFilterWriter() *debugFilterWriter {
	return &debugFilterWriter{
		out:      os.Stderr,
		maxBytes: 10 << 20,
		backups:  3,
	}
}

func init() {
	debugLoggingEnabled.Store(false)
	log.SetOutput(globalLogWriter)
}

func (w *debugFilterWriter) Write(p []byte) (int, error) {
	if !DebugLoggingEnabled() && isDebugLogLine(p) {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.path == "" {
		if w.out == nil {
			return os.Stderr.Write(p)
		}
		return w.out.Write(p)
	}
	if w.size > 0 && int64(len(p)) <= w.maxBytes && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	written := 0
	for len(p) > 0 {
		if w.size >= w.maxBytes {
			if err := w.rotate(); err != nil {
				return written, err
			}
		}
		if w.file == nil {
			return written, fmt.Errorf("log file is unavailable")
		}
		chunk := p
		if remaining := w.maxBytes - w.size; int64(len(chunk)) > remaining {
			chunk = p[:int(remaining)]
		}
		n, err := w.file.Write(chunk)
		written += n
		w.size += int64(n)
		if err != nil {
			return written, err
		}
		if n != len(chunk) {
			return written, io.ErrShortWrite
		}
		p = p[n:]
	}
	return written, nil
}

func (w *debugFilterWriter) rotate() (err error) {
	if w.file != nil {
		if err = w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	defer func() {
		f, openErr := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if openErr == nil {
			w.file = f
			w.out = f
			if info, statErr := f.Stat(); statErr == nil {
				w.size = info.Size()
			} else {
				openErr = statErr
			}
		}
		if err == nil {
			err = openErr
		}
	}()
	if err = os.Remove(fmt.Sprintf("%s.%d", w.path, w.backups)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for index := w.backups - 1; index >= 1; index-- {
		if err = os.Rename(fmt.Sprintf("%s.%d", w.path, index), fmt.Sprintf("%s.%d", w.path, index+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(w.path, w.path+".1")
}

func (w *debugFilterWriter) SetOutputPath(path string) error {
	path = strings.TrimSpace(path)
	w.mu.Lock()
	defer w.mu.Unlock()

	target := io.Writer(os.Stderr)
	var file *os.File

	if path != "" {
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
		}

		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		if err = f.Chmod(0600); err != nil {
			_ = f.Close()
			return err
		}
		target = f
		file = f
	}

	oldFile := w.file
	w.out = target
	w.file = file
	w.path = path
	w.size = 0
	if file != nil {
		if info, err := file.Stat(); err == nil {
			w.size = info.Size()
		}
	}

	if oldFile != nil && oldFile != file {
		_ = oldFile.Close()
	}
	return nil
}

func isDebugLogLine(p []byte) bool {
	line := string(p)
	for _, tag := range debugLogTags {
		if strings.Contains(line, tag) {
			return true
		}
	}
	return false
}

func SetDebugLoggingEnabled(enabled bool) {
	debugLoggingEnabled.Store(enabled)
}

func DebugLoggingEnabled() bool {
	return debugLoggingEnabled.Load()
}

func ConfigureLogOutput(path string) error {
	return globalLogWriter.SetOutputPath(path)
}

func SetAPILogInputEnabled(enabled bool) {
	apiInputLoggingEnabled.Store(enabled)
}

func APILogInputEnabled() bool {
	return apiInputLoggingEnabled.Load()
}

func SetAPILogOutputEnabled(enabled bool) {
	apiOutputLoggingEnabled.Store(enabled)
}

func APILogOutputEnabled() bool {
	return apiOutputLoggingEnabled.Load()
}

func SetNotionRequestLoggingEnabled(enabled bool) {
	notionRequestLoggingEnabled.Store(enabled)
}

func NotionRequestLoggingEnabled() bool {
	return notionRequestLoggingEnabled.Load()
}

func SetNotionResponseLoggingEnabled(enabled bool) {
	notionResponseLoggingEnabled.Store(enabled)
}

func NotionResponseLoggingEnabled() bool {
	return notionResponseLoggingEnabled.Load()
}

func LogAPIInputJSON(requestID, label string, v interface{}) {
	if !APILogInputEnabled() {
		return
	}
	logJSONPayload("[api-in]", requestID, label, v)
}

func LogAPIInputJSONBytes(requestID, label string, raw []byte) {
	if !APILogInputEnabled() {
		return
	}
	logJSONBytesPayload("[api-in]", requestID, label, raw)
}

func LogAPIInputText(requestID, label, text string) {
	if !APILogInputEnabled() {
		return
	}
	logTextPayload("[api-in]", requestID, label, text)
}

func LogAPIOutputJSON(requestID, label string, v interface{}) {
	if !APILogOutputEnabled() {
		return
	}
	logJSONPayload("[api-out]", requestID, label, v)
}

func LogAPIOutputText(requestID, label, text string) {
	if !APILogOutputEnabled() {
		return
	}
	logTextPayload("[api-out]", requestID, label, text)
}

func LogNotionRequestJSON(requestID, label string, v interface{}) {
	if !NotionRequestLoggingEnabled() {
		return
	}
	logJSONPayload("[notion-req]", requestID, label, v)
}

func LogNotionResponseJSON(requestID, label string, v interface{}) {
	if !NotionResponseLoggingEnabled() {
		return
	}
	logJSONPayload("[notion-resp]", requestID, label, v)
}

func LogNotionResponseJSONBytes(requestID, label string, raw []byte) {
	if !NotionResponseLoggingEnabled() {
		return
	}
	logJSONBytesPayload("[notion-resp]", requestID, label, raw)
}

func LogNotionResponseText(requestID, label, text string) {
	if !NotionResponseLoggingEnabled() {
		return
	}
	logTextPayload("[notion-resp]", requestID, label, text)
}

func logJSONPayload(tag, requestID, label string, v interface{}) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("%s %s %s marshal error: %v", tag, requestID, label, err)
		return
	}
	logTextPayload(tag, requestID, label, string(raw))
}

func logJSONBytesPayload(tag, requestID, label string, raw []byte) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err == nil {
		logTextPayload(tag, requestID, label, buf.String())
		return
	}
	logTextPayload(tag, requestID, label, string(raw))
}

func logTextPayload(tag, requestID, label, text string) {
	label = strings.TrimSpace(label)
	requestID = strings.TrimSpace(requestID)
	if requestID != "" && label != "" {
		log.Printf("%s %s %s\n%s", tag, requestID, label, text)
		return
	}
	if requestID != "" {
		log.Printf("%s %s\n%s", tag, requestID, text)
		return
	}
	if label != "" {
		log.Printf("%s %s\n%s", tag, label, text)
		return
	}
	log.Printf("%s\n%s", tag, text)
}
