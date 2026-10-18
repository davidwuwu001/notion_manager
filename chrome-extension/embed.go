package extension

import (
	"archive/zip"
	"bytes"
	"embed"
	"net/http"
)

//go:embed manifest.json popup.html popup.js import.js
var files embed.FS

// Download serves the extension bundled with this exact application version.
func Download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, name := range []string{"manifest.json", "popup.html", "popup.js", "import.js"} {
		data, err := files.ReadFile(name)
		if err != nil {
			http.Error(w, "extension unavailable", 500)
			return
		}
		entry, err := archive.Create("notion-manager-extension/" + name)
		if err != nil {
			http.Error(w, "extension unavailable", 500)
			return
		}
		if _, err = entry.Write(data); err != nil {
			http.Error(w, "extension unavailable", 500)
			return
		}
	}
	if err := archive.Close(); err != nil {
		http.Error(w, "extension unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="notion-manager-extension.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buffer.Bytes())
}
