package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/morain/5gws/internal/quic"
	"github.com/morain/5gws/internal/render"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/ssrust"
	"github.com/morain/5gws/internal/store"
)

type Supervisor struct {
	mu        sync.Mutex
	lifecycle context.Context
	logs      *LogBuffer
	current   *processGroup
	fatal     chan error
	stateDir  string
}

type processGroup struct {
	cancel   context.CancelFunc
	cmds     []*exec.Cmd
	done     chan error
	root     string
	bundle   store.Bundle
	stopped  chan struct{}
	stopOnce sync.Once
	wait     sync.WaitGroup
}

type ProcessStatus struct {
	Name string `json:"name"`
	PID  int    `json:"pid"`
}

const readinessTimeout = 15 * time.Second
const shutdownTimeout = 5 * time.Second

var errShutdownTimeout = errors.New("process group shutdown timed out")

func (s *Supervisor) Status() []ProcessStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	out := make([]ProcessStatus, 0, len(s.current.cmds)+1)
	for _, cmd := range s.current.cmds {
		if cmd.Process != nil {
			out = append(out, ProcessStatus{Name: filepath.Base(cmd.Path), PID: cmd.Process.Pid})
		}
	}
	out = append(out, ProcessStatus{Name: "gateway", PID: os.Getpid()})
	return out
}

func NewSupervisor(ctx context.Context, stateDir string, logs *LogBuffer) *Supervisor {
	return &Supervisor{lifecycle: ctx, stateDir: stateDir, logs: logs, fatal: make(chan error, 1)}
}

func (s *Supervisor) Fatal() <-chan error { return s.fatal }

func (s *Supervisor) Logs() *LogBuffer { return s.logs }

func (s *Supervisor) Prepare(ctx context.Context, revisionID int64, bundle store.Bundle) (string, error) {
	root := filepath.Join(s.stateDir, "revisions", fmt.Sprint(revisionID))
	if err := s.prepareAt(ctx, root, bundle); err != nil {
		return "", err
	}
	return root, nil
}

func (s *Supervisor) Preflight(ctx context.Context, bundle store.Bundle) error {
	root, err := os.MkdirTemp(s.stateDir, "preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	return s.prepareAt(ctx, root, bundle)
}

func (s *Supervisor) prepareAt(ctx context.Context, root string, bundle store.Bundle) error {
	norm := bundle.Normalized()
	files, err := render.GenerateAt(bundle.Config, norm, root)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	if err := render.WriteAll(root, files); err != nil {
		return err
	}
	checks := [][]string{
		{bundle.Config.DNS.Binary, "test", "-c", filepath.Join(root, "smartdns", "smartdns.conf")},
		{"haproxy", "-c", "-f", filepath.Join(root, "haproxy", "haproxy.cfg")},
		{"nft", "-c", "-f", filepath.Join(root, "nftables", "5gws.nft")},
	}
	for _, check := range checks {
		if err := runCheck(ctx, s.logs, check[0], check[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Supervisor) Start(ctx context.Context, root string, bundle store.Bundle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		return errors.New("supervisor is already running")
	}
	group, err := s.startGroup(ctx, root, bundle)
	if err != nil {
		return err
	}
	s.current = group
	go s.watch(group)
	return nil
}

func (s *Supervisor) Boot(ctx context.Context, root string, bundle store.Bundle) error {
	if err := runCheck(ctx, s.logs, "nft", "-f", filepath.Join(root, "nftables", "5gws.nft")); err != nil {
		return err
	}
	return s.Start(ctx, root, bundle)
}

func (s *Supervisor) Apply(ctx context.Context, root string, bundle store.Bundle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.current
	if previous != nil {
		if err := stopGroup(previous); err != nil {
			s.reportFatal(err)
			return err
		}
	}
	if err := runCheck(ctx, s.logs, "nft", "-f", filepath.Join(root, "nftables", "5gws.nft")); err != nil {
		if previous != nil {
			s.restoreLocked(ctx, previous)
		}
		return err
	}
	group, err := s.startGroup(ctx, root, bundle)
	if err != nil {
		if errors.Is(err, errShutdownTimeout) {
			s.reportFatal(err)
			return err
		}
		if previous != nil {
			s.restoreLocked(ctx, previous)
		}
		return err
	}
	s.current = group
	go s.watch(group)
	return nil
}

func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		if err := stopGroup(s.current); err != nil {
			s.reportFatal(err)
		}
		s.current = nil
	}
}

func (s *Supervisor) restoreLocked(ctx context.Context, previous *processGroup) {
	if err := runCheck(ctx, s.logs, "nft", "-f", filepath.Join(previous.root, "nftables", "5gws.nft")); err != nil {
		s.reportFatal(fmt.Errorf("restore nftables: %w", err))
		return
	}
	restored, err := s.startGroup(ctx, previous.root, previous.bundle)
	if err != nil {
		s.reportFatal(fmt.Errorf("restore active process group: %w", err))
		return
	}
	s.current = restored
	go s.watch(restored)
}

func (s *Supervisor) startGroup(operationCtx context.Context, root string, bundle store.Bundle) (*processGroup, error) {
	ctx, cancel := context.WithCancel(s.lifecycle)
	group := &processGroup{cancel: cancel, done: make(chan error, 1), root: root, bundle: bundle, stopped: make(chan struct{})}
	commands := managedCommands(root, bundle)
	for _, spec := range commands {
		cmd := exec.CommandContext(ctx, spec[0], spec[1:]...)
		cmd.Stdout = io.MultiWriter(os.Stdout, s.logs)
		cmd.Stderr = io.MultiWriter(os.Stderr, s.logs)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			startErr := fmt.Errorf("start %s: %w", spec[0], err)
			if stopErr := stopGroup(group); stopErr != nil {
				return nil, errors.Join(startErr, stopErr)
			}
			return nil, startErr
		}
		group.cmds = append(group.cmds, cmd)
		group.wait.Add(1)
		go func(name string, cmd *exec.Cmd) {
			defer group.wait.Done()
			if err := cmd.Wait(); ctx.Err() == nil {
				if err == nil {
					err = errors.New("process exited successfully but was expected to remain running")
				}
				select {
				case group.done <- fmt.Errorf("managed process %s exited: %w", name, err):
				default:
				}
			}
		}(spec[0], cmd)
	}
	group.wait.Add(1)
	go func() {
		defer group.wait.Done()
		log.SetOutput(io.MultiWriter(os.Stderr, s.logs))
		compiled, err := rules.Compile(bundle.Normalized())
		if err == nil {
			err = quic.RunCompiled(ctx, bundle.Config, bundle.Normalized(), compiled)
		}
		if ctx.Err() == nil {
			if err == nil {
				err = errors.New("gateway exited without an error")
			}
			select {
			case group.done <- fmt.Errorf("gateway exited: %w", err):
			default:
			}
		}
	}()
	time.Sleep(250 * time.Millisecond)
	select {
	case err := <-group.done:
		if stopErr := stopGroup(group); stopErr != nil {
			return nil, errors.Join(err, stopErr)
		}
		return nil, err
	default:
		for _, address := range readinessAddresses(bundle) {
			if err := waitTCP(operationCtx, address, readinessTimeout); err != nil {
				if stopErr := stopGroup(group); stopErr != nil {
					return nil, errors.Join(err, stopErr)
				}
				return nil, err
			}
		}
		return group, nil
	}
}

func managedCommands(root string, bundle store.Bundle) [][]string {
	commands := [][]string{
		{bundle.Config.DNS.Binary, "run", "-c", filepath.Join(root, "smartdns", "smartdns.conf")},
		{"haproxy", "-db", "-f", filepath.Join(root, "haproxy", "haproxy.cfg")},
	}
	for _, exit := range bundle.Config.Exits {
		if exit.Type == "shadowsocks-rust" {
			commands = append(commands, []string{"sslocal", "-c", filepath.Join(root, "ssrust", ssrust.ConfigFileName(exit))})
		}
	}
	return commands
}

func readinessAddresses(bundle store.Bundle) []string {
	addresses := []string{
		loopbackAddress(bundle.Config.DNS.ListenTCP),
		net.JoinHostPort("127.0.0.1", fmt.Sprint(bundle.Config.Network.HTTPRedirectPort)),
		net.JoinHostPort("127.0.0.1", fmt.Sprint(bundle.Config.Network.HTTPSRedirectPort)),
		net.JoinHostPort("127.0.0.1", fmt.Sprint(bundle.Config.Network.TCPRedirectPort)),
	}
	for _, exit := range bundle.Config.Exits {
		if exit.Type == "shadowsocks-rust" {
			addresses = append(addresses, net.JoinHostPort(exit.ListenAddress, fmt.Sprint(exit.ListenPort)))
		}
	}
	return addresses
}

func loopbackAddress(address string) string {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return net.JoinHostPort("127.0.0.1", port)
}

func waitTCP(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("readiness probe timed out: %s", address)
}

func (s *Supervisor) watch(group *processGroup) {
	var err error
	select {
	case err = <-group.done:
	case <-group.stopped:
		return
	}
	s.mu.Lock()
	current := s.current == group
	s.mu.Unlock()
	if current {
		s.reportFatal(err)
	}
}

func (s *Supervisor) reportFatal(err error) {
	select {
	case s.fatal <- err:
	default:
	}
}

func stopGroup(group *processGroup) error {
	group.stopOnce.Do(func() { close(group.stopped) })
	group.cancel()
	stopCommands(group.cmds)
	done := make(chan struct{})
	go func() {
		group.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(shutdownTimeout):
		return fmt.Errorf("%w after %s", errShutdownTimeout, shutdownTimeout)
	}
}

func stopCommands(cmds []*exec.Cmd) {
	for _, cmd := range cmds {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	}
	time.Sleep(100 * time.Millisecond)
	for _, cmd := range cmds {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}

func runCheck(ctx context.Context, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	data, err := cmd.CombinedOutput()
	if len(data) > 0 {
		_, _ = out.Write(data)
	}
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, data)
	}
	return nil
}
