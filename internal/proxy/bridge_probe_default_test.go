//go:build !bridgeprobe

package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
)

func TestBridgeProbeOrdinaryBuildCannotEnable(t *testing.T) {
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE", "1")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	t.Setenv("NOTION_MANAGER_BRIDGE_PROBE_FD", strconv.Itoa(int(writer.Fd())))
	BridgeProbeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probeFromContext(r.Context()) != nil {
			t.Error("ordinary build enabled probe")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", nil))
	writer.Close()
	data, err := io.ReadAll(reader)
	if err != nil || len(data) > 0 {
		t.Fatal("ordinary build emitted probe output")
	}
}
