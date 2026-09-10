package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestTaskWakeupDialUsesSOCKSProxy(t *testing.T) {
	if os.Getenv(wakeupProxyChildEnv) == "1" {
		req, err := http.NewRequest(http.MethodGet, "https://"+wakeupProxyTargetHost, nil)
		if err != nil {
			t.Fatal(err)
		}
		proxyURL, err := http.ProxyFromEnvironment(req)
		if err != nil || proxyURL == nil {
			t.Fatalf("resolve environment proxy: %v", err)
		}
		originalURL := *proxyURL
		dialWakeupThroughEnvProxy(t)
		if *proxyURL != originalURL {
			t.Fatal("wakeup dial mutated the cached environment proxy URL")
		}
		return
	}

	for _, scheme := range []string{"socks5h", "socks5"} {
		t.Run(scheme, func(t *testing.T) {
			// Given an authenticated SOCKS proxy and a target resolved only by it.
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			received := make(chan socksWakeupRequest, 1)
			go func() {
				received <- readSOCKSWakeupRequest(ln)
			}()
			proxyURL := &url.URL{
				Scheme: scheme,
				Host:   ln.Addr().String(),
				User:   url.UserPassword("proxy-user", "proxy:p@ss"),
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTaskWakeupDialUsesSOCKSProxy$", "-test.v")
			cmd.Env = append(environWithoutProxyVars(),
				wakeupProxyChildEnv+"=1", "HTTPS_PROXY="+proxyURL.String(),
			)

			// When the daemon opens its real wakeup connection through the proxy.
			out, runErr := cmd.CombinedOutput()
			ln.Close()
			request := <-received

			// Then SOCKS authentication and the unresolved domain reach the proxy.
			if runErr != nil || request.err != nil {
				t.Fatalf("SOCKS request: %v; child exit: %v\n%s", request.err, runErr, out)
			}
			if request.target != wakeupProxyTargetHost+":443" {
				t.Fatalf("SOCKS target = %q, want %s:443", request.target, wakeupProxyTargetHost)
			}
		})
	}
}

func TestTaskWakeupProxyHonorsNoProxy(t *testing.T) {
	if os.Getenv(wakeupProxyChildEnv) == "1" {
		req, err := http.NewRequest(http.MethodGet, "https://"+wakeupProxyTargetHost, nil)
		if err != nil {
			t.Fatal(err)
		}
		proxyURL, err := taskWakeupProxy(req)
		if err != nil || proxyURL != nil {
			t.Fatalf("NO_PROXY matched target: proxy = %v, error = %v", proxyURL, err)
		}
		return
	}

	// Given a SOCKS proxy with this target excluded, resolve in a fresh process.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTaskWakeupProxyHonorsNoProxy$", "-test.v")
	cmd.Env = append(environWithoutProxyVars(),
		wakeupProxyChildEnv+"=1", "HTTPS_PROXY=socks5h://127.0.0.1:1", "NO_PROXY="+wakeupProxyTargetHost,
	)
	// When the wakeup proxy callback runs, then no proxy is selected.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("NO_PROXY resolution: %v\n%s", err, out)
	}
}

type socksWakeupRequest struct {
	target string
	err    error
}

func readSOCKSWakeupRequest(ln net.Listener) socksWakeupRequest {
	conn, err := ln.Accept()
	if err != nil {
		return socksWakeupRequest{err: err}
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return socksWakeupRequest{err: err}
	}
	target, err := readAuthenticatedSOCKSTarget(conn)
	return socksWakeupRequest{target: target, err: err}
}

func readAuthenticatedSOCKSTarget(conn net.Conn) (string, error) {
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return "", err
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", err
	}
	if greeting[0] != 5 || !bytes.Contains(methods, []byte{2}) {
		return "", fmt.Errorf("SOCKS greeting = %v %v, want version 5 with password authentication", greeting, methods)
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil {
		return "", err
	}
	var authVersion [1]byte
	if _, err := io.ReadFull(conn, authVersion[:]); err != nil {
		return "", err
	}
	user, err := readSOCKSString(conn)
	if err != nil {
		return "", err
	}
	password, err := readSOCKSString(conn)
	if err != nil {
		return "", err
	}
	if authVersion[0] != 1 || user != "proxy-user" || password != "proxy:p@ss" {
		return "", fmt.Errorf("SOCKS proxy credentials were not preserved")
	}
	if _, err := conn.Write([]byte{1, 0}); err != nil {
		return "", err
	}
	var connect [4]byte
	if _, err := io.ReadFull(conn, connect[:]); err != nil {
		return "", err
	}
	if connect != [4]byte{5, 1, 0, 3} {
		return "", fmt.Errorf("SOCKS connect = %v, want a DOMAIN target without local DNS", connect)
	}
	host, err := readSOCKSString(conn)
	if err != nil {
		return "", err
	}
	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(port[:]))), nil
}

func readSOCKSString(reader io.Reader) (string, error) {
	var size [1]byte
	if _, err := io.ReadFull(reader, size[:]); err != nil {
		return "", err
	}
	value := make([]byte, int(size[0]))
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return string(value), nil
}
