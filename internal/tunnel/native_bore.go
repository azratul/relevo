package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// startBore speaks the bore protocol (github.com/ekzhang/bore, MIT): JSON
// messages terminated by a NUL byte on control port 7835. The public side is
// plain TCP: http://server:port, WITHOUT TLS.
func startBore(ctx context.Context, server string, localPort int) (*Tunnel, error) {
	const controlPort = "7835"
	var d net.Dialer
	ctrl, err := d.DialContext(ctx, "tcp", net.JoinHostPort(server, controlPort))
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(ctrl)
	if err := boreSend(ctrl, map[string]any{"Hello": 0}); err != nil {
		ctrl.Close()
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = ctrl.SetReadDeadline(dl)
	}
	msg, err := boreRecv(r)
	if err != nil {
		ctrl.Close()
		return nil, err
	}
	_ = ctrl.SetReadDeadline(time.Time{})
	var hello struct {
		Hello *int
		Error *string
	}
	_ = json.Unmarshal(msg, &hello)
	if hello.Error != nil {
		ctrl.Close()
		return nil, errors.New(*hello.Error)
	}
	if hello.Hello == nil {
		ctrl.Close()
		return nil, fmt.Errorf("unexpected bore reply %s (does the server require a secret?)", msg)
	}
	t := &Tunnel{URL: "http://" + net.JoinHostPort(server, strconv.Itoa(*hello.Hello)), done: make(chan struct{})}
	var once sync.Once
	shutdown := func(err error) { once.Do(func() { t.err = err; ctrl.Close(); close(t.done) }) }
	t.stop = func() { shutdown(nil) }
	t.log("listening at " + t.URL)
	go func() {
		for {
			// The server sends a heartbeat every ~500 ms; silence means a dead tunnel.
			_ = ctrl.SetReadDeadline(time.Now().Add(15 * time.Second))
			msg, err := boreRecv(r)
			if err != nil {
				shutdown(err)
				return
			}
			var m struct{ Connection *string }
			if json.Unmarshal(msg, &m) == nil && m.Connection != nil {
				go boreAccept(server+":"+controlPort, *m.Connection, localPort)
			}
		}
	}()
	return t, nil
}

func boreAccept(control, id string, localPort int) {
	c, err := net.DialTimeout("tcp", control, 10*time.Second)
	if err != nil {
		return
	}
	if err := boreSend(c, map[string]any{"Accept": id}); err != nil {
		c.Close()
		return
	}
	pipeTo(c, "127.0.0.1:"+strconv.Itoa(localPort))
}

func boreSend(c net.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.Write(append(b, 0))
	return err
}

func boreRecv(r *bufio.Reader) ([]byte, error) {
	b, err := r.ReadBytes(0)
	if err != nil {
		return nil, err
	}
	if len(b) > 1024 {
		return nil, errors.New("bore frame too large")
	}
	return b[:len(b)-1], nil
}
