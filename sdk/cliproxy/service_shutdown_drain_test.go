package cliproxy

// Regression tests for graceful shutdown (fork branch feature/graceful-shutdown-drain).
//
// Upstream v7.2.121 created the shutdown deadline when Run started, so every stop
// more than 30 s after launch began with an already-expired deadline: Run returned
// at once, the process exited and clients lost in-flight streams ("Connection lost
// mid-response"). Instead of sleeping 30 s these tests shrink the drain window and
// stop a service that has been up longer than that window, which is the same
// condition scaled down.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

type drainTestService struct {
	addr     string
	launched time.Time
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

type drainStreamResult struct {
	status int
	body   string
	err    error
}

func startDrainTestService(t *testing.T, drain time.Duration, routes func(*gin.Engine)) *drainTestService {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	authDir := filepath.Join(dir, "auth")
	configPath := filepath.Join(dir, "config.yaml")
	port := freeLoopbackPort(t)
	configYAML := fmt.Sprintf("host: 127.0.0.1\nport: %d\nauth-dir: %q\n", port, authDir)
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := &config.Config{Host: "127.0.0.1", Port: port, AuthDir: authDir}
	svc, err := NewBuilder().
		WithConfig(cfg).
		WithConfigPath(configPath).
		WithWatcherFactory(func(string, string, func(*config.Config)) (*WatcherWrapper, error) {
			return &WatcherWrapper{}, nil
		}).
		WithServerOptions(api.WithRouterConfigurator(func(engine *gin.Engine, _ *handlers.BaseAPIHandler, _ *config.Config) {
			engine.GET("/drain-test/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
			routes(engine)
		})).
		Build()
	if err != nil {
		t.Fatalf("build service: %v", err)
	}
	svc.drainTimeout = drain

	ctx, cancel := context.WithCancel(context.Background())
	d := &drainTestService{
		addr:     fmt.Sprintf("127.0.0.1:%d", port),
		launched: time.Now(),
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go func() {
		d.err = svc.Run(ctx)
		close(d.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-d.done:
		case <-time.After(drain + 5*time.Second):
			t.Error("Run did not return during test cleanup")
		}
	})

	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, errGet := client.Get("http://" + d.addr + "/drain-test/ping")
		if errGet == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return d
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("service did not become ready: %v", errGet)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// waitUptime blocks until the service has been running for at least uptime.
func (d *drainTestService) waitUptime(uptime time.Duration) {
	if remaining := time.Until(d.launched.Add(uptime)); remaining > 0 {
		time.Sleep(remaining)
	}
}

func (d *drainTestService) stream(path string) <-chan drainStreamResult {
	out := make(chan drainStreamResult, 1)
	go func() {
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Get("http://" + d.addr + path)
		if err != nil {
			out <- drainStreamResult{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, errRead := io.ReadAll(resp.Body)
		out <- drainStreamResult{status: resp.StatusCode, body: string(body), err: errRead}
	}()
	return out
}

func (d *drainTestService) waitListenerClosed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", d.addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("listener still accepted new connections after shutdown began")
}

// holdStream registers a streaming route that sends one event, then waits for
// release (or for the request to be cancelled) before sending the final event.
func holdStream(engine *gin.Engine, path string, started chan<- struct{}, release <-chan struct{}, completed *atomic.Bool) {
	var once sync.Once
	engine.GET(path, func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Status(http.StatusOK)
		_, _ = io.WriteString(c.Writer, "data: first\n\n")
		c.Writer.Flush()
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-c.Request.Context().Done():
			return
		}
		_, _ = io.WriteString(c.Writer, "data: last\n\n")
		c.Writer.Flush()
		completed.Store(true)
	})
}

func closeOnce(ch chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func receiveWithin[T any](t *testing.T, ch <-chan T, within time.Duration, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(within):
		t.Fatalf("timed out after %s waiting for %s", within, what)
	}
	var zero T
	return zero
}

func TestServiceShutdownDrainsInFlightStreamStartedAfterLaunchWindow(t *testing.T) {
	const drain = time.Second
	started := make(chan struct{})
	release := make(chan struct{})
	releaseNow := closeOnce(release)
	t.Cleanup(releaseNow)
	var completed atomic.Bool

	d := startDrainTestService(t, drain, func(engine *gin.Engine) {
		holdStream(engine, "/drain-test/stream", started, release, &completed)
	})
	// The regression condition: the service has been up longer than its drain
	// window, so a deadline created at launch would already have expired.
	d.waitUptime(drain + drain/2)

	result := d.stream("/drain-test/stream")
	receiveWithin(t, started, 3*time.Second, "the stream to start")

	d.cancel() // the equivalent of SIGTERM reaching StartService

	select {
	case <-d.done:
		t.Fatalf("Run returned (%v) while a stream was still in flight; the process would exit and cut it", d.err)
	case <-time.After(drain / 3):
	}

	releaseNow()
	got := receiveWithin(t, result, 3*time.Second, "the in-flight stream to finish")
	if got.err != nil || got.status != http.StatusOK || got.body != "data: first\n\ndata: last\n\n" {
		t.Fatalf("in-flight stream = status %d body %q err %v; want the complete stream", got.status, got.body, got.err)
	}
	if !completed.Load() {
		t.Fatal("stream handler did not complete")
	}
	receiveWithin(t, d.done, drain, "Run to return once the stream drained")
	if d.err != nil && !errors.Is(d.err, context.Canceled) {
		t.Fatalf("Run returned %v", d.err)
	}
}

func TestServiceShutdownRefusesNewRequestsWhileDraining(t *testing.T) {
	const drain = 2 * time.Second
	started := make(chan struct{})
	release := make(chan struct{})
	releaseNow := closeOnce(release)
	t.Cleanup(releaseNow)
	var completed atomic.Bool
	var newRequests atomic.Int32

	d := startDrainTestService(t, drain, func(engine *gin.Engine) {
		holdStream(engine, "/drain-test/stream", started, release, &completed)
		engine.GET("/drain-test/new", func(c *gin.Context) {
			newRequests.Add(1)
			c.String(http.StatusOK, "served")
		})
	})
	result := d.stream("/drain-test/stream")
	receiveWithin(t, started, 3*time.Second, "the stream to start")

	// A keep-alive client that finished a request before shutdown holds an idle
	// connection into the drain.
	keepAlive := &http.Client{Timeout: 2 * time.Second}
	defer keepAlive.CloseIdleConnections()
	resp, err := keepAlive.Get("http://" + d.addr + "/drain-test/new")
	if err != nil {
		t.Fatalf("pre-shutdown request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// A client that connected and began a request before shutdown, but had not
	// finished sending it, is already inside the HTTP server when the drain starts.
	partial, err := net.Dial("tcp", d.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = partial.Close() }()
	if _, err = io.WriteString(partial, "GET /drain-test/new HTTP/1.1\r\n"); err != nil {
		t.Fatalf("write partial request: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // let the protocol multiplexer hand it to the HTTP server
	before := newRequests.Load()

	d.cancel()
	// New connections are refused outright.
	d.waitListenerClosed(t)

	// Work that arrives during the drain is refused before any handler runs, so
	// nothing has streamed and clients can retry it safely.
	if resp, err = keepAlive.Get("http://" + d.addr + "/drain-test/new"); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("request on a pre-shutdown keep-alive connection was served during drain (status %d)", resp.StatusCode)
	}
	if _, err = io.WriteString(partial, "Host: drain-test\r\n\r\n"); err == nil {
		_ = partial.SetReadDeadline(time.Now().Add(3 * time.Second))
		if resp, errRead := http.ReadResponse(bufio.NewReader(partial), nil); errRead == nil {
			_ = resp.Body.Close()
			t.Fatalf("request completed during drain was answered with status %d; want it refused", resp.StatusCode)
		}
	}
	if got := newRequests.Load(); got != before {
		t.Fatalf("%d new request(s) reached a handler during drain", got-before)
	}

	// Meanwhile the stream that was already in flight is still being drained.
	select {
	case <-d.done:
		t.Fatalf("Run returned (%v) while a stream was still in flight", d.err)
	default:
	}
	releaseNow()
	got := receiveWithin(t, result, 3*time.Second, "the in-flight stream to finish")
	if got.err != nil || got.body != "data: first\n\ndata: last\n\n" {
		t.Fatalf("in-flight stream = body %q err %v; want the complete stream", got.body, got.err)
	}
	receiveWithin(t, d.done, drain, "Run to return once the stream drained")
}

func TestServiceShutdownForceClosesStreamsThatOutliveDrainWindow(t *testing.T) {
	const drain = 400 * time.Millisecond
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once

	d := startDrainTestService(t, drain, func(engine *gin.Engine) {
		engine.GET("/drain-test/endless", func(c *gin.Context) {
			c.Header("Content-Type", "text/event-stream")
			c.Status(http.StatusOK)
			_, _ = io.WriteString(c.Writer, "data: first\n\n")
			c.Writer.Flush()
			close(started)
			<-c.Request.Context().Done()
			once.Do(func() { close(cancelled) })
		})
	})
	d.waitUptime(2 * drain)

	result := d.stream("/drain-test/endless")
	receiveWithin(t, started, 3*time.Second, "the stream to start")

	stopAt := time.Now()
	d.cancel()
	receiveWithin(t, d.done, drain+3*time.Second, "Run to return after the drain window")
	if elapsed := time.Since(stopAt); elapsed < drain*3/4 {
		t.Fatalf("Run returned %s after shutdown began; want it to wait for the %s drain window", elapsed, drain)
	}

	got := receiveWithin(t, result, 3*time.Second, "the force-closed stream to end")
	if got.err == nil {
		t.Fatalf("stream outliving the drain window ended cleanly with body %q; want its connection force-closed", got.body)
	}
	receiveWithin(t, cancelled, 3*time.Second, "the force-closed request's context to be cancelled")
}
