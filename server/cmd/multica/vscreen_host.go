package main

import "github.com/multica-ai/multica/server/internal/vscreen/native"

func runVscreenHost() error { return native.RunHost(version + "/" + commit) }
