//go:build darwin || linux

package hostclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
)

func fakeAppHost(control net.Conn, response native.Response, token []byte, mode string) int {
	open := func(fd uintptr) (net.Conn, error) {
		file := os.NewFile(fd, "private")
		defer file.Close()
		conn, err := net.FileConn(file)
		if err != nil {
			return nil, err
		}
		given := make([]byte, 32)
		_, err = io.ReadFull(conn, given)
		if err != nil || !bytes.Equal(given, token) {
			conn.Close()
			return nil, native.ErrProtocol
		}
		return conn, nil
	}
	media, err := open(5)
	if err != nil {
		return 60
	}
	defer media.Close()
	app, err := open(6)
	if err != nil {
		return 61
	}
	defer app.Close()
	var mediaMu sync.Mutex
	done := make(chan struct{})
	defer func() { app.Close(); <-done }()
	go func() {
		defer close(done)
		blocked := make(map[string]native.Request)
		reply := func(r native.Request, out *native.AppResponse, code string) error {
			return native.WriteMessage(app, native.Response{Version: 1, Build: "test/commit", ID: r.ID, Epoch: r.Epoch, App: out, Error: code})
		}
		for {
			var r native.Request
			if native.ReadMessage(app, &r) != nil {
				return
			}
			if r.Version != 1 || r.Build != "test/commit" || r.App == nil {
				return
			}
			switch r.Operation {
			case "app_launch":
				if r.App.Launch.BundleID == "blocked" {
					blocked[r.ID] = r
					continue
				}
				if reply(r, &native.AppResponse{Window: &appcontrol.Window{Handle: "owned"}}, "") != nil {
					return
				}
			case "app_cancel":
				original, ok := blocked[r.App.CancelID]
				if ok {
					delete(blocked, r.App.CancelID)
					if reply(original, nil, "cancelled") != nil {
						return
					}
				}
				if reply(r, nil, "") != nil {
					return
				}
			case "app_observe":
				a := r.App.Authority
				o := appcontrol.Observation{Display: appcontrol.Display{Resource: a.Resource, Epoch: a.Epoch, ID: 1, Virtual: true}, Window: appcontrol.Window{Handle: r.App.WindowHandle, SnapshotRevision: 1}, Width: 800, Height: 600}
				out := &native.AppResponse{Observation: &o}
				if !r.App.IncludePNG {
					if reply(r, out, "") != nil {
						return
					}
					continue
				}
				png := append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, bytes.Repeat([]byte{1}, native.SnapshotChunkBytes+77)...)
				sum := sha256.Sum256(png)
				out.Snapshot = &native.SnapshotDescriptor{ID: r.App.SnapshotID, Resource: a.Resource, Epoch: a.Epoch, WindowHandle: r.App.WindowHandle, SnapshotRevision: 1, DisplayID: 1, Size: uint32(len(png)), SHA256: hex.EncodeToString(sum[:])}
				if mode == "app-bad-hash" {
					out.Snapshot.SHA256 = strings.Repeat("0", 64)
				}
				if mode == "app-oversize" {
					out.Snapshot.Size = native.MaxMediaPayloadBytes + 1
				}
				before := mode == "app-response-first" || mode == "app-eof"
				if before && reply(r, out, "") != nil {
					return
				}
				if mode == "app-eof" {
					media.Close()
					continue
				}
				for offset := 0; offset < len(png); offset += native.SnapshotChunkBytes {
					sample := native.MediaSample{Kind: native.MediaSnapshot, StreamID: r.App.SnapshotID, Epoch: a.Epoch, DisplayID: 1, SnapshotOffset: uint32(offset), SnapshotTotal: uint32(len(png)), PNG: png[offset:min(offset+native.SnapshotChunkBytes, len(png))]}
					if mode == "app-wrong-identity" {
						sample.DisplayID = 2
					}
					if mode == "app-reordered" && offset == 0 {
						sample.SnapshotOffset = 1
						sample.PNG = sample.PNG[:len(sample.PNG)-1]
					}
					mediaMu.Lock()
					e := native.WriteMediaSample(media, sample)
					mediaMu.Unlock()
					if e != nil {
						return
					}
				}
				if !before && reply(r, out, "") != nil {
					return
				}
			default:
				if reply(r, nil, "") != nil {
					return
				}
			}
		}
	}()
	for {
		var r native.Request
		if native.ReadMessage(control, &r) != nil {
			return 0
		}
		response.ID = r.ID
		response.App = nil
		response.Error = ""
		if native.WriteMessage(control, response) != nil {
			return 62
		}
	}
}
