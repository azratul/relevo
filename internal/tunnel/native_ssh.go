package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// startSSH opens an SSH reverse tunnel (like `ssh -R 80:127.0.0.1:port host`)
// with the built-in client: no ssh binary, no user keys, no agent. The host
// key is pinned on first use in relevo's own known_hosts file.
func startSSH(ctx context.Context, addr, user string, localPort int, urlRe *regexp.Regexp) (*Tunnel, error) {
	cfg := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			// Anonymous services accept keyboard-interactive/none with no prompts.
			ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return nil, nil }),
		},
		HostKeyCallback: tofuHostKeys(filepath.Join(stateDir(), "known_hosts")),
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256},
		Timeout:       15 * time.Second,
		ClientVersion: "SSH-2.0-relevo",
	}
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}
	c, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		raw.Close()
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	go ssh.DiscardRequests(reqs)
	t := &Tunnel{done: make(chan struct{})}
	var once sync.Once
	shutdown := func(err error) {
		once.Do(func() {
			t.err = err
			c.Close()
			close(t.done)
		})
	}
	t.stop = func() { shutdown(nil) }
	// Any step of the setup can block on a silent server: the context deadline ends it.
	stopSetup := context.AfterFunc(ctx, func() { shutdown(ctx.Err()) })
	defer stopSetup()
	// Incoming connections arrive as "forwarded-tcpip" channels. We accept
	// them ourselves: x/crypto's Listen only matches the exact bind address,
	// and these services report a different one.
	go func() {
		for nc := range chans {
			if nc.ChannelType() != "forwarded-tcpip" {
				_ = nc.Reject(ssh.UnknownChannelType, "unsupported")
				continue
			}
			ch, creqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go ssh.DiscardRequests(creqs)
			go pipeTo(chanConn{ch}, "127.0.0.1:"+strconv.Itoa(localPort))
		}
		shutdown(errors.New("connection closed by server"))
	}()
	// Remote forward of port 80, as `ssh -R 80:…` does.
	fwd := struct {
		Addr string
		Port uint32
	}{"localhost", 80}
	if ok, _, err := c.SendRequest("tcpip-forward", true, ssh.Marshal(&fwd)); err != nil || !ok {
		shutdown(err)
		return nil, fmt.Errorf("remote forward refused (%v)", err)
	}
	// The services print the public URL on a session channel (like `ssh -T`).
	sch, sreqs, err := c.OpenChannel("session", nil)
	if err != nil {
		shutdown(err)
		return nil, err
	}
	go ssh.DiscardRequests(sreqs)
	// No reply requested: localhost.run never answers a want-reply "shell".
	if _, err := sch.SendRequest("shell", false, nil); err != nil {
		shutdown(err)
		return nil, fmt.Errorf("session refused (%v)", err)
	}
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.Copy(pw, io.MultiReader(sch, sch.Stderr()))
		pw.Close()
		shutdown(errors.New("session closed by server"))
	}()
	found := make(chan string, 1)
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		sent := false
		for sc.Scan() {
			line := ansi.ReplaceAllString(sc.Text(), "")
			t.log(line)
			if m := urlRe.FindStringSubmatch(line); m != nil && !sent {
				u := m[0]
				if len(m) > 1 {
					u = m[1]
				}
				found <- strings.TrimRight(u, "/")
				sent = true
			}
		}
	}()
	go func() { // keepalive: detect dead tunnels
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-tick.C:
				if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					shutdown(err)
					return
				}
			}
		}
	}()
	select {
	case u := <-found:
		t.URL = u
		return t, nil
	case <-t.done:
		return nil, fmt.Errorf("closed before giving a URL: %v\n%s", t.err, t.Output())
	case <-ctx.Done():
		shutdown(ctx.Err())
		return nil, fmt.Errorf("no public URL: %w\n%s", ctx.Err(), t.Output())
	}
}

// chanConn adapts an SSH channel to the subset of net.Conn used by pipeTo.
type chanConn struct{ ssh.Channel }

func (chanConn) LocalAddr() net.Addr              { return nil }
func (chanConn) RemoteAddr() net.Addr             { return nil }
func (chanConn) SetDeadline(time.Time) error      { return nil }
func (chanConn) SetReadDeadline(time.Time) error  { return nil }
func (chanConn) SetWriteDeadline(time.Time) error { return nil }

// pipeTo proxies an accepted tunnel connection to the local relay.
func pipeTo(remote net.Conn, local string) {
	defer remote.Close()
	l, err := net.DialTimeout("tcp", local, 5*time.Second)
	if err != nil {
		return
	}
	defer l.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(l, remote); done <- struct{}{} }()
	go func() { _, _ = io.Copy(remote, l); done <- struct{}{} }()
	<-done
}

// tofuHostKeys pins host keys on first use (like StrictHostKeyChecking=accept-new).
func tofuHostKeys(path string) ssh.HostKeyCallback {
	var mu sync.Mutex
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		mu.Lock()
		defer mu.Unlock()
		if _, err := os.Stat(path); err == nil {
			cb, err := knownhosts.New(path)
			if err != nil {
				return err
			}
			err = cb(hostname, remote, key)
			var ke *knownhosts.KeyError
			if err == nil || !errors.As(err, &ke) {
				return err
			}
			for _, w := range ke.Want {
				if w.Key.Type() == key.Type() {
					return err // same key type, different key: refuse (possible impersonation)
				}
			}
			// unknown host, or a key type we had not pinned yet: pin it
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
		return err
	}
}
