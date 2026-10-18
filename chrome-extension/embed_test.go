package extension

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
)

func TestDownload(t *testing.T) {
	recorder := httptest.NewRecorder()
	Download(recorder, httptest.NewRequest("GET", "/dashboard/extension.zip", nil))
	if recorder.Code != 200 || recorder.Header().Get("Content-Type") != "application/zip" {
		t.Fatal("invalid download response")
	}
	archive, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 4 {
		t.Fatalf("unexpected packaged files: %d", len(archive.File))
	}
	for _, name := range []string{"manifest.json", "popup.html", "popup.js", "import.js"} {
		entry, err := archive.Open("notion-manager-extension/" + name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(entry)
		entry.Close()
		if err != nil || len(data) == 0 {
			t.Fatalf("empty packaged file %s", name)
		}
		if name == "manifest.json" && !json.Valid(data) {
			t.Fatal("invalid manifest")
		}
	}
	bad := httptest.NewRecorder()
	Download(bad, httptest.NewRequest("POST", "/dashboard/extension.zip", nil))
	if bad.Code != 405 {
		t.Fatal("unexpected mutation method accepted")
	}
}
