//go:build darwin || linux

package hostclient

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "internal-vscreen-host" {
		os.Exit(fakeHost())
	}
	os.Exit(m.Run())
}

func fakeHost() int {
	socket := os.NewFile(3, "control")
	bootstrap := os.NewFile(4, "bootstrap")
	token := make([]byte, 32)
	if _, err := io.ReadFull(bootstrap, token); err != nil {
		return 2
	}
	bootstrap.Close()
	conn, err := net.FileConn(socket)
	if err != nil {
		return 3
	}
	socket.Close()
	defer conn.Close()
	mode := os.Getenv("HOSTCLIENT_TEST_MODE")
	var hello native.Request
	if err := native.ReadMessage(conn, &hello); err != nil {
		return 4
	}
	if mode == "reject-token" {
		token[0] ^= 1
	}
	if hello.Operation != "hello" || !bytes.Equal(token, hello.Token) || hello.Build != "test/commit" || hello.Version != 1 {
		return 5
	}
	if mode == "startup-exit" {
		return 6
	}
	if mode == "startup-stall" {
		<-time.NewTimer(10 * time.Second).C
		return 7
	}
	response := native.Response{Version: 1, Build: "test/commit", ID: hello.ID, Epoch: protocol.VscreenEpoch{NativeEpoch: "test-native"}}
	if hello.Media {
		response.Epoch.NativeEpoch = strings.Repeat("a", 64)
	}
	switch mode {
	case "wrong-build":
		response.Build = "foreign"
	case "wrong-version":
		response.Version = 2
	case "wrong-id":
		response.ID = "foreign"
	case "missing-epoch":
		response.Epoch.NativeEpoch = ""
	case "partial":
		conn.Write([]byte{0, 0})
		return 8
	case "oversize":
		conn.Write([]byte{0, 2, 0, 0})
		return 8
	case "malformed":
		conn.Write([]byte{0, 0, 0, 1, '!'})
		return 8
	}
	if err := native.WriteMessage(conn, response); err != nil {
		return 9
	}
	if hello.Media {
		return fakeMediaHost(conn, response, token, mode)
	}
	quiescent := false
	for {
		var request native.Request
		err := native.ReadMessage(conn, &request)
		if err != nil {
			if mode == "ignore-eof" {
				<-time.NewTimer(10 * time.Second).C
			}
			if err == io.EOF {
				os.WriteFile(os.Getenv("HOSTCLIENT_TEST_CLEANUP"), []byte("EOF: owned resources released\n"), 0600)
			}
			return 0
		}
		if len(request.Token) != 0 {
			return 11
		}
		if path := os.Getenv("HOSTCLIENT_TEST_TRACE"); path != "" {
			trace, _ := json.Marshal(struct {
				Args      []string
				Operation string
			}{os.Args, request.Operation})
			os.WriteFile(path, trace, 0600)
		}
		response.ID = request.ID
		response.Error = ""
		if request.Operation != "list" {
			response.Epoch = protocol.VscreenEpoch{NativeEpoch: "test-native", DisplayGeneration: "display", GeometryRevision: 1}
			response.Display = &native.Display{ID: 10, UUID: "uuid", Width: 1600, Height: 900}
			switch request.Operation {
			case "quiesce":
				quiescent = true
			case "dispose":
				if !quiescent {
					response.Error = "quiescence_required"
				}
				response.Display = nil
			}
			response.Quiescent = quiescent
		}
		switch mode {
		case "call-exit":
			return 10
		case "call-stall":
			<-time.NewTimer(10 * time.Second).C
			return 10
		case "epoch-drift":
			response.Epoch.NativeEpoch = "replacement"
		case "remote-error":
			response.Error = "geometry_conflict"
		case "hostile-error":
			os.Stderr.Write(bytes.Repeat(hello.Token, 32768))
			response.Error = string(hello.Token)
		case "partial-call":
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], 40)
			conn.Write(size[:])
			conn.Write([]byte("{"))
			return 12
		}
		if err := native.WriteMessage(conn, response); err != nil {
			return 13
		}
	}
}
