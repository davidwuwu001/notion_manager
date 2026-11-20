//go:build bridgeprobe

package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
)

func TestBridgeProbeTaggedBuildRequiresFlagAndInheritedFD(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE", "1")
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE_FD", strconv.Itoa(int(writer.Fd())))
	BridgeProbeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probeFromContext(r.Context()) == nil {
			t.Error("enabled probe missing")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", nil))
	writer.Close()
	b, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var d bridgeProbeData
	if err = json.Unmarshal(b, &d); err != nil || d.Protocol != probeResponses {
		t.Fatalf("invalid probe output: %v", err)
	}
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE", "0")
	BridgeProbeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probeFromContext(r.Context()) != nil {
			t.Error("missing opt-in enabled probe")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", nil))
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE", "1")
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE_FD", "1")
	BridgeProbeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probeFromContext(r.Context()) != nil {
			t.Error("unapproved descriptor enabled probe")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", nil))
}

func TestBridgeProbeRejectsRegularFileDescriptor(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "regular")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE", "1")
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE_FD", strconv.Itoa(int(f.Fd())))
	BridgeProbeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probeFromContext(r.Context()) != nil {
			t.Error("regular file descriptor enabled probe")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", nil))
	if n, err := f.Seek(0, io.SeekEnd); err != nil || n != 0 {
		t.Fatalf("unexpected data written: %d %v", n, err)
	}
}
