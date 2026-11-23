package netns

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"time"

	vnetns "github.com/vishvananda/netns"
)

// RunInNetns executes fn inside the target Linux network namespace while safely
// pinning the OS thread and restoring the original network namespace afterward.
func RunInNetns(nsPath string, fn func() error) error {
	if nsPath == "" {
		return fn()
	}

	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		origNS, err := vnetns.Get()
		if err != nil {
			errCh <- fmt.Errorf("get current netns: %w", err)
			return
		}
		defer origNS.Close()

		targetNS, err := vnetns.GetFromPath(nsPath)
		if err != nil {
			errCh <- fmt.Errorf("open target netns %s: %w", nsPath, err)
			return
		}
		defer targetNS.Close()

		if err := vnetns.Set(targetNS); err != nil {
			errCh <- fmt.Errorf("setns %s: %w", nsPath, err)
			return
		}
		defer func() {
			_ = vnetns.Set(origNS)
		}()

		errCh <- fn()
	}()

	return <-errCh
}

// DialInNetns dials a network connection strictly inside the specified network namespace.
// Because net.Dialer can spawn helper goroutines, we create the socket inside a locked OS thread.
func DialInNetns(ctx context.Context, nsPath, network, address string, localAddr net.Addr, timeout time.Duration) (net.Conn, error) {
	type dialRes struct {
		conn net.Conn
		err  error
	}
	ch := make(chan dialRes, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if nsPath != "" {
			origNS, err := vnetns.Get()
			if err != nil {
				ch <- dialRes{err: fmt.Errorf("get orig netns: %w", err)}
				return
			}
			defer origNS.Close()

			targetNS, err := vnetns.GetFromPath(nsPath)
			if err != nil {
				ch <- dialRes{err: fmt.Errorf("open target netns: %w", err)}
				return
			}
			defer targetNS.Close()

			if err := vnetns.Set(targetNS); err != nil {
				ch <- dialRes{err: fmt.Errorf("setns: %w", err)}
				return
			}
			defer func() {
				_ = vnetns.Set(origNS)
			}()
		}

		d := &net.Dialer{
			Timeout:       timeout,
			LocalAddr:     localAddr,
			FallbackDelay: -1, // disable Happy Eyeballs helper goroutines to stay on locked thread
		}
		conn, err := d.DialContext(ctx, network, address)
		ch <- dialRes{conn: conn, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.conn, r.err
	}
}

// NewNetnsResolver creates a pure-Go DNS resolver whose upstream UDP/TCP queries
// execute inside the target Pod's network namespace.
func NewNetnsResolver(nsPath string, timeout time.Duration) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return DialInNetns(ctx, nsPath, network, address, nil, timeout)
		},
	}
}
