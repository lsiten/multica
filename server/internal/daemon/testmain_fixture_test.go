//go:build darwin || linux

package daemon

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func daemonFixtureEntry(_ *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == applicationhost.Entrypoint && os.Getenv("APPLICATION_DAEMON_FIXTURE") == "1" {
		if path := os.Getenv("APPLICATION_HOST_PID_FILE"); path != "" {
			if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				os.Exit(96)
			}
		}
		if err := applicationhost.Run(context.Background(), os.Args[2]); err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(95)
		}
		os.Exit(0)
	}

	if len(os.Args) == 5 && os.Args[1] == "readopt-parent" {
		if err := runReAdoptParent(os.Args[2], os.Args[3], os.Args[4]); err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(98)
		}
		os.Exit(0)
	}
	if len(os.Args) == 4 && os.Args[1] == "survival-parent" {
		if err := runSurvivalParent(os.Args[2], os.Args[3]); err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(97)
		}
		os.Exit(0)
	}

	if len(os.Args) != 2 || os.Args[1] != runtimeproc.Entrypoint {
		return
	}
	bootstrap, err := runtimeproc.ReadBootstrap(os.Stdin, "fixture/commit")
	if err != nil {
		os.Exit(92)
	}
	if os.Getenv("MULTICA_TOKEN") != "" {
		os.Exit(93)
	}
	switch bootstrap.Identity.Scope.Service {
	case "environment":
		err = RunEnvironmentService(context.Background(), bootstrap)
	case "application":
		err = RunApplicationService(context.Background(), bootstrap)
	case "gateway":
		err = RunGatewayService(context.Background(), bootstrap)
	case "worker":
		err = RunWorkerService(context.Background(), bootstrap)
	case "control":
		err = RunControlService(context.Background(), bootstrap)
	default:
		err = RunMirrorService(context.Background(), bootstrap)
	}
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(94)
	}
	os.Exit(0)
}
