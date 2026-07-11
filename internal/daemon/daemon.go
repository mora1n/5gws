package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/morain/5gws/internal/api"
	"github.com/morain/5gws/internal/auth"
	"github.com/morain/5gws/internal/engine"
	"github.com/morain/5gws/internal/service"
	"github.com/morain/5gws/internal/store"
	"github.com/morain/5gws/internal/updater"
	webassets "github.com/morain/5gws/internal/web"
)

type Options struct {
	Database string
	Version  string
}

func Run(ctx context.Context, opts Options) (runErr error) {
	state, err := store.Open(opts.Database)
	if err != nil {
		return err
	}
	defer state.Close()
	active, err := state.Active(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if runErr != nil {
			if rollbackErr := updater.RollbackPending(active.Bundle.Config.System.StateDir); rollbackErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("update rollback: %w", rollbackErr))
			}
		}
	}()
	if len(active.Bundle.ResolvedRules) == 0 && len(active.Bundle.Rules.Rules)+len(active.Bundle.Rules.Imports) > 0 {
		return errors.New("active revision has no resolved rules; run 5gws install again")
	}
	logs := engine.NewLogBuffer(2 << 20)
	supervisor := engine.NewSupervisor(active.Bundle.Config.System.StateDir, logs)
	root, err := supervisor.Prepare(ctx, active.ID, active.Bundle)
	if err != nil {
		return err
	}
	if err := supervisor.Boot(ctx, root, active.Bundle); err != nil {
		return err
	}
	defer supervisor.Stop()
	go collectMetrics(ctx, state, supervisor)

	application := service.New(state, supervisor)
	server := &api.Server{
		Service: application, Auth: auth.New(state.DB(), 24*time.Hour), Supervisor: supervisor,
		Web: webassets.FS(), Version: opts.Version,
		Updater: updater.New(),
	}
	public := &http.Server{
		Addr: active.Bundle.Config.Panel.Listen, Handler: server.Router(false),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second,
	}
	publicErr := make(chan error, 1)
	go func() {
		log.Printf("panel listening on http://%s", public.Addr)
		publicErr <- public.ListenAndServe()
	}()

	unixServer := &http.Server{Handler: server.Router(true), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second}
	listener, err := listenControl(active.Bundle.Config.System.RunDir)
	if err != nil {
		return err
	}
	defer listener.Close()
	unixErr := make(chan error, 1)
	go func() { unixErr <- unixServer.Serve(listener) }()

	startup := time.NewTimer(5 * time.Second)
	defer startup.Stop()
	select {
	case <-ctx.Done():
	case err := <-supervisor.Fatal():
		return err
	case err := <-publicErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case err := <-unixErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-startup.C:
		if err := updater.Confirm(active.Bundle.Config.System.StateDir); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
		case err := <-supervisor.Fatal():
			return err
		case err := <-publicErr:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		case err := <-unixErr:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = public.Shutdown(shutdown)
	_ = unixServer.Shutdown(shutdown)
	return nil
}

func collectMetrics(ctx context.Context, state *store.Store, supervisor *engine.Supervisor) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		active, err := state.Active(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("metrics active revision: %v", err)
			}
		} else {
			metric := engine.CollectMetrics(supervisor.Status(), active.Bundle.Config.DNS.ListenUDP)
			if err := state.PutMetric(ctx, metric.Timestamp, metric); err != nil && ctx.Err() == nil {
				log.Printf("metrics: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func listenControl(runDir string) (net.Listener, error) {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(runDir, "control.sock")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	return &rootListener{UnixListener: listener}, nil
}

type rootListener struct{ *net.UnixListener }

func (l *rootListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err == nil && uid == 0 {
			return conn, nil
		}
		conn.Close()
	}
}

func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		cred, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if socketErr != nil {
		return 0, socketErr
	}
	if cred == nil {
		return 0, fmt.Errorf("missing peer credentials")
	}
	return cred.Uid, nil
}
