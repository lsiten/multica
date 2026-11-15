package main

import "github.com/multica-ai/multica/server/internal/mirror/nativecapture"

func runMirrorCaptureHost() error {
	return nativecapture.RunHost(version + "/" + commit)
}
